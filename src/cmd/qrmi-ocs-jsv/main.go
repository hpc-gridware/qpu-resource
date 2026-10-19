// Copyright 2026 Pasqal and HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

// qrmi-ocs-jsv is a server-side OCS Job Submission Verifier. It rejects qpu
// requests the queue prolog cannot honor and adds a default qpu_slots request
// so every QPU job draws from the slot pool.
//
// qmaster starts the JSV from jsv_url without arguments. The QRMI config path
// and slots resource can be changed with QRMI_OCS_JSV_CONFIG and
// QRMI_OCS_JSV_SLOTS_RESOURCE; an empty slots resource disables injection.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/hpc-gridware/qpu-resource/src/internal/qrmiocs"
)

const (
	defaultConfigPath        = "/etc/qrmi/qrmi_config.json"
	defaultResourceName      = "qpu"
	defaultSlotsResourceName = "qpu_slots"
)

func main() {
	configPath := envOr("QRMI_OCS_JSV_CONFIG", defaultConfigPath)
	slotsResource := envOr("QRMI_OCS_JSV_SLOTS_RESOURCE", defaultSlotsResourceName)
	if err := runProtocol(os.Stdin, os.Stdout, configPath, slotsResource); err != nil {
		fmt.Fprintf(os.Stderr, "qrmi-ocs-jsv: %v\n", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

// runProtocol serves the JSV line protocol until QUIT or end of input.
func runProtocol(in io.Reader, out io.Writer, configPath, slotsResource string) error {
	scanner := bufio.NewScanner(in)
	w := bufio.NewWriter(out)
	params := map[string]string{}
	for scanner.Scan() {
		cmd, rest, _ := strings.Cut(scanner.Text(), " ")
		var reply []string
		switch cmd {
		case "START":
			params = map[string]string{}
			reply = []string{"STARTED"}
		case "PARAM":
			k, v, _ := strings.Cut(rest, " ")
			params[k] = v
		case "BEGIN":
			reply = verify(params, configPath, slotsResource)
		case "QUIT":
			return nil
		}
		for _, line := range reply {
			fmt.Fprintln(w, line)
		}
		if err := w.Flush(); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// verify returns the PARAM corrections and RESULT line for one job.
func verify(params map[string]string, configPath, slotsResource string) []string {
	if _, ok := parseList(params["l_soft"])[defaultResourceName]; ok {
		return []string{"RESULT STATE REJECT qpu must be a hard resource request"}
	}
	hard := parseList(params["l_hard"])
	raw, ok := hard[defaultResourceName]
	if !ok {
		return []string{"RESULT STATE ACCEPT"}
	}
	backend, err := qrmiocs.ParseGrantedBackend(raw)
	if err != nil || strings.ContainsAny(backend, "*?|&!") {
		return []string{fmt.Sprintf("RESULT STATE REJECT qpu=%s must name exactly one backend", raw)}
	}
	known, err := configuredResources(configPath)
	if err != nil {
		return []string{fmt.Sprintf("RESULT STATE REJECT_WAIT cannot read %s: %v", configPath, err)}
	}
	if !known[backend] {
		return []string{fmt.Sprintf("RESULT STATE REJECT qpu=%s is not in %s", backend, configPath)}
	}
	if _, ok := hard[slotsResource]; slotsResource == "" || ok {
		return []string{"RESULT STATE ACCEPT"}
	}
	hard[slotsResource] = "1"
	return []string{
		"PARAM l_hard " + joinList(hard),
		fmt.Sprintf("RESULT STATE CORRECT added %s=1", slotsResource),
	}
}

func configuredResources(path string) (map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Resources []struct {
			Name string `json:"name"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, r := range cfg.Resources {
		known[r.Name] = true
	}
	return known, nil
}

func parseList(s string) map[string]string {
	m := map[string]string{}
	for _, item := range strings.Split(s, ",") {
		if k, v, ok := strings.Cut(item, "="); ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}

func joinList(m map[string]string) string {
	items := make([]string, 0, len(m))
	for k, v := range m {
		items = append(items, k+"="+v)
	}
	sort.Strings(items)
	return strings.Join(items, ",")
}
