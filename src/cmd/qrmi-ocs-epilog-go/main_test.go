// Copyright 2026 Pasqal and HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestEpilog(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "qrmi-ocs-epilog-go Suite")
}

var _ = Describe("epilog run()", func() {
	var (
		spool  string
		meta   string
		jobEnv string
		usage  string
	)

	BeforeEach(func() {
		spool = GinkgoT().TempDir()
		meta = filepath.Join(spool, "qrmi_ocs_acquired.tsv")
		jobEnv = filepath.Join(spool, "environment")
		usage = filepath.Join(spool, "usage")

		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", spool)
		GinkgoT().Setenv("SGE_JOB_ENV", jobEnv)
		GinkgoT().Setenv("QRMI_OCS_METADATA_PATH", "")
	})

	It("returns 0 when no metadata file exists", func() {
		Expect(run()).To(Equal(0))
	})

	It("returns 1 and records error when metadata is malformed", func() {
		Expect(os.WriteFile(meta, []byte("badline\n"), 0o644)).To(Succeed())
		Expect(run()).To(Equal(1))

		// usage file should record qrmi_epilog_status_code=0 (error per inverted convention)
		data, err := os.ReadFile(usage)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("qrmi_epilog_status_code=0\n"))
		Expect(string(data)).To(ContainSubstring("qrmi_release_failed=1\n"))
	})

	It("returns 1 when metadata has multiple records", func() {
		Expect(os.WriteFile(meta, []byte("a\t1\tt1\t1\nb\t1\tt2\t2\n"), 0o644)).To(Succeed())
		Expect(run()).To(Equal(1))

		data, err := os.ReadFile(usage)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("qrmi_epilog_status_code=0\n"))
	})

	It("removes the metadata file after a parse failure", func() {
		Expect(os.WriteFile(meta, []byte("badline\n"), 0o644)).To(Succeed())
		Expect(run()).To(Equal(1))

		_, err := os.Stat(meta)
		Expect(os.IsNotExist(err)).To(BeTrue())
	})
})
