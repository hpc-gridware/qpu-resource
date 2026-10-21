// Copyright 2026 Pasqal, HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

package qrmiocs

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// OCS runs queue prologs and epilogs with the job's environment, including
// variables submitted with qsub -v or -V. The hooks run as the OCS admin
// user, so they must not take anything from that environment except the
// variables OCS sets for them, and those are validated.
var trustedEnv = map[string]*regexp.Regexp{
	"JOB_ID":            regexp.MustCompile(`^[0-9]+$`),
	"SGE_TASK_ID":       regexp.MustCompile(`^([0-9]+|undefined)$`),
	"SGE_JOB_SPOOL_DIR": regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`),
	"SGE_ROOT":          regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`),
	"SGE_CELL":          regexp.MustCompile(`^[A-Za-z0-9._-]+$`),
	"SGE_QMASTER_PORT":  regexp.MustCompile(`^[0-9]+$`),
	"SGE_EXECD_PORT":    regexp.MustCompile(`^[0-9]+$`),
	"TZ":                tzPattern,
}

// tzPattern matches a time zone name such as Europe/Paris or UTC+1.
var tzPattern = regexp.MustCompile(`^[A-Za-z0-9_+:/.-]{1,64}$`)

var (
	digits      = regexp.MustCompile(`^[0-9]+$`)
	cellPattern = trustedEnv["SGE_CELL"]
)

// LaunchSettings are the values qrmi-ocs-launch takes from its own command
// line, which setup-qrmi-support writes from the cluster configuration. The
// job cannot set them: values of the same names in the hook environment
// are ignored.
type LaunchSettings struct {
	SGERoot     string // $SGE_ROOT
	SGECell     string // $SGE_CELL
	SpoolRoot   string // execd spool directory; SGERoot when empty
	QmasterPort string // optional, when /etc/services has no sge_qmaster entry
	ExecdPort   string // optional, when /etc/services has no sge_execd entry
}

// LaunchEnv builds the hook environment for qrmi-ocs-launch. It fails the
// job if loader variables are set, takes JOB_ID, SGE_TASK_ID and
// SGE_JOB_SPOOL_DIR from environ after validating them, keeps TZ only when
// it is a plain time zone name, and sets everything else to fixed values:
// SGE_ROOT, SGE_CELL and the ports from s, a fixed PATH, the hook user's
// HOME and the C locale. QRMI_*, <BACKEND>_QRMI_*, proxy and TLS variables,
// RUST_LOG and the job owner's HOME are dropped; the hooks set RUST_LOG
// from their --log-level argument.
func LaunchEnv(environ []string, s LaunchSettings) ([]string, error) {
	if err := CheckLoaderEnv(environ); err != nil {
		return nil, err
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, kv := range environ {
		key, value, ok := strings.Cut(kv, "=")
		if ok {
			values[key] = value
		}
	}
	jobID, taskID := values["JOB_ID"], values["SGE_TASK_ID"]
	if !digits.MatchString(jobID) {
		return nil, JobError(errors.New("invalid JOB_ID"))
	}
	if taskID != "" && !trustedEnv["SGE_TASK_ID"].MatchString(taskID) {
		return nil, JobError(errors.New("invalid SGE_TASK_ID"))
	}
	spool := values["SGE_JOB_SPOOL_DIR"]
	spoolRoot := s.SpoolRoot
	if spoolRoot == "" {
		spoolRoot = s.SGERoot
	}
	if err := CheckSpoolDir(spool, spoolRoot, jobID, taskID); err != nil {
		return nil, JobError(err)
	}
	out := []string{"JOB_ID=" + jobID, "SGE_JOB_SPOOL_DIR=" + spool, "SGE_ROOT=" + s.SGERoot, "SGE_CELL=" + s.SGECell}
	if taskID != "" {
		out = append(out, "SGE_TASK_ID="+taskID)
	}
	if s.QmasterPort != "" {
		out = append(out, "SGE_QMASTER_PORT="+s.QmasterPort)
	}
	if s.ExecdPort != "" {
		out = append(out, "SGE_EXECD_PORT="+s.ExecdPort)
	}
	if tz := values["TZ"]; tzPattern.MatchString(tz) && !strings.Contains(tz, "..") {
		out = append(out, "TZ="+tz)
	}
	return append(out, "PATH="+TrustedPath, "HOME="+hookUserHome(), "LANG=C", "LC_ALL=C"), nil
}

func (s LaunchSettings) validate() error {
	for _, dir := range []string{s.SGERoot, s.SpoolRoot} {
		if dir != "" && (!filepath.IsAbs(dir) || filepath.Clean(dir) != dir) {
			return fmt.Errorf("invalid directory %q", dir)
		}
	}
	if s.SGERoot == "" || !cellPattern.MatchString(s.SGECell) {
		return errors.New("--sge-root and --sge-cell are required")
	}
	for _, port := range []string{s.QmasterPort, s.ExecdPort} {
		if port != "" && !digits.MatchString(port) {
			return fmt.Errorf("invalid port %q", port)
		}
	}
	return nil
}

// CheckSpoolDir requires spool to be the job's own spool directory,
// <root>/.../active_jobs/<job>.<task>: an existing directory below root,
// in clean form, with no symbolic links below root.
func CheckSpoolDir(spool, root, jobID, taskID string) error {
	if !filepath.IsAbs(spool) || filepath.Clean(spool) != spool || strings.Contains(spool, "..") ||
		!strings.HasPrefix(spool, root+"/") {
		return fmt.Errorf("invalid SGE_JOB_SPOOL_DIR %q", spool)
	}
	task := taskID
	if task == "" || task == "undefined" {
		task = "1"
	}
	if filepath.Base(spool) != jobID+"."+task || filepath.Base(filepath.Dir(spool)) != "active_jobs" {
		return fmt.Errorf("SGE_JOB_SPOOL_DIR %q is not the spool directory of job %s.%s", spool, jobID, task)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve spool root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(spool)
	if err != nil {
		return fmt.Errorf("resolve SGE_JOB_SPOOL_DIR: %w", err)
	}
	if resolved != resolvedRoot+strings.TrimPrefix(spool, root) {
		return fmt.Errorf("SGE_JOB_SPOOL_DIR %q contains a symbolic link", spool)
	}
	info, err := os.Lstat(spool)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("SGE_JOB_SPOOL_DIR %q is not a directory", spool)
	}
	return nil
}

// TrustedPath is the PATH the hooks run with.
const TrustedPath = "/usr/sbin:/usr/bin:/sbin:/bin"

// CleanEnv builds the hook environment from environ: the trusted OCS
// variables, a fixed PATH, the hook user's own HOME and the C locale.
// Everything else is dropped, including QRMI_* and <BACKEND>_QRMI_*
// settings, proxy and TLS trust variables, RUST_LOG, loader variables and
// the job owner's HOME. A trusted variable with an unexpected value is an
// error, because OCS sets these itself.
func CleanEnv(environ []string) ([]string, error) {
	values := map[string]string{}
	for _, kv := range environ {
		key, value, ok := strings.Cut(kv, "=")
		pattern, trusted := trustedEnv[key]
		if !ok || !trusted {
			continue
		}
		if !pattern.MatchString(value) || strings.Contains(value, "..") {
			return nil, fmt.Errorf("unexpected value for %s", key)
		}
		values[key] = value
	}
	out := make([]string, 0, len(values)+4)
	for key, value := range values {
		out = append(out, key+"="+value)
	}
	return append(out,
		"PATH="+TrustedPath,
		"HOME="+hookUserHome(),
		"LANG=C",
		"LC_ALL=C",
	), nil
}

// hookUserHome returns the home directory of the account the hook runs as,
// or "/" if it has none.
func hookUserHome() string {
	account, err := user.LookupId(strconv.Itoa(os.Geteuid()))
	if err != nil || !filepath.IsAbs(account.HomeDir) {
		return "/"
	}
	return account.HomeDir
}

// CheckLoaderEnv fails if dynamic-loader variables are still set. The
// hooks are dynamically linked, so by the time their code runs the loader
// has already read them; qrmi-ocs-launch removes them before starting a
// hook. Finding one means the hook was started without the launcher.
func CheckLoaderEnv(environ []string) error {
	for _, kv := range environ {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "LD_") || key == "GLIBC_TUNABLES" {
			return JobError(fmt.Errorf("refusing to run: %s is set", key))
		}
	}
	return nil
}

// ResetProcessEnv checks for loader variables and replaces the process
// environment with CleanEnv's result. It is a second line of defense for a
// hook started without the launcher.
func ResetProcessEnv() error {
	if err := CheckLoaderEnv(os.Environ()); err != nil {
		return err
	}
	env, err := CleanEnv(os.Environ())
	if err != nil {
		return JobError(err)
	}
	os.Clearenv()
	for _, kv := range env {
		key, value, _ := strings.Cut(kv, "=")
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("setenv %s: %w", key, err)
		}
	}
	return nil
}

// Exit codes OCS interprets for queue prologs and epilogs: 99 reschedules
// the job, 100 puts the job into error state, and any other non-zero code
// puts the queue instance into error state.
const (
	ExitOK         = 0
	ExitHostError  = 1
	ExitReschedule = 99
	ExitJobError   = 100
)

// HookError carries the exit code a hook failure maps to.
type HookError struct {
	Code int
	Err  error
}

func (e *HookError) Error() string { return e.Err.Error() }
func (e *HookError) Unwrap() error { return e.Err }

// JobError marks a problem caused by the job's request.
func JobError(err error) error { return &HookError{Code: ExitJobError, Err: err} }

// RetryError marks a temporary problem: OCS reschedules the job.
func RetryError(err error) error { return &HookError{Code: ExitReschedule, Err: err} }

// ExitCode returns the exit code for err: 0 for nil, the HookError code if
// err wraps one, and ExitHostError otherwise.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var he *HookError
	if errors.As(err, &he) {
		return he.Code
	}
	return ExitHostError
}
