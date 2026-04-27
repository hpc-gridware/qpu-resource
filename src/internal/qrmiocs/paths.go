// Copyright 2026 Pasqal, HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

package qrmiocs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// MetadataFilename is the basename used by ResolveMetadataPath when no
// override is supplied. It must match the value used in the C prolog/epilog
// so legacy and new binaries can interoperate during migration.
const MetadataFilename = "qrmi_ocs_acquired.tsv"

// ResolveJobEnvPath returns the path of the per-job environment file the
// scheduler reads to populate the job env. It honors SGE_JOB_ENV first, then
// falls back to ${SGE_JOB_SPOOL_DIR}/environment, matching the C version.
func ResolveJobEnvPath() (string, error) {
	if v := os.Getenv("SGE_JOB_ENV"); v != "" {
		return v, nil
	}
	spool := os.Getenv("SGE_JOB_SPOOL_DIR")
	if spool == "" {
		return "", errors.New("SGE_JOB_ENV and SGE_JOB_SPOOL_DIR are both unset")
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

// ResolveMetadataPath returns the path used by the prolog to write and the
// epilog to read the acquisition metadata TSV. Resolution order matches the
// C version exactly:
//
//  1. QRMI_OCS_METADATA_PATH (explicit override)
//  2. ${SGE_JOB_SPOOL_DIR}/qrmi_ocs_acquired.tsv
//  3. /tmp/qrmi_ocs_<JOB_ID>.tsv when JOB_ID is set
//  4. /tmp/qrmi_ocs_acquired.tsv (last-resort fallback)
func ResolveMetadataPath() string {
	if v := os.Getenv("QRMI_OCS_METADATA_PATH"); v != "" {
		return v
	}
	if spool := os.Getenv("SGE_JOB_SPOOL_DIR"); spool != "" {
		return filepath.Join(spool, MetadataFilename)
	}
	if jobID := os.Getenv("JOB_ID"); jobID != "" {
		return fmt.Sprintf("/tmp/qrmi_ocs_%s.tsv", jobID)
	}
	return "/tmp/" + MetadataFilename
}
