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
		spool string
		meta  string
		usage string
	)

	BeforeEach(func() {
		spool = GinkgoT().TempDir()
		meta = filepath.Join(spool, "qrmi_ocs_acquired.tsv")
		usage = filepath.Join(spool, "usage")

		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", spool)
	})

	cfg := hookConfig{ConfigPath: "/nonexistent/qrmi_config.json"}

	It("returns 0 when no metadata file exists", func() {
		Expect(run(cfg)).To(Equal(0))
	})

	It("returns 1 and records error when metadata is malformed", func() {
		Expect(os.WriteFile(meta, []byte("badline\n"), 0o644)).To(Succeed())
		Expect(run(cfg)).To(Equal(1))

		// usage file should record qrmi_epilog_status_code=0 (error per inverted convention)
		data, err := os.ReadFile(usage)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("qrmi_epilog_status_code=0\n"))
		Expect(string(data)).To(ContainSubstring("qrmi_release_failed=1\n"))
	})

	It("returns 1 when metadata has multiple records", func() {
		Expect(os.WriteFile(meta, []byte("a\t1\tt1\t1\nb\t1\tt2\t2\n"), 0o644)).To(Succeed())
		Expect(run(cfg)).To(Equal(1))

		data, err := os.ReadFile(usage)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("qrmi_epilog_status_code=0\n"))
	})

	It("retains the metadata file after a parse failure", func() {
		Expect(os.WriteFile(meta, []byte("badline\n"), 0o644)).To(Succeed())
		Expect(run(cfg)).To(Equal(1))

		_, err := os.Stat(meta)
		Expect(err).ToNot(HaveOccurred())
	})

	It("exits 0 but retains the metadata file after a release failure", func() {
		Expect(os.WriteFile(meta, []byte("PASQAL_LOCAL\t4\tsession-id\t1\n"), 0o600)).To(Succeed())
		Expect(run(cfg)).To(Equal(0))

		_, err := os.Stat(meta)
		Expect(err).ToNot(HaveOccurred())
		data, err := os.ReadFile(usage)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("qrmi_release_failed=1\n"))
		Expect(string(data)).To(ContainSubstring("qrmi_epilog_status_code=0\n"))
	})

	It("takes administrator settings from hook arguments", func() {
		got, err := parseHookArgs([]string{"--config=/etc/site/qrmi.json", "--log-level=4", "alice"})
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(hookConfig{ConfigPath: "/etc/site/qrmi.json", LogLevel: "4", JobOwner: "alice"}))
		_, err = parseHookArgs([]string{"alice", "bob"})
		Expect(err).To(HaveOccurred())
	})
})
