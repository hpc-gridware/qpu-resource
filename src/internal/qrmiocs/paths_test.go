// Copyright 2026 Pasqal, HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

package qrmiocs_test

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/hpc-gridware/qpu-resource/src/internal/qrmiocs"
)

var _ = Describe("ResolveJobEnvPath", func() {
	BeforeEach(func() {
		GinkgoT().Setenv("SGE_JOB_ENV", "")
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", "")
	})

	It("uses SGE_JOB_ENV when set", func() {
		GinkgoT().Setenv("SGE_JOB_ENV", "/var/spool/job/env")
		got, err := qrmiocs.ResolveJobEnvPath()
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal("/var/spool/job/env"))
	})

	It("falls back to SGE_JOB_SPOOL_DIR/environment", func() {
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", "/var/spool/job")
		got, err := qrmiocs.ResolveJobEnvPath()
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(filepath.Join("/var/spool/job", "environment")))
	})

	It("errors when neither var is set", func() {
		_, err := qrmiocs.ResolveJobEnvPath()
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("ResolveMetadataPath", func() {
	BeforeEach(func() {
		GinkgoT().Setenv("QRMI_OCS_METADATA_PATH", "")
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", "")
		GinkgoT().Setenv("JOB_ID", "")
	})

	It("uses the explicit override first", func() {
		GinkgoT().Setenv("QRMI_OCS_METADATA_PATH", "/tmp/custom.tsv")
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", "/var/spool/job")
		Expect(qrmiocs.ResolveMetadataPath()).To(Equal("/tmp/custom.tsv"))
	})

	It("uses spool dir when override is unset", func() {
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", "/var/spool/job")
		Expect(qrmiocs.ResolveMetadataPath()).To(Equal(filepath.Join("/var/spool/job", qrmiocs.MetadataFilename)))
	})

	It("falls back to /tmp with JOB_ID when spool dir is unset", func() {
		GinkgoT().Setenv("JOB_ID", "12345")
		Expect(qrmiocs.ResolveMetadataPath()).To(Equal("/tmp/qrmi_ocs_12345.tsv"))
	})

	It("falls back to /tmp/qrmi_ocs_acquired.tsv as last resort", func() {
		Expect(qrmiocs.ResolveMetadataPath()).To(Equal("/tmp/" + qrmiocs.MetadataFilename))
	})
})

var _ = Describe("ResolveUsagePath", func() {
	BeforeEach(func() {
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", "")
	})

	It("returns spool/usage when set", func() {
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", "/var/spool/job")
		got, err := qrmiocs.ResolveUsagePath()
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(filepath.Join("/var/spool/job", "usage")))
	})

	It("errors when SGE_JOB_SPOOL_DIR is unset", func() {
		_, err := qrmiocs.ResolveUsagePath()
		Expect(err).To(HaveOccurred())
	})
})
