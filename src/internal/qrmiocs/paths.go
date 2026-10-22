// Copyright 2026 Pasqal, HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

package qrmiocs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// MetadataFilename is the basename used by ResolveMetadataPath. It must match
// the value used in the C prolog/epilog so legacy and new binaries can
// interoperate during migration.
const MetadataFilename = "qrmi_ocs_acquired.tsv"

// ResolveJobEnvPath returns the scheduler-owned per-job environment path.
func ResolveJobEnvPath() (string, error) {
	spool := os.Getenv("SGE_JOB_SPOOL_DIR")
	if spool == "" {
		return "", errors.New("SGE_JOB_SPOOL_DIR is unset")
	}
	return filepath.Join(spool, "environment"), nil
}

// ResolveUsagePath returns ${SGE_JOB_SPOOL_DIR}/usage. Returns an error when
// SGE_JOB_SPOOL_DIR is unset since the epilog has nowhere to publish
// accounting metrics in that case.
func ResolveUsagePath() (string, error) {
	spool := os.Getenv("SGE_JOB_SPOOL_DIR")
	if spool == "" {
		return "", errors.New("SGE_JOB_SPOOL_DIR is unset")
	}
	return filepath.Join(spool, "usage"), nil
}

// ResolveMetadataPath returns the scheduler-owned acquisition metadata path.
func ResolveMetadataPath() (string, error) {
	spool := os.Getenv("SGE_JOB_SPOOL_DIR")
	if spool == "" {
		return "", errors.New("SGE_JOB_SPOOL_DIR is unset")
	}
	return filepath.Join(spool, MetadataFilename), nil
}

// FailedReleaseDir is the directory in the execd's host spool directory
// where the epilog keeps the metadata of a failed release. OCS removes the
// job's spool directory after the job; the host spool directory stays.
const FailedReleaseDir = "qrmi_failed_releases"

// PreserveFailedRelease moves the metadata file at path from
// <host spool>/active_jobs/<job>.<task> to
// <host spool>/qrmi_failed_releases/<job>.<task>.tsv and returns the new
// path. An existing record of the same name is not replaced.
func PreserveFailedRelease(path string) (string, error) {
	jobDir := filepath.Dir(path)
	dir := filepath.Join(filepath.Dir(filepath.Dir(jobDir)), FailedReleaseDir)
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", err
	}
	kept := filepath.Join(dir, filepath.Base(jobDir)+".tsv")
	if err := os.Link(path, kept); err != nil {
		return "", err
	}
	return kept, os.Remove(path)
}
