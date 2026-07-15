// Copyright 2026 Pasqal and HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

// qrmi-ocs-prolog-go is the OCS queue prolog hook. It resolves the granted
// QRMI backend, applies backend-prefixed environment variables from
// qrmi_config.json, acquires an acquisition token, exports runtime
// variables into the job environment, and writes a metadata TSV that the
// matching epilog will use to release the token.
//
// This is the Go port of src/cmd/qrmi-ocs-prolog/main.c. The external
// contract (env var names, metadata format, exit codes, log line prefixes)
// is identical so the two binaries are interchangeable during migration.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hpc-gridware/qpu-resource/src/internal/qrmi"
	"github.com/hpc-gridware/qpu-resource/src/internal/qrmiocs"
)

const (
	defaultConfigPath        = "/etc/qrmi/qrmi_config.json"
	defaultResourceName      = "qpu"
	defaultSlotsResourceName = "qpu_slots"
	component                = "qrmi-ocs-prolog"
)

var log = qrmiocs.NewLogger(component)
var errRequestedResourceNotFound = errors.New("requested resource not found")

func main() {
	if len(os.Args) > 2 {
		log.Error("expected at most one job-owner argument")
		os.Exit(1)
	}
	jobOwner := ""
	if len(os.Args) == 2 {
		jobOwner = os.Args[1]
	}
	if err := runForOwner(jobOwner); err != nil {
		log.Error("%v", err)
		os.Exit(1)
	}
}

func run() error {
	return runForOwner("")
}

func runForOwner(jobOwner string) error {
	cfg := loadHookConfig()

	granted, err := readGranted(cfg.ResourceName)
	if err != nil {
		return err
	}
	backend, err := qrmiocs.ParseGrantedBackend(granted)
	if err != nil {
		return fmt.Errorf("parse granted resource value %q: %w", granted, err)
	}

	jobEnv, err := qrmiocs.OpenJobEnv()
	if err != nil {
		return err
	}
	defer jobEnv.Close()

	if err := jobEnv.ApplyDefaultRustLog(); err != nil {
		return reportError(jobEnv, fmt.Errorf("apply default RUST_LOG: %w", err))
	}
	qrmi.SetLogCallback(log.QRMILog)

	qcfg, err := qrmi.LoadConfig(cfg.ConfigPath)
	if err != nil {
		return reportError(jobEnv, fmt.Errorf("load qrmi config %s: %w", cfg.ConfigPath, err))
	}
	defer qcfg.Close()

	def, err := qcfg.ResourceDef(backend)
	if err != nil {
		return reportError(jobEnv, fmt.Errorf("resource %q not in %s: %w", backend, cfg.ConfigPath, err))
	}
	defer def.Close()

	if err := exportBackendEnv(jobEnv, backend, def.Environments()); err != nil {
		return reportError(jobEnv, fmt.Errorf("apply backend env for %s: %w", backend, err))
	}
	if err := exportSchedulerJobEnv(jobEnv, cfg.SlotsResourceName, jobOwner); err != nil {
		return reportError(jobEnv, fmt.Errorf("export scheduler job env: %w", err))
	}

	resource, err := qrmi.NewResource(backend, def.Type())
	if err != nil {
		return reportError(jobEnv, fmt.Errorf("new qrmi resource %s: %w", backend, err))
	}
	defer resource.Close()

	// qpu-fraction

	if ok, err := resource.IsAccessible(); err != nil {
		return reportError(jobEnv, fmt.Errorf("backend %s accessibility check: %w", backend, err))
	} else if !ok {
		return reportError(jobEnv, fmt.Errorf("backend %s is not accessible", backend))
	}

	token, err := resource.Acquire()
	if err != nil {
		return reportError(jobEnv, fmt.Errorf("acquire token for %s: %w", backend, err))
	}

	rec := qrmiocs.Record{
		Name:          backend,
		Type:          def.Type(),
		Token:         token,
		AcquiredEpoch: time.Now().Unix(),
	}
	typeStr := def.TypeString()
	if typeStr == "" {
		typeStr = strconv.Itoa(def.Type())
	}

	if err := exportRuntimeEnv(jobEnv, backend, token, rec, typeStr); err != nil {
		_ = resource.Release(token)
		return reportError(jobEnv, fmt.Errorf("export runtime env: %w", err))
	}

	metaPath := qrmiocs.ResolveMetadataPath()
	if err := qrmiocs.WriteAtomic(metaPath, []qrmiocs.Record{rec}); err != nil {
		_ = resource.Release(token)
		return reportError(jobEnv, fmt.Errorf("write metadata %s: %w", metaPath, err))
	}
	if err := jobEnv.Set("QRMI_OCS_METADATA_PATH", metaPath); err != nil {
		_ = resource.Release(token)
		return reportError(jobEnv, fmt.Errorf("export metadata path: %w", err))
	}

	if err := jobEnv.Set(qrmiocs.PrologStatusKey, "success"); err != nil {
		_ = resource.Release(token)
		return reportError(jobEnv, fmt.Errorf("export prolog status: %w", err))
	}

	log.Info("acquired 1 backend resource(s): %s", backend)
	return nil
}

// hookConfig captures the small set of env-driven parameters that change
// the prolog's behavior. Keeping them in one struct makes the run loop
// easier to read.
type hookConfig struct {
	ConfigPath        string
	ResourceName      string
	SlotsResourceName string
}

func loadHookConfig() hookConfig {
	cfg := hookConfig{
		ConfigPath:        os.Getenv("QRMI_OCS_CONFIG_PATH"),
		ResourceName:      os.Getenv("QRMI_OCS_RESOURCE_NAME"),
		SlotsResourceName: os.Getenv("QRMI_OCS_SLOTS_RESOURCE_NAME"),
	}
	if cfg.ConfigPath == "" {
		cfg.ConfigPath = defaultConfigPath
	}
	if cfg.ResourceName == "" {
		cfg.ResourceName = defaultResourceName
	}
	if cfg.SlotsResourceName == "" {
		cfg.SlotsResourceName = defaultSlotsResourceName
	}
	return cfg
}

// readGranted returns the value of the scheduler's granted-resource env
// variable. It tries SGE_HGR_<resource> first (hard request), then
// SGE_SGR_<resource> (soft request), and then the dispatched job's qstat
// resource list. The selected host's complex_values is the final fallback
// for non-consumable host selectors.
func readGranted(resourceName string) (string, error) {
	if v := os.Getenv("SGE_HGR_" + resourceName); v != "" {
		return v, nil
	}
	if v := os.Getenv("SGE_SGR_" + resourceName); v != "" {
		return v, nil
	}
	if v, err := readJobRequestedResource(resourceName); err == nil {
		return v, nil
	}
	if v, err := readHostComplexValue(resourceName); err == nil && v != "" {
		return v, nil
	}
	return "", fmt.Errorf("no granted value found in SGE_HGR_%s or SGE_SGR_%s", resourceName, resourceName)
}

func readJobRequestedResource(resourceName string) (string, error) {
	jobID := os.Getenv("JOB_ID")
	if jobID == "" {
		return "", errRequestedResourceNotFound
	}
	out, err := exec.Command(qstatPath(), "-j", jobID).Output()
	if err != nil {
		return "", fmt.Errorf("read scheduler state for job %s: %w", jobID, err)
	}
	return parseJobRequestedResource(out, resourceName)
}

func parseJobRequestedResource(out []byte, resourceName string) (string, error) {
	for _, field := range []string{"hard_resource_list:", "soft_resource_list:"} {
		if value, ok := parseResourceListField(out, field, resourceName); ok {
			return value, nil
		}
	}
	return "", fmt.Errorf("%w: %s", errRequestedResourceNotFound, resourceName)
}

func parseResourceListField(out []byte, field, resourceName string) (string, bool) {
	scanner := bufio.NewScanner(bytes.NewReader(out))
	var values strings.Builder
	collecting := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, field) {
			collecting = true
			values.WriteString(strings.TrimSpace(strings.TrimPrefix(line, field)))
			continue
		}
		if collecting && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			values.WriteString(strings.TrimSpace(line))
			continue
		}
		if collecting {
			break
		}
	}
	for _, item := range strings.Split(strings.ReplaceAll(values.String(), "\\", ""), ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(item), "=")
		if ok && strings.TrimSpace(key) == resourceName {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

func readHostComplexValue(resourceName string) (string, error) {
	host := os.Getenv("HOST")
	if host == "" {
		host = os.Getenv("HOSTNAME")
	}
	if host == "" {
		return "", fmt.Errorf("HOST is not set")
	}
	out, err := exec.Command(qconfPath(), "-se", host).Output()
	if err != nil {
		return "", err
	}
	return parseHostComplexValue(out, resourceName)
}

func qconfPath() string {
	if p := os.Getenv("QRMI_OCS_QCONF_PATH"); p != "" {
		return p
	}
	if dir := os.Getenv("SGE_BINARY_PATH"); dir != "" {
		return filepath.Join(dir, "qconf")
	}
	return "qconf"
}

func qstatPath() string {
	if p := os.Getenv("QRMI_OCS_QSTAT_PATH"); p != "" {
		return p
	}
	if dir := os.Getenv("SGE_BINARY_PATH"); dir != "" {
		return filepath.Join(dir, "qstat")
	}
	return "qstat"
}

func parseHostComplexValue(out []byte, resourceName string) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(out))
	var values strings.Builder
	collecting := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "complex_values") {
			collecting = true
			values.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "complex_values")))
			continue
		}
		if collecting && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			values.WriteByte(',')
			values.WriteString(strings.TrimSpace(line))
			continue
		}
		if collecting {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	for _, item := range strings.Split(strings.ReplaceAll(values.String(), "\\", ""), ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(item), "=")
		if ok && strings.TrimSpace(key) == resourceName {
			return strings.TrimSpace(value), nil
		}
	}
	return "", fmt.Errorf("%s not found in host complex_values", resourceName)
}

// exportBackendEnv applies the backend-prefixed environment variables
// declared in qrmi_config.json. The prefix is the backend name (for
// example "EMU_FREE"); a configured key "QRMI_PASQAL_CLOUD_AUTH_ENDPOINT"
// becomes "EMU_FREE_QRMI_PASQAL_CLOUD_AUTH_ENDPOINT" in the job env.
//
// A backend-prefixed value already present in the process env wins, so
// admins can override config defaults via the queue env.
func exportBackendEnv(je *qrmiocs.JobEnv, backend string, env []qrmi.EnvVar) error {
	for _, e := range env {
		if e.Key == "" {
			continue
		}
		name := backend + "_" + e.Key
		if err := je.Set(name, e.Value); err != nil {
			return err
		}
	}
	return nil
}

func exportSchedulerJobEnv(je *qrmiocs.JobEnv, slotsResourceName, jobOwner string) error {
	uid, err := schedulerJobUID(jobOwner)
	if err != nil {
		return err
	}
	pairs := [][2]string{
		{"QRMI_JOB_UID", strconv.Itoa(uid)},
		{"QRMI_JOB_ID", os.Getenv("JOB_ID")},
	}
	if slotsResourceName != "" {
		if slots, ok, err := readGrantedSlots(slotsResourceName); err != nil {
			return err
		} else if ok {
			pairs = append(pairs, [2]string{"QRMI_JOB_QPU_SLOTS", strconv.Itoa(slots)})
		}
	}
	for _, kv := range pairs {
		if kv[1] == "" {
			continue
		}
		if err := je.Set(kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

func schedulerJobUID(jobOwner string) (int, error) {
	if jobOwner == "" {
		return os.Getuid(), nil
	}
	account, err := user.Lookup(jobOwner)
	if err != nil {
		return 0, fmt.Errorf("look up scheduler job owner %q: %w", jobOwner, err)
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil || uid < 0 {
		return 0, fmt.Errorf("invalid uid %q for scheduler job owner %q", account.Uid, jobOwner)
	}
	return uid, nil
}

func readGrantedSlots(resourceName string) (int, bool, error) {
	raw := os.Getenv("SGE_HGR_" + resourceName)
	if raw == "" {
		raw = os.Getenv("SGE_SGR_" + resourceName)
	}
	if raw == "" {
		var err error
		raw, err = readJobRequestedResource(resourceName)
		if errors.Is(err, errRequestedResourceNotFound) {
			return 0, false, nil
		}
		if err != nil {
			return 0, false, err
		}
	}
	slots, err := parseGrantedSlots(raw)
	if err != nil {
		return 0, false, fmt.Errorf("parse granted %s value %q: %w", resourceName, raw, err)
	}
	return slots, true, nil
}

func parseGrantedSlots(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if value, _, ok := strings.Cut(raw, "("); ok {
		raw = strings.TrimSpace(value)
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, err
	}
	slots := int(value)
	if slots < 1 || float64(slots) != value {
		return 0, fmt.Errorf("slot count must be a positive integer")
	}
	return slots, nil
}

// exportRuntimeEnv writes the standard set of runtime variables into the
// job environment. The names and values are part of the public contract
// surfaced to job scripts and accounting hooks; they must match the C
// version exactly.
func exportRuntimeEnv(je *qrmiocs.JobEnv, backend, token string, rec qrmiocs.Record, typeStr string) error {
	tokenKey := backend + "_QRMI_JOB_ACQUISITION_TOKEN"
	pairs := [][2]string{
		{tokenKey, token},
		{"QRMI_JOB_QPU_RESOURCES", backend},
		{"QRMI_JOB_QPU_TYPES", typeStr},
		{"qrmi_resources", backend},
		{"qrmi_resource_types", typeStr},
		{"qrmi_acquired_count", "1"},
	}
	for _, kv := range pairs {
		if err := je.Set(kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

// reportError records the failure in the job env so it surfaces in the
// running job and the epilog, then returns the original error so main
// can exit with the right status. The C version uses set_plugin_error +
// goto fail; this collapses both into a single return path.
func reportError(je *qrmiocs.JobEnv, err error) error {
	if perr := je.SetPluginError(err.Error()); perr != nil {
		log.Warn("failed to record plugin error: %v", perr)
	}
	return err
}
