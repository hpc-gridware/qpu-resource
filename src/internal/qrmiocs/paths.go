// Copyright 2026 Pasqal, HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

package qrmiocs

import (
	"errors"
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
