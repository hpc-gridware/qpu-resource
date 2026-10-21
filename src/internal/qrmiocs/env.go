// Copyright 2026 Pasqal, HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

package qrmiocs

import (
	"bufio"
	"errors"
	"fmt"
	"os"
)

// JobEnv mirrors os.Setenv calls into the per-job environment file the
// scheduler reads when launching the job. The C version performs both
// writes in lock-step via set_runtime_env; this type captures the same
// invariant so callers can never forget the file half.
//
// JobEnv is not safe for concurrent use; the prolog and epilog are
// single-threaded.
type JobEnv struct {
	path string
	f    *os.File
	w    *bufio.Writer
}

// OpenJobEnv resolves the job env file path and opens it for append.
// The file is created if missing so a fresh job has a clean target.
func OpenJobEnv() (*JobEnv, error) {
	path, err := ResolveJobEnvPath()
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open job env %s: %w", path, err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("set job env permissions %s: %w", path, err)
	}
	return &JobEnv{path: path, f: f, w: bufio.NewWriter(f)}, nil
}

// Path returns the resolved job env file path. Useful for log lines.
func (e *JobEnv) Path() string {
	if e == nil {
		return ""
	}
	return e.path
}

// Set updates the current process env via os.Setenv and appends a KEY=VALUE
// line to the job env file. Both halves must succeed; partial state is
// reported as an error so the caller does not silently drift between the
// process env and the file.
func (e *JobEnv) Set(key, value string) error {
	if err := os.Setenv(key, value); err != nil {
		return fmt.Errorf("setenv %s: %w", key, err)
	}
	if e == nil || e.w == nil {
		return nil
	}
	if _, err := fmt.Fprintf(e.w, "%s=%s\n", key, value); err != nil {
		return fmt.Errorf("write %s to job env: %w", key, err)
	}
	if err := e.w.Flush(); err != nil {
		return fmt.Errorf("flush job env: %w", err)
	}
	return nil
}

// Close flushes any buffered writes and releases the file handle. It is
// safe to call on a nil receiver.
func (e *JobEnv) Close() error {
	if e == nil || e.f == nil {
		return nil
	}
	if e.w != nil {
		if err := e.w.Flush(); err != nil {
			_ = e.f.Close()
			e.f = nil
			return err
		}
	}
	err := e.f.Close()
	e.f = nil
	return err
}

// ApplyRustLog maps the hook's --log-level value to RUST_LOG for QRMI and
// the job. An empty or unknown level is a no-op, so QRMI applies its own
// default. The level comes from the queue's hook command line, not from
// the job environment.
func (e *JobEnv) ApplyRustLog(level string) error {
	rustLevel, ok := RustLogFromDebug(level)
	if !ok {
		return nil
	}
	return e.Set("RUST_LOG", rustLevel)
}

// PluginErrorKey is the env var name used to surface a prolog failure to
// the running job and the epilog. Its presence (combined with
// qrmi_prolog_status=error) signals an unrecoverable setup failure.
const PluginErrorKey = "QRMI_PLUGIN_ERROR"

// PrologStatusKey is the env var name used to record the prolog outcome.
// Set to "success" on a clean run and "error" on failure.
const PrologStatusKey = "qrmi_prolog_status"

// SetPluginError records an error message in the job env and marks the
// prolog status as failed. The C version performs both writes via
// set_plugin_error; the same invariant is captured here.
func (e *JobEnv) SetPluginError(message string) error {
	if message == "" {
		message = "unknown qrmi-ocs-prolog error"
	}
	var errs []error
	if err := e.Set(PluginErrorKey, message); err != nil {
		errs = append(errs, err)
	}
	if err := e.Set(PrologStatusKey, "error"); err != nil {
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}
