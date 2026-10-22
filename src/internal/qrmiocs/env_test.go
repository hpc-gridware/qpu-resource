// Copyright 2026 Pasqal, HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

package qrmiocs_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/hpc-gridware/qpu-resource/src/internal/qrmiocs"
)

var _ = Describe("JobEnv", func() {
	var (
		path string
		je   *qrmiocs.JobEnv
	)

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		path = filepath.Join(dir, "environment")
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", dir)
		Expect(os.WriteFile(path, nil, 0o644)).To(Succeed())

		var err error
		je, err = qrmiocs.OpenJobEnv()
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		Expect(je.Close()).To(Succeed())
	})

	It("mirrors os.Setenv into the job env file", func() {
		Expect(je.Set("FOO", "bar")).To(Succeed())
		Expect(os.Getenv("FOO")).To(Equal("bar"))

		data, err := os.ReadFile(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("FOO=bar\n"))
		info, err := os.Stat(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})

	It("appends multiple lines in order", func() {
		Expect(je.Set("A", "1")).To(Succeed())
		Expect(je.Set("B", "2")).To(Succeed())

		data, err := os.ReadFile(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(data)).To(Equal("A=1\nB=2\n"))
	})

	It("records both QRMI_PLUGIN_ERROR and qrmi_prolog_status=error on SetPluginError", func() {
		Expect(je.SetPluginError("backend not accessible")).To(Succeed())

		data, err := os.ReadFile(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("QRMI_PLUGIN_ERROR=backend not accessible\n"))
		Expect(string(data)).To(ContainSubstring("qrmi_prolog_status=error\n"))
	})
})

var _ = Describe("JobEnv.ApplyRustLog", func() {
	var (
		path string
		je   *qrmiocs.JobEnv
	)

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		path = filepath.Join(dir, "environment")
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", dir)
		GinkgoT().Setenv("RUST_LOG", "")

		var err error
		je, err = qrmiocs.OpenJobEnv()
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		Expect(je.Close()).To(Succeed())
	})

	It("maps the hook's log level to RUST_LOG", func() {
		Expect(je.ApplyRustLog("4")).To(Succeed())
		Expect(os.Getenv("RUST_LOG")).To(Equal("debug"))
		data, _ := os.ReadFile(path)
		Expect(string(data)).To(ContainSubstring("RUST_LOG=debug\n"))
	})

	It("is a no-op for an empty or unknown level", func() {
		Expect(je.ApplyRustLog("")).To(Succeed())
		Expect(je.ApplyRustLog("verbose")).To(Succeed())
		Expect(os.Getenv("RUST_LOG")).To(Equal(""))
	})
})

var _ = Describe("ResetProcessEnv", func() {
	It("keeps only the variables OCS sets for the hooks", func() {
		GinkgoT().Setenv("JOB_ID", "42")
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", "/spool/42.1")
		GinkgoT().Setenv("PASQAL_CONFIG_ROOT", "/root")
		GinkgoT().Setenv("EMU_FREE_QRMI_PASQAL_CLOUD_AUTH_ENDPOINT", "https://example.invalid")
		GinkgoT().Setenv("PATH", "/tmp/evil")
		GinkgoT().Setenv("HOME", "/home/alice")
		Expect(qrmiocs.ResetProcessEnv()).To(Succeed())
		Expect(os.Getenv("JOB_ID")).To(Equal("42"))
		Expect(os.Getenv("SGE_JOB_SPOOL_DIR")).To(Equal("/spool/42.1"))
		Expect(os.Getenv("PATH")).To(Equal(qrmiocs.TrustedPath))
		Expect(os.Getenv("HOME")).NotTo(Equal("/home/alice"))
		Expect(os.Getenv("LC_ALL")).To(Equal("C"))
		_, ok := os.LookupEnv("PASQAL_CONFIG_ROOT")
		Expect(ok).To(BeFalse())
		_, ok = os.LookupEnv("EMU_FREE_QRMI_PASQAL_CLOUD_AUTH_ENDPOINT")
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("CheckLoaderEnv", func() {
	It("fails the job when loader variables are still set", func() {
		for _, kv := range []string{"LD_AUDIT=/tmp/x.so", "LD_PRELOAD=/tmp/x.so", "GLIBC_TUNABLES=glibc.x=1"} {
			err := qrmiocs.CheckLoaderEnv([]string{"JOB_ID=1", kv})
			Expect(qrmiocs.ExitCode(err)).To(Equal(qrmiocs.ExitJobError))
		}
		Expect(qrmiocs.CheckLoaderEnv([]string{"JOB_ID=1", "OLD_LD=x"})).To(Succeed())
	})

	It("rejects a tampered trusted value", func() {
		_, err := qrmiocs.CleanEnv([]string{"JOB_ID=1;rm"})
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("ExitCode", func() {
	It("maps hook errors to OCS exit codes", func() {
		Expect(qrmiocs.ExitCode(nil)).To(Equal(0))
		Expect(qrmiocs.ExitCode(errors.New("host"))).To(Equal(1))
		Expect(qrmiocs.ExitCode(qrmiocs.RetryError(errors.New("busy")))).To(Equal(99))
		Expect(qrmiocs.ExitCode(fmt.Errorf("wrapped: %w", qrmiocs.JobError(errors.New("bad"))))).To(Equal(100))
	})
})
