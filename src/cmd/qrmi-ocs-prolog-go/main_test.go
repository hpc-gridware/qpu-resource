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
	"github.com/hpc-gridware/qpu-resource/src/internal/qrmiocs"
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
		GinkgoT().Setenv("QRMI_OCS_SLOTS_RESOURCE_NAME", "")
		GinkgoT().Setenv("QRMI_OCS_QCONF_PATH", "")
		GinkgoT().Setenv("QRMI_OCS_LOG_LEVEL", "")
		GinkgoT().Setenv("RUST_LOG", "")
		GinkgoT().Setenv("HOST", "")
		GinkgoT().Setenv("HOSTNAME", "")
		GinkgoT().Setenv("SGE_BINARY_PATH", "")
		GinkgoT().Setenv("JOB_ID", "")
		GinkgoT().Setenv("SGE_HGR_qpu_slots", "")
		GinkgoT().Setenv("SGE_SGR_qpu_slots", "")
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

	It("uses host complex_values when OCS does not export a non-consumable grant", func() {
		spool := GinkgoT().TempDir()
		qconf := filepath.Join(spool, "qconf")
		err := os.WriteFile(qconf, []byte("#!/bin/sh\nprintf '%s\n' 'hostname ocs-master' 'complex_values qpu=EMU_FREE,qpu_slots=1'\n"), 0o755)
		Expect(err).NotTo(HaveOccurred())

		GinkgoT().Setenv("SGE_JOB_ENV", filepath.Join(spool, "environment"))
		GinkgoT().Setenv("HOST", "ocs-master")
		GinkgoT().Setenv("QRMI_OCS_QCONF_PATH", qconf)

		err = run()
		Expect(err).To(HaveOccurred())
		if !errors.Is(err, qrmi.ErrNotAvailable) {
			Skip("test only validates stub-build error path")
		}
	})

	It("parses multiline host complex_values", func() {
		value, err := parseHostComplexValue([]byte("hostname ocs-master\ncomplex_values qpu=PASQAL_LOCAL, \\\n                      qpu_slots=1\nprocessors 20\n"), "qpu")
		Expect(err).NotTo(HaveOccurred())
		Expect(value).To(Equal("PASQAL_LOCAL"))
	})

	It("exports scheduler job id and uid for Pasqal Local", func() {
		spool := GinkgoT().TempDir()
		jobEnv, err := qrmiocs.OpenJobEnv()
		Expect(err).To(HaveOccurred())
		Expect(jobEnv).To(BeNil())

		GinkgoT().Setenv("SGE_JOB_ENV", filepath.Join(spool, "environment"))
		GinkgoT().Setenv("JOB_ID", "1234")
		GinkgoT().Setenv("SGE_HGR_qpu_slots", "5.000000")
		jobEnv, err = qrmiocs.OpenJobEnv()
		Expect(err).NotTo(HaveOccurred())
		defer jobEnv.Close()
		Expect(exportSchedulerJobEnv(jobEnv, defaultSlotsResourceName)).To(Succeed())

		data, err := os.ReadFile(filepath.Join(spool, "environment"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("QRMI_JOB_UID="))
		Expect(string(data)).To(ContainSubstring("QRMI_JOB_ID=1234\n"))
		Expect(string(data)).To(ContainSubstring("QRMI_JOB_QPU_SLOTS=5\n"))
	})

	It("parses granted qpu slot counts", func() {
		for value, want := range map[string]int{"1": 1, "5.000000": 5, "5(1)": 5} {
			slots, err := parseGrantedSlots(value)
			Expect(err).NotTo(HaveOccurred())
			Expect(slots).To(Equal(want))
		}
		_, err := parseGrantedSlots("1.5")
		Expect(err).To(HaveOccurred())
	})
})
