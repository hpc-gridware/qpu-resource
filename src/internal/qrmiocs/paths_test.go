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
