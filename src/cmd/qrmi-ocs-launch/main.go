// Copyright 2026 Pasqal and HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

// qrmi-ocs-launch starts a QRMI queue hook with a clean environment.
//
// OCS runs queue prologs and epilogs as the configured user (the OCS admin
// account) but with the job's environment, including variables submitted
// with qsub -v or -V. The hooks are dynamically linked against libqrmi, so
// the dynamic loader would act on loader variables from that environment
// before the hook's own code runs. This launcher is built with
// CGO_ENABLED=0 and is statically linked, so no loader reads the
// environment. It refuses to run if loader variables are set, keeps only
// the variables OCS sets for the hook (validated, see qrmiocs.LaunchEnv),
// takes SGE_ROOT, SGE_CELL and the ports from its own options, changes to /
// and replaces itself with the hook via execve. --spool-root may be given
// once per execd spool directory (the global one and host-local overrides):
//
//	root@/opt/ocs/qrmi/hooks/qrmi-ocs-launch --sge-root=/opt/ocs --sge-cell=default \
//	    /opt/ocs/qrmi/hooks/qrmi-ocs-prolog --config=... $job_owner
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hpc-gridware/qpu-resource/src/internal/qrmiocs"
)

// checkTrustedHook is replaced in tests, which run hooks from temporary
// directories.
var checkTrustedHook = trustedHook

func main() {
	path, argv, env, err := prepare(os.Args, os.Environ())
	if err == nil {
		err = os.Chdir("/")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "qrmi-ocs-launch[ERROR]: %v\n", err)
		os.Exit(qrmiocs.ExitCode(err))
	}
	err = syscall.Exec(path, argv, env)
	fmt.Fprintf(os.Stderr, "qrmi-ocs-launch[ERROR]: exec %s: %v\n", path, err)
	os.Exit(qrmiocs.ExitHostError)
}

// prepare parses the launcher options that precede the hook path and
// returns the hook path, its argv and the clean environment.
func prepare(args, environ []string) (string, []string, []string, error) {
	var settings qrmiocs.LaunchSettings
	options := map[string]*string{
		"--sge-root=":     &settings.SGERoot,
		"--sge-cell=":     &settings.SGECell,
		"--qmaster-port=": &settings.QmasterPort,
		"--execd-port=":   &settings.ExecdPort,
	}
	i := 1
	for ; i < len(args) && strings.HasPrefix(args[i], "--"); i++ {
		name, value, _ := strings.Cut(args[i], "=")
		target, ok := options[name+"="]
		switch {
		case name == "--spool-root":
			settings.SpoolRoots = append(settings.SpoolRoots, value)
		case !ok:
			return "", nil, nil, fmt.Errorf("unknown option %s", name)
		default:
			*target = value
		}
	}
	if i >= len(args) {
		return "", nil, nil, fmt.Errorf("usage: %s --sge-root=DIR --sge-cell=CELL [--spool-root=DIR]... "+
			"[--qmaster-port=N] [--execd-port=N] <hook> [hook arguments]", filepath.Base(args[0]))
	}
	env, err := qrmiocs.LaunchEnv(environ, settings)
	if err != nil {
		return "", nil, nil, err
	}
	path, err := checkTrustedHook(args[i])
	if err != nil {
		return "", nil, nil, err
	}
	return path, append([]string{path}, args[i+1:]...), env, nil
}

// trustedHook requires an absolute path to a root-owned regular file that
// group and others cannot write.
func trustedHook(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("hook %s is not an absolute path", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return "", fmt.Errorf("hook %s is not a root-owned file that only root can write", resolved)
	}
	return resolved, nil
}
