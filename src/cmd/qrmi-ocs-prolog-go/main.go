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
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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

// checkTrustedExecutable is replaced in tests, which run fake scheduler
// commands from a temporary directory.
var checkTrustedExecutable = trustedExecutable

// main exits with the codes OCS interprets for a queue prolog: 0 when the
// job is set up or requests no QPU, 100 (job error) when the job's request
// cannot be served, 99 (reschedule) when the backend is temporarily
// unavailable, and 1 (queue error) only for problems of the host or its
// configuration.
func main() {
	// qrmi-ocs-launch starts the hook with a clean environment; this is a
	// second check in case the hook was started directly.
	if err := qrmiocs.ResetProcessEnv(); err != nil {
		log.Error("environment: %v", err)
		os.Exit(qrmiocs.ExitCode(err))
	}
	cfg, jobOwner, err := parseHookArgs(os.Args[1:])
	if err != nil {
		log.Error("%v", err)
		os.Exit(qrmiocs.ExitHostError)
	}
	err = runWithConfig(jobOwner, cfg)
	if err != nil {
		log.Error("%v", err)
	}
	os.Exit(qrmiocs.ExitCode(err))
}

func runWithConfig(jobOwner string, cfg hookConfig) error {
	granted, err := readGranted(cfg.ResourceName, cfg.QstatPath)
	if errors.Is(err, errRequestedResourceNotFound) {
		log.Info("job requests no %s resource; nothing to do", cfg.ResourceName)
		return nil
	}
	if err != nil {
		return err
	}
	backend, err := qrmiocs.ParseGrantedBackend(granted)
	if err != nil {
		return qrmiocs.JobError(fmt.Errorf("parse granted resource value %q: %w", granted, err))
	}
	if err := checkHostOffers(cfg, backend); err != nil {
		return err
	}

	jobEnv, err := qrmiocs.OpenJobEnv()
	if err != nil {
		return err
	}
	defer jobEnv.Close()

	if err := jobEnv.ApplyRustLog(cfg.LogLevel); err != nil {
		return reportError(jobEnv, fmt.Errorf("apply RUST_LOG: %w", err))
	}
	qrmi.SetLogCallback(log.QRMILog)

	qcfg, err := qrmi.LoadConfig(cfg.ConfigPath)
	if err != nil {
		return reportError(jobEnv, fmt.Errorf("load qrmi config %s: %w", cfg.ConfigPath, err))
	}
	defer qcfg.Close()

	def, err := qcfg.ResourceDef(backend)
	if err != nil {
		return reportError(jobEnv, qrmiocs.JobError(fmt.Errorf("resource %q not in %s: %w", backend, cfg.ConfigPath, err)))
	}
	defer def.Close()

	if err := exportBackendEnv(jobEnv, backend, def.Environments()); err != nil {
		return reportError(jobEnv, fmt.Errorf("apply backend env for %s: %w", backend, err))
	}
	if err := exportSchedulerJobEnv(jobEnv, cfg, jobOwner); err != nil {
		return reportError(jobEnv, fmt.Errorf("export scheduler job env: %w", err))
	}

	resource, err := qrmi.NewResource(backend, def.Type())
	if err != nil {
		return reportError(jobEnv, fmt.Errorf("new qrmi resource %s: %w", backend, err))
	}
	defer resource.Close()

	if ok, err := resource.IsAccessible(); err != nil {
		return reportError(jobEnv, qrmiocs.RetryError(fmt.Errorf("backend %s accessibility check: %w", backend, err)))
	} else if !ok {
		return reportError(jobEnv, qrmiocs.RetryError(fmt.Errorf("backend %s is not accessible", backend)))
	}

	token, err := resource.Acquire()
	if err != nil {
		return reportError(jobEnv, qrmiocs.RetryError(fmt.Errorf("acquire token for %s: %w", backend, err)))
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

	metaPath, err := qrmiocs.ResolveMetadataPath()
	if err != nil {
		_ = resource.Release(token)
		return reportError(jobEnv, fmt.Errorf("resolve metadata path: %w", err))
	}
	if err := qrmiocs.WriteAtomic(metaPath, []qrmiocs.Record{rec}); err != nil {
		_ = resource.Release(token)
		return reportError(jobEnv, fmt.Errorf("write metadata %s: %w", metaPath, err))
	}

	if err := jobEnv.Set(qrmiocs.PrologStatusKey, "success"); err != nil {
		_ = resource.Release(token)
		return reportError(jobEnv, fmt.Errorf("export prolog status: %w", err))
	}

	log.Info("acquired 1 backend resource(s): %s", backend)
	return nil
}

type hookConfig struct {
	ConfigPath        string
	ResourceName      string
	SlotsResourceName string
	QstatPath         string
	LogLevel          string
}

func defaultHookConfig() hookConfig {
	return hookConfig{
		ConfigPath:        defaultConfigPath,
		ResourceName:      defaultResourceName,
		SlotsResourceName: defaultSlotsResourceName,
	}
}

func parseHookArgs(args []string) (hookConfig, string, error) {
	cfg := defaultHookConfig()
	fs := flag.NewFlagSet(component, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.ConfigPath, "config", cfg.ConfigPath, "")
	fs.StringVar(&cfg.ResourceName, "resource", cfg.ResourceName, "")
	fs.StringVar(&cfg.SlotsResourceName, "slots-resource", cfg.SlotsResourceName, "")
	fs.StringVar(&cfg.QstatPath, "qstat", cfg.QstatPath, "")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "")
	if err := fs.Parse(args); err != nil {
		return hookConfig{}, "", err
	}
	if fs.NArg() > 1 {
		return hookConfig{}, "", errors.New("expected at most one job-owner argument")
	}
	if cfg.ConfigPath == "" || cfg.ResourceName == "" || !filepath.IsAbs(cfg.QstatPath) {
		return hookConfig{}, "", errors.New("config, resource, and absolute qstat path are required")
	}
	jobOwner := ""
	if fs.NArg() == 1 {
		jobOwner = fs.Arg(0)
	}
	return cfg, jobOwner, nil
}

// readGranted resolves the request from scheduler state rather than trusting
// similarly named variables submitted with the job.
func readGranted(resourceName, qstatPath string) (string, error) {
	return readJobRequestedResource(resourceName, qstatPath)
}

func readJobRequestedResource(resourceName, qstatPath string) (string, error) {
	jobID := os.Getenv("JOB_ID")
	if jobID == "" {
		return "", errors.New("JOB_ID is unset")
	}
	out, err := runSchedulerCommand(qstatPath, "-j", jobID)
	if err != nil {
		return "", err
	}
	return parseJobRequestedResource(out, resourceName)
}

// runSchedulerCommand runs a root-owned OCS client command. The hook's
// environment has already been reset to the OCS-set variables, so the
// command does not read job-provided settings such as HOME/.sge_qstat.
func runSchedulerCommand(path string, args ...string) ([]byte, error) {
	trusted, err := checkTrustedExecutable(path)
	if err != nil {
		return nil, err
	}
	out, err := exec.Command(trusted, args...).Output()
	if err != nil {
		// qmaster may be briefly unreachable; let OCS reschedule the job.
		return nil, qrmiocs.RetryError(fmt.Errorf("run %s %s: %w", trusted, strings.Join(args, " "), err))
	}
	return out, nil
}

// parseJobRequestedResource reads only hard requests. OCS does not
// enforce soft requests, so a soft value could name a backend the host
// does not offer. master_hard_resource_list holds the master task's hard
// requests of a parallel job submitted with -scope master; the prolog
// runs for the master task.
func parseJobRequestedResource(out []byte, resourceName string) (string, error) {
	for _, field := range []string{"hard_resource_list:", "master_hard_resource_list:"} {
		if value, ok := resourceFromList(fieldValue(out, field), resourceName); ok {
			return value, nil
		}
	}
	return "", fmt.Errorf("%w: %s", errRequestedResourceNotFound, resourceName)
}

// fieldValue returns the value of the first line starting with field, joined
// with its continuation lines (indented, or after a trailing backslash).
// Fields without a trailing colon, as in qconf output, must be followed by
// whitespace.
func fieldValue(out []byte, field string) string {
	scanner := bufio.NewScanner(bytes.NewReader(out))
	var value strings.Builder
	collecting := false
	for scanner.Scan() {
		line := scanner.Text()
		if !collecting {
			rest, ok := strings.CutPrefix(line, field)
			if !ok || (!strings.HasSuffix(field, ":") && rest != "" && rest[0] != ' ' && rest[0] != '\t') {
				continue
			}
			collecting = true
			value.WriteString(strings.TrimSpace(rest))
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			break
		}
		value.WriteString(strings.TrimSpace(line))
	}
	return strings.ReplaceAll(value.String(), "\\", "")
}

func resourceFromList(list, resourceName string) (string, bool) {
	for _, item := range strings.Split(list, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(item), "=")
		if ok && strings.TrimSpace(key) == resourceName && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

// checkHostOffers confirms that this execution host's complex_values offer
// the requested backend, so a job can only use the backend the scheduler
// matched it to.
func checkHostOffers(cfg hookConfig, backend string) error {
	host, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("read host name: %w", err)
	}
	qconfPath := filepath.Join(filepath.Dir(cfg.QstatPath), "qconf")
	out, err := runSchedulerCommand(qconfPath, "-se", host)
	if err != nil && strings.Contains(host, ".") {
		host, _, _ = strings.Cut(host, ".")
		out, err = runSchedulerCommand(qconfPath, "-se", host)
	}
	if err != nil {
		return err
	}
	offered, ok := resourceFromList(fieldValue(out, "complex_values"), cfg.ResourceName)
	if !ok || offered != backend {
		return qrmiocs.JobError(fmt.Errorf("host %s does not offer %s=%s", host, cfg.ResourceName, backend))
	}
	return nil
}

func trustedExecutable(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("scheduler executable %s is not an absolute path", path)
	}
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return "", fmt.Errorf("scheduler executable %s is not root-owned and non-writable", path)
	}
	return path, nil
}

// exportBackendEnv applies the backend-prefixed environment variables
// declared in qrmi_config.json. The prefix is the backend name (for
// example "EMU_FREE"); a configured key "QRMI_PASQAL_CLOUD_AUTH_ENDPOINT"
// becomes "EMU_FREE_QRMI_PASQAL_CLOUD_AUTH_ENDPOINT" in the job env.
//
// The configured value always replaces a value the job may have set.
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

func exportSchedulerJobEnv(je *qrmiocs.JobEnv, cfg hookConfig, jobOwner string) error {
	uid, err := schedulerJobUID(jobOwner)
	if err != nil {
		return err
	}
	pairs := [][2]string{
		{"QRMI_JOB_UID", strconv.Itoa(uid)},
		{"QRMI_JOB_ID", schedulerJobID()},
	}
	if cfg.SlotsResourceName != "" {
		if slots, ok, err := readGrantedSlots(cfg.SlotsResourceName, cfg.QstatPath); err != nil {
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

func schedulerJobID() string {
	jobID := os.Getenv("JOB_ID")
	taskID := os.Getenv("SGE_TASK_ID")
	if taskID != "" && taskID != "undefined" && taskID != "0" {
		return jobID + "." + taskID
	}
	return jobID
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

func readGrantedSlots(resourceName, qstatPath string) (int, bool, error) {
	raw, err := readJobRequestedResource(resourceName, qstatPath)
	if errors.Is(err, errRequestedResourceNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
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
