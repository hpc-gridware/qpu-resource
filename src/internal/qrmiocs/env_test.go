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

var _ = Describe("JobEnv", func() {
	var (
		path string
		je   *qrmiocs.JobEnv
	)

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		path = filepath.Join(dir, "environment")
		GinkgoT().Setenv("SGE_JOB_ENV", path)
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", "")

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

var _ = Describe("JobEnv.ApplyDefaultRustLog", func() {
	var (
		path string
		je   *qrmiocs.JobEnv
	)

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		path = filepath.Join(dir, "environment")
		GinkgoT().Setenv("SGE_JOB_ENV", path)
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", "")
		GinkgoT().Setenv("RUST_LOG", "")
		GinkgoT().Setenv("QRMI_OCS_LOG_LEVEL", "")
		GinkgoT().Setenv("SGE_DEBUG_LEVEL", "")

		var err error
		je, err = qrmiocs.OpenJobEnv()
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		Expect(je.Close()).To(Succeed())
	})

	It("does not overwrite RUST_LOG when already set", func() {
		GinkgoT().Setenv("RUST_LOG", "warn")
		GinkgoT().Setenv("SGE_DEBUG_LEVEL", "5")
		Expect(je.ApplyDefaultRustLog()).To(Succeed())
		Expect(os.Getenv("RUST_LOG")).To(Equal("warn"))

		data, _ := os.ReadFile(path)
		Expect(string(data)).ToNot(ContainSubstring("RUST_LOG="))
	})

	It("derives from QRMI_OCS_LOG_LEVEL first", func() {
		GinkgoT().Setenv("QRMI_OCS_LOG_LEVEL", "4")
		GinkgoT().Setenv("SGE_DEBUG_LEVEL", "2")
		Expect(je.ApplyDefaultRustLog()).To(Succeed())
		Expect(os.Getenv("RUST_LOG")).To(Equal("debug"))
	})

	It("falls back to SGE_DEBUG_LEVEL", func() {
		GinkgoT().Setenv("SGE_DEBUG_LEVEL", "3")
		Expect(je.ApplyDefaultRustLog()).To(Succeed())
		Expect(os.Getenv("RUST_LOG")).To(Equal("info"))
	})

	It("is a no-op when neither maps to a known level", func() {
		Expect(je.ApplyDefaultRustLog()).To(Succeed())
		Expect(os.Getenv("RUST_LOG")).To(Equal(""))
	})
})
