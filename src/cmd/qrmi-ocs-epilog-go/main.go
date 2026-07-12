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
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"time"

	"github.com/hpc-gridware/qpu-resource/src/internal/qrmi"
	"github.com/hpc-gridware/qpu-resource/src/internal/qrmiocs"
)

const component = "qrmi-ocs-epilog"

var log = qrmiocs.NewLogger(component)

func main() {
	os.Exit(run())
}

// run returns the process exit code so main can call os.Exit without
// having to track success/failure through panics or defers.
//
// Exit semantics match the C version exactly: 0 on success, 1 on any
// failure, but a missing metadata file is treated as success since it
// means the prolog never recorded an acquisition for this job (for
// example because the job was rejected before it ran).
func run() int {
	qrmi.SetLogCallback(log.QRMILog)
	metaPath := qrmiocs.ResolveMetadataPath()
	rec, readErr := qrmiocs.ReadStrictSingle(metaPath)
	if readErr != nil && errors.Is(readErr, fs.ErrNotExist) {
		log.Info("no metadata file found at %s; skipping release", metaPath)
		return 0
	}

	start := time.Now()
	failed := 0
	if readErr != nil {
		log.Error("read metadata %s: %v", metaPath, readErr)
		failed = 1
	} else if err := releaseRecord(rec); err != nil {
		log.Error("%v", err)
		failed = 1
	}

	if err := os.Remove(metaPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Warn("failed to remove metadata file %s: %v", metaPath, err)
	}

	elapsed := int64(time.Since(start).Seconds())
	publishSummary(failed, elapsed)

	logSummary(failed, elapsed)
	return failed
}

// releaseRecord opens a QRMI handle for the recorded resource and
// releases its token. Errors are wrapped so the caller can log them
// directly.
func releaseRecord(rec qrmiocs.Record) error {
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
