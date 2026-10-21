// Copyright 2026 Pasqal and HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hpc-gridware/qpu-resource/src/internal/qrmiocs"
)

// testCluster creates <root>/spool/execd/node/active_jobs/42.1 and returns
// the launcher options and the job's spool directory.
func testCluster(t *testing.T) (string, []string, string) {
	t.Helper()
	checkTrustedHook = func(path string) (string, error) { return path, nil }
	t.Cleanup(func() { checkTrustedHook = trustedHook })
	root := t.TempDir()
	spoolRoot := filepath.Join(root, "spool", "execd")
	spool := filepath.Join(spoolRoot, "node", "active_jobs", "42.1")
	if err := os.MkdirAll(spool, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, []string{"qrmi-ocs-launch", "--sge-root=" + root, "--sge-cell=default",
		"--spool-root=" + spoolRoot, "--qmaster-port=6444"}, spool
}

func run(args []string, hookArgs ...string) []string {
	return append(append(slices.Clone(args), "/opt/qrmi/qrmi-ocs-prolog"), hookArgs...)
}

func TestPrepareStartsHookWithCleanEnvironment(t *testing.T) {
	root, args, spool := testCluster(t)
	path, argv, env, err := prepare(run(args, "--config=/etc/qrmi.json", "alice"), []string{
		"JOB_ID=42", "SGE_TASK_ID=undefined", "SGE_JOB_SPOOL_DIR=" + spool, "TZ=Europe/Paris",
		"SGE_ROOT=/tmp/x", "SGE_CELL=evil", "SGE_QMASTER_PORT=1", "SGE_EXECD_PORT=2",
		"PATH=/tmp/evil", "HOME=/home/alice", "RUST_LOG=trace", "QRMI_WARDEN_URL=http://evil",
		"PASQAL_LOCAL_QRMI_WARDEN_URL=http://evil", "PASQAL_CONFIG_ROOT=/root", "HTTPS_PROXY=http://evil",
		"SSL_CERT_FILE=/tmp/ca", "OPENSSL_CONF=/tmp/x", "BASH_FUNC_x%%=() { :; }",
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/opt/qrmi/qrmi-ocs-prolog" || !slices.Equal(argv, []string{path, "--config=/etc/qrmi.json", "alice"}) {
		t.Fatalf("unexpected exec: %s %v", path, argv)
	}
	want := map[string]string{
		"JOB_ID": "42", "SGE_TASK_ID": "undefined", "SGE_JOB_SPOOL_DIR": spool, "TZ": "Europe/Paris",
		"SGE_ROOT": root, "SGE_CELL": "default", "SGE_QMASTER_PORT": "6444",
		"PATH": qrmiocs.TrustedPath, "LANG": "C", "LC_ALL": "C",
	}
	for _, kv := range env {
		key, value, _ := strings.Cut(kv, "=")
		if key == "HOME" {
			if value == "/home/alice" {
				t.Errorf("job owner's HOME kept")
			}
			continue
		}
		if expected, ok := want[key]; !ok || value != expected {
			t.Errorf("unexpected %s", kv)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Errorf("missing %v in %v", want, env)
	}
}

func TestPrepareDropsUnsafeTimeZone(t *testing.T) {
	_, args, spool := testCluster(t)
	for _, tz := range []string{"../../etc/passwd", "/etc/../x", "a b", strings.Repeat("A", 65)} {
		_, _, env, err := prepare(run(args), []string{"JOB_ID=42", "SGE_JOB_SPOOL_DIR=" + spool, "TZ=" + tz})
		if err != nil {
			t.Fatal(err)
		}
		if slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "TZ=") }) {
			t.Errorf("TZ=%q kept", tz)
		}
	}
}

func TestPrepareRefusesLoaderVariables(t *testing.T) {
	_, args, spool := testCluster(t)
	for _, kv := range []string{"LD_AUDIT=/tmp/x.so", "LD_PRELOAD=/tmp/x.so", "LD_LIBRARY_PATH=/tmp", "GLIBC_TUNABLES=glibc.x=1"} {
		_, _, _, err := prepare(run(args), []string{"JOB_ID=42", "SGE_JOB_SPOOL_DIR=" + spool, kv})
		if qrmiocs.ExitCode(err) != qrmiocs.ExitJobError || !strings.Contains(err.Error(), "refusing to run") {
			t.Errorf("%s: expected a job error, got %v", kv, err)
		}
	}
}

func TestPrepareRejectsTamperedSpoolDirectory(t *testing.T) {
	root, args, spool := testCluster(t)
	other := filepath.Join(root, "spool", "execd", "node", "active_jobs", "7.1")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "spool", "execd", "link")
	if err := os.Symlink(filepath.Join(root, "spool", "execd", "node"), link); err != nil {
		t.Fatal(err)
	}
	for name, env := range map[string][]string{
		"missing":       {"JOB_ID=42"},
		"other job":     {"JOB_ID=42", "SGE_JOB_SPOOL_DIR=" + other},
		"outside root":  {"JOB_ID=42", "SGE_JOB_SPOOL_DIR=/tmp/active_jobs/42.1"},
		"dot dot":       {"JOB_ID=42", "SGE_JOB_SPOOL_DIR=" + spool + "/../42.1"},
		"symlink":       {"JOB_ID=42", "SGE_JOB_SPOOL_DIR=" + filepath.Join(link, "active_jobs", "42.1")},
		"no directory":  {"JOB_ID=43", "SGE_JOB_SPOOL_DIR=" + filepath.Join(filepath.Dir(spool), "43.1")},
		"bad job id":    {"JOB_ID=42;x", "SGE_JOB_SPOOL_DIR=" + spool},
		"bad task id":   {"JOB_ID=42", "SGE_TASK_ID=1;x", "SGE_JOB_SPOOL_DIR=" + spool},
		"wrong task id": {"JOB_ID=42", "SGE_TASK_ID=2", "SGE_JOB_SPOOL_DIR=" + spool},
	} {
		if _, _, _, err := prepare(run(args), env); qrmiocs.ExitCode(err) != qrmiocs.ExitJobError {
			t.Errorf("%s: expected a job error, got %v", name, err)
		}
	}
}

func TestPrepareRequiresClusterOptions(t *testing.T) {
	_, args, spool := testCluster(t)
	env := []string{"JOB_ID=42", "SGE_JOB_SPOOL_DIR=" + spool}
	for name, bad := range map[string][]string{
		"no root":        {"qrmi-ocs-launch", "--sge-cell=default"},
		"relative root":  {"qrmi-ocs-launch", "--sge-root=opt/ocs", "--sge-cell=default"},
		"bad cell":       {"qrmi-ocs-launch", "--sge-root=/opt/ocs", "--sge-cell=../x"},
		"bad port":       append(slices.Clone(args), "--execd-port=x"),
		"unknown option": append(slices.Clone(args), "--sge-rot=/x"),
	} {
		_, _, _, err := prepare(run(bad), env)
		if err == nil || qrmiocs.ExitCode(err) != qrmiocs.ExitHostError {
			t.Errorf("%s: expected a host error, got %v", name, err)
		}
	}
	if _, _, _, err := prepare(args, env); err == nil {
		t.Error("expected a missing hook path to be rejected")
	}
}

func TestTrustedHookRequiresAbsoluteRootOwnedPath(t *testing.T) {
	if _, err := trustedHook("qrmi-ocs-prolog"); err == nil {
		t.Fatal("expected a relative hook path to be rejected")
	}
	if _, err := trustedHook(t.TempDir()); err == nil {
		t.Fatal("expected a directory to be rejected")
	}
}
