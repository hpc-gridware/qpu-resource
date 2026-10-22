// Copyright 2026 Pasqal and HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

// qrmi-ocs-epilog-go is the OCS queue epilog hook. It reads the
// acquisition metadata written by the prolog, releases the QRMI
// acquisition token, removes the metadata file, and appends accounting
// metrics into the scheduler's per-job usage file so they are captured
// by qacct via the usage_patterns=qrmi:qrmi_* reporting param.
//
// This is the Go port of src/cmd/qrmi-ocs-epilog/main.c. The external
// contract (env var names, accounting fields, exit codes, log line
// prefixes) is identical so the two binaries are interchangeable during
// migration.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"strconv"
	"time"

	"github.com/hpc-gridware/qpu-resource/src/internal/qrmi"
	"github.com/hpc-gridware/qpu-resource/src/internal/qrmiocs"
)

const (
	component         = "qrmi-ocs-epilog"
	defaultConfigPath = "/etc/qrmi/qrmi_config.json"
)

var log = qrmiocs.NewLogger(component)

type hookConfig struct {
	ConfigPath string
	LogLevel   string
	JobOwner   string
}

func main() {
	// qrmi-ocs-launch starts the hook with a clean environment; this is a
	// second check in case the hook was started directly.
	if err := qrmiocs.ResetProcessEnv(); err != nil {
		log.Error("environment: %v", err)
		os.Exit(qrmiocs.ExitCode(err))
	}
	cfg, err := parseHookArgs(os.Args[1:])
	if err != nil {
		log.Error("%v", err)
		os.Exit(qrmiocs.ExitHostError)
	}
	os.Exit(run(cfg))
}

func parseHookArgs(args []string) (hookConfig, error) {
	cfg := hookConfig{ConfigPath: defaultConfigPath}
	fs := flag.NewFlagSet(component, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.ConfigPath, "config", cfg.ConfigPath, "")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "")
	if err := fs.Parse(args); err != nil {
		return hookConfig{}, err
	}
	if fs.NArg() > 1 {
		return hookConfig{}, errors.New("expected at most one job-owner argument")
	}
	cfg.JobOwner = fs.Arg(0)
	return cfg, nil
}

// run returns the process exit code so main can call os.Exit without
// having to track success/failure through panics or defers.
//
// A missing metadata file is success: the prolog recorded no acquisition
// for this job (for example because it requested no QPU). A failed release
// is logged, recorded in accounting and moves the metadata file out of the
// job spool, which OCS removes, for administrator recovery (see
// qrmiocs.PreserveFailedRelease), but still exits 0: the job has finished,
// and a non-zero code would put the queue instance into error state. Only
// unreadable metadata or a missing spool directory exit 1.
//
// Exit semantics match the C version.
func run(cfg hookConfig) int {
	if level, ok := qrmiocs.RustLogFromDebug(cfg.LogLevel); ok {
		_ = os.Setenv("RUST_LOG", level)
	}
	qrmi.SetLogCallback(log.QRMILog)
	metaPath, err := qrmiocs.ResolveMetadataPath()
	if err != nil {
		log.Error("resolve metadata path: %v", err)
		return 1
	}
	rec, readErr := qrmiocs.ReadStrictSingle(metaPath)
	if readErr != nil && errors.Is(readErr, fs.ErrNotExist) {
		log.Info("no metadata file found at %s; skipping release", metaPath)
		return 0
	}

	start := time.Now()
	failed := 0
	exitCode := 0
	if readErr != nil {
		log.Error("read metadata %s: %v", metaPath, readErr)
		failed = 1
		exitCode = qrmiocs.ExitHostError
	} else if err := releaseRecord(cfg, rec); err != nil {
		log.Error("%v", err)
		failed = 1
	}

	if failed == 0 {
		if err := os.Remove(metaPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Warn("failed to remove metadata file %s: %v", metaPath, err)
		}
	} else if kept, err := qrmiocs.PreserveFailedRelease(metaPath); err != nil {
		log.Warn("failed to move metadata file %s out of the job spool for administrator recovery: %v", metaPath, err)
	} else {
		log.Warn("moved metadata for administrator recovery to %s", kept)
	}

	elapsed := int64(time.Since(start).Seconds())
	publishSummary(failed, elapsed)

	logSummary(failed, elapsed)
	return exitCode
}

// releaseRecord opens a QRMI handle for the recorded resource and
// releases its token. QRMI reads the backend settings from the process
// environment, which was reset in main; they are rebuilt here from
// qrmi_config.json and the metadata record, as the prolog set them.
// Errors are wrapped so the caller can log them directly.
func releaseRecord(cfg hookConfig, rec qrmiocs.Record) error {
	if err := setReleaseEnv(cfg, rec); err != nil {
		return err
	}
	r, err := qrmi.NewResource(rec.Name, rec.Type)
	if err != nil {
		return fmt.Errorf("qrmi_resource_new(%s): %w", rec.Name, err)
	}
	defer r.Close()

	if err := r.Release(rec.Token); err != nil {
		return fmt.Errorf("qrmi_resource_release(%s): %w", rec.Name, err)
	}
	return nil
}

func setReleaseEnv(cfg hookConfig, rec qrmiocs.Record) error {
	qcfg, err := qrmi.LoadConfig(cfg.ConfigPath)
	if err != nil {
		return fmt.Errorf("load qrmi config %s: %w", cfg.ConfigPath, err)
	}
	defer qcfg.Close()
	def, err := qcfg.ResourceDef(rec.Name)
	if err != nil {
		return fmt.Errorf("resource %q not in %s: %w", rec.Name, cfg.ConfigPath, err)
	}
	defer def.Close()

	uid := os.Getuid()
	if cfg.JobOwner != "" {
		account, err := user.Lookup(cfg.JobOwner)
		if err != nil {
			return fmt.Errorf("look up scheduler job owner %q: %w", cfg.JobOwner, err)
		}
		if uid, err = strconv.Atoi(account.Uid); err != nil {
			return fmt.Errorf("invalid uid %q for scheduler job owner %q", account.Uid, cfg.JobOwner)
		}
	}
	jobID := os.Getenv("JOB_ID")
	if task := os.Getenv("SGE_TASK_ID"); task != "" && task != "undefined" && task != "0" {
		jobID += "." + task
	}
	pairs := [][2]string{
		{"QRMI_JOB_UID", strconv.Itoa(uid)},
		{"QRMI_JOB_ID", jobID},
		{rec.Name + "_QRMI_JOB_ACQUISITION_TOKEN", rec.Token},
	}
	for _, e := range def.Environments() {
		pairs = append(pairs, [2]string{rec.Name + "_" + e.Key, e.Value})
	}
	for _, kv := range pairs {
		if err := os.Setenv(kv[0], kv[1]); err != nil {
			return fmt.Errorf("setenv %s: %w", kv[0], err)
		}
	}
	return nil
}

// publishSummary writes accounting metrics into the job env (so a still
// running job script can observe them) and into the per-job usage file
// (so the scheduler captures them under usage_patterns=qrmi:qrmi_*).
//
// The job env writes are best-effort: the C version performs them and
// the README documents them, but in practice the epilog runs after the
// job has exited so only accounting consumers see the values. We log
// failures but never fail the epilog because of them.
func publishSummary(failed int, elapsed int64) {
	releaseSuccess := 1 - failed
	statusCode := 1 // matches C: 1 = success, 0 = error
	statusName := "success"
	if failed != 0 {
		statusCode = 0
		statusName = "error"
	}

	metrics := [][2]string{
		{"qrmi_release_total", "1"},
		{"qrmi_release_success", strconv.Itoa(releaseSuccess)},
		{"qrmi_release_failed", strconv.Itoa(failed)},
		{"qrmi_release_elapsed_seconds", strconv.FormatInt(elapsed, 10)},
		{"qrmi_epilog_status", statusName},
	}

	// Best-effort job env mirror.
	if je, err := qrmiocs.OpenJobEnv(); err == nil {
		defer je.Close()
		for _, kv := range metrics {
			if err := je.Set(kv[0], kv[1]); err != nil {
				log.Warn("write %s to job env: %v", kv[0], err)
			}
		}
	} else {
		log.Warn("open job env: %v", err)
	}

	// Required usage file accounting.
	usagePath, err := qrmiocs.ResolveUsagePath()
	if err != nil {
		log.Warn("resolve usage path: %v", err)
		return
	}
	usageMetrics := [][2]string{
		{"qrmi_acquired_count", "1"},
		{"qrmi_release_total", "1"},
		{"qrmi_release_success", strconv.Itoa(releaseSuccess)},
		{"qrmi_release_failed", strconv.Itoa(failed)},
		{"qrmi_release_elapsed_seconds", strconv.FormatInt(elapsed, 10)},
		{"qrmi_epilog_status_code", strconv.Itoa(statusCode)},
	}
	if err := appendUsage(usagePath, usageMetrics); err != nil {
		log.Warn("append usage %s: %v", usagePath, err)
	}
}

// appendUsage writes KEY=VALUE lines to path, creating the file if
// missing. The scheduler reads this file to publish qrmi_* metrics into
// qacct.
func appendUsage(path string, metrics [][2]string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, kv := range metrics {
		if _, err := fmt.Fprintf(f, "%s=%s\n", kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

func logSummary(failed int, elapsed int64) {
	log.Info("release summary: total=1 success=%d failed=%d elapsed=%ds",
		1-failed, failed, elapsed)
}
