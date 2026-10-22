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
		kept  string
	)

	BeforeEach(func() {
		host := GinkgoT().TempDir()
		spool = filepath.Join(host, "active_jobs", "42.1")
		Expect(os.MkdirAll(spool, 0o755)).To(Succeed())
		meta = filepath.Join(spool, "qrmi_ocs_acquired.tsv")
		usage = filepath.Join(spool, "usage")
		kept = filepath.Join(host, "qrmi_failed_releases", "42.1.tsv")

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

	It("moves the metadata file out of the job spool after a parse failure", func() {
		Expect(os.WriteFile(meta, []byte("badline\n"), 0o644)).To(Succeed())
		Expect(run(cfg)).To(Equal(1))

		Expect(meta).ToNot(BeAnExistingFile())
		Expect(os.ReadFile(kept)).To(Equal([]byte("badline\n")))
	})

	It("exits 0 and moves the metadata file out of the job spool after a release failure", func() {
		Expect(os.WriteFile(meta, []byte("PASQAL_LOCAL\t4\tsession-id\t1\n"), 0o600)).To(Succeed())
		Expect(run(cfg)).To(Equal(0))

		Expect(meta).ToNot(BeAnExistingFile())
		Expect(os.ReadFile(kept)).To(Equal([]byte("PASQAL_LOCAL\t4\tsession-id\t1\n")))
		info, err := os.Stat(filepath.Dir(kept))
		Expect(err).ToNot(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o700)))
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
