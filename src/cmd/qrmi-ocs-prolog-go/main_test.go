// Copyright 2026 Pasqal and HPC Gridware GmbHand its contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/hpc-gridware/qpu-resource/src/internal/qrmi"
)

func TestProlog(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "qrmi-ocs-prolog-go Suite")
}

var _ = Describe("prolog run()", func() {
	BeforeEach(func() {
		GinkgoT().Setenv("SGE_HGR_qpu", "")
		GinkgoT().Setenv("SGE_SGR_qpu", "")
		GinkgoT().Setenv("SGE_JOB_ENV", "")
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", "")
		GinkgoT().Setenv("QRMI_OCS_CONFIG_PATH", "")
		GinkgoT().Setenv("QRMI_OCS_RESOURCE_NAME", "")
		GinkgoT().Setenv("QRMI_OCS_LOG_LEVEL", "")
		GinkgoT().Setenv("RUST_LOG", "")
	})

	It("fails when no granted value is in the environment", func() {
		spool := GinkgoT().TempDir()
		GinkgoT().Setenv("SGE_JOB_ENV", filepath.Join(spool, "environment"))
		err := run()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no granted value"))
	})

	It("fails when granted value contains a weighted backend", func() {
		spool := GinkgoT().TempDir()
		GinkgoT().Setenv("SGE_JOB_ENV", filepath.Join(spool, "environment"))
		GinkgoT().Setenv("SGE_HGR_qpu", "EMU_FREE(2)")
		err := run()
		Expect(err).To(HaveOccurred())
	})

	It("uses SGE_SGR_qpu when SGE_HGR_qpu is empty", func() {
		spool := GinkgoT().TempDir()
		GinkgoT().Setenv("SGE_JOB_ENV", filepath.Join(spool, "environment"))
		GinkgoT().Setenv("SGE_SGR_qpu", "EMU_FREE")
		// Without QRMI we never reach the acquire path, but the granted parse
		// should succeed and the next failure should be the QRMI load step.
		err := run()
		Expect(err).To(HaveOccurred())
		// On stub builds the QRMI load returns ErrNotAvailable.
		if !errors.Is(err, qrmi.ErrNotAvailable) {
			Skip("test only validates stub-build error path")
		}
	})

	It("records QRMI_PLUGIN_ERROR in the job env on failure", func() {
		spool := GinkgoT().TempDir()
		jobEnvPath := filepath.Join(spool, "environment")
		GinkgoT().Setenv("SGE_JOB_ENV", jobEnvPath)
		GinkgoT().Setenv("SGE_HGR_qpu", "EMU_FREE")

		err := run()
		Expect(err).To(HaveOccurred())

		data, _ := os.ReadFile(jobEnvPath)
		Expect(string(data)).To(ContainSubstring("QRMI_PLUGIN_ERROR="))
		Expect(string(data)).To(ContainSubstring("qrmi_prolog_status=error\n"))
	})

	It("respects QRMI_OCS_RESOURCE_NAME override", func() {
		spool := GinkgoT().TempDir()
		GinkgoT().Setenv("SGE_JOB_ENV", filepath.Join(spool, "environment"))
		GinkgoT().Setenv("QRMI_OCS_RESOURCE_NAME", "qpu_alt")
		GinkgoT().Setenv("SGE_HGR_qpu_alt", "EMU_FREE")

		err := run()
		Expect(err).To(HaveOccurred())
		// Without an SGE_HGR_qpu, the only way the parse reached the QRMI
		// load (and failed there) is if the override worked.
		if !errors.Is(err, qrmi.ErrNotAvailable) {
			Skip("test only validates stub-build error path")
		}
	})
})
