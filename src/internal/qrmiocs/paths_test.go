// Copyright 2026 Pasqal, HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

package qrmiocs_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/hpc-gridware/qpu-resource/src/internal/qrmiocs"
)

var _ = Describe("ResolveJobEnvPath", func() {
	BeforeEach(func() {
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", "")
	})

	It("uses SGE_JOB_SPOOL_DIR/environment", func() {
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", "/var/spool/job")
		got, err := qrmiocs.ResolveJobEnvPath()
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(filepath.Join("/var/spool/job", "environment")))
	})

	It("errors when the spool directory is unset", func() {
		_, err := qrmiocs.ResolveJobEnvPath()
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("ResolveMetadataPath", func() {
	BeforeEach(func() {
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", "")
	})

	It("uses the scheduler spool directory", func() {
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", "/var/spool/job")
		path, err := qrmiocs.ResolveMetadataPath()
		Expect(err).ToNot(HaveOccurred())
		Expect(path).To(Equal(filepath.Join("/var/spool/job", qrmiocs.MetadataFilename)))
	})

	It("errors when the scheduler spool directory is unset", func() {
		_, err := qrmiocs.ResolveMetadataPath()
		Expect(err).To(HaveOccurred())
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

var _ = Describe("PreserveFailedRelease", func() {
	It("moves the metadata to the host spool directory without replacing a record", func() {
		host := GinkgoT().TempDir()
		spool := filepath.Join(host, "active_jobs", "42.1")
		Expect(os.MkdirAll(spool, 0o755)).To(Succeed())
		meta := filepath.Join(spool, qrmiocs.MetadataFilename)
		Expect(os.WriteFile(meta, []byte("first\n"), 0o600)).To(Succeed())

		kept, err := qrmiocs.PreserveFailedRelease(meta)
		Expect(err).ToNot(HaveOccurred())
		Expect(kept).To(Equal(filepath.Join(host, qrmiocs.FailedReleaseDir, "42.1.tsv")))
		Expect(meta).ToNot(BeAnExistingFile())

		Expect(os.WriteFile(meta, []byte("second\n"), 0o600)).To(Succeed())
		_, err = qrmiocs.PreserveFailedRelease(meta)
		Expect(err).To(HaveOccurred())
		Expect(os.ReadFile(kept)).To(Equal([]byte("first\n")))
		Expect(meta).To(BeAnExistingFile())
	})
})
