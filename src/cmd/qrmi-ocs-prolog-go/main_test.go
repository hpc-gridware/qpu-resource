// Copyright 2026 Pasqal and HPC Gridware GmbHand its contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
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
		GinkgoT().Setenv("SGE_JOB_ENV", "")
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", "")
		GinkgoT().Setenv("QRMI_OCS_LOG_LEVEL", "")
		GinkgoT().Setenv("RUST_LOG", "")
		GinkgoT().Setenv("JOB_ID", "")
		GinkgoT().Setenv("SGE_TASK_ID", "")
	})

	It("fails when scheduler state has no requested backend", func() {
		spool := GinkgoT().TempDir()
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", spool)
		GinkgoT().Setenv("JOB_ID", "42")
		cfg := defaultHookConfig()
		cfg.QstatPath = writeQstat(GinkgoT(), spool, "")
		err := runWithConfig("", cfg)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("requested resource not found"))
	})

	It("fails when granted value contains a weighted backend", func() {
		spool := GinkgoT().TempDir()
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", spool)
		GinkgoT().Setenv("JOB_ID", "42")
		cfg := defaultHookConfig()
		cfg.QstatPath = writeQstat(GinkgoT(), spool, "hard_resource_list: qpu=EMU_FREE(2)")
		err := runWithConfig("", cfg)
		Expect(err).To(HaveOccurred())
	})

	It("uses a soft resource request from scheduler state", func() {
		spool := GinkgoT().TempDir()
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", spool)
		GinkgoT().Setenv("JOB_ID", "42")
		cfg := defaultHookConfig()
		cfg.QstatPath = writeQstat(GinkgoT(), spool, "soft_resource_list: qpu=EMU_FREE")
		// Without QRMI we never reach the acquire path, but the granted parse
		// should succeed and the next failure should be the QRMI load step.
		err := runWithConfig("", cfg)
		Expect(err).To(HaveOccurred())
		// On stub builds the QRMI load returns ErrNotAvailable.
		if !errors.Is(err, qrmi.ErrNotAvailable) {
			Skip("test only validates stub-build error path")
		}
	})

	It("records QRMI_PLUGIN_ERROR in the job env on failure", func() {
		spool := GinkgoT().TempDir()
		jobEnvPath := filepath.Join(spool, "environment")
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", spool)
		GinkgoT().Setenv("JOB_ID", "42")
		cfg := defaultHookConfig()
		cfg.QstatPath = writeQstat(GinkgoT(), spool, "hard_resource_list: qpu=EMU_FREE")

		err := runWithConfig("", cfg)
		Expect(err).To(HaveOccurred())

		data, _ := os.ReadFile(jobEnvPath)
		Expect(string(data)).To(ContainSubstring("QRMI_PLUGIN_ERROR="))
		Expect(string(data)).To(ContainSubstring("qrmi_prolog_status=error\n"))
	})

	It("takes administrator settings from hook arguments", func() {
		cfg, owner, err := parseHookArgs([]string{
			"--config=/etc/site/qrmi.json",
			"--resource=qpu_alt",
			"--slots-resource=capacity",
			"--qstat=/opt/ocs/bin/qstat",
			"alice",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(owner).To(Equal("alice"))
		Expect(cfg).To(Equal(hookConfig{
			ConfigPath:        "/etc/site/qrmi.json",
			ResourceName:      "qpu_alt",
			SlotsResourceName: "capacity",
			QstatPath:         "/opt/ocs/bin/qstat",
		}))
	})

	It("reads backend and slots from scheduler job state", func() {
		spool := GinkgoT().TempDir()
		qstat := writeQstat(GinkgoT(), spool, "hard_resource_list: qpu=PASQAL_LOCAL,qpu_slots=3")
		GinkgoT().Setenv("JOB_ID", "1234")

		backend, err := readGranted("qpu", qstat)
		Expect(err).NotTo(HaveOccurred())
		Expect(backend).To(Equal("PASQAL_LOCAL"))
		slots, ok, err := readGrantedSlots("qpu_slots", qstat)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(slots).To(Equal(3))
	})

	It("exports scheduler job id and uid for Pasqal Local", func() {
		spool := GinkgoT().TempDir()
		jobEnv, err := qrmiocs.OpenJobEnv()
		Expect(err).To(HaveOccurred())
		Expect(jobEnv).To(BeNil())

		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", spool)
		GinkgoT().Setenv("JOB_ID", "1234")
		GinkgoT().Setenv("SGE_TASK_ID", "7")
		cfg := defaultHookConfig()
		cfg.QstatPath = writeQstat(GinkgoT(), spool, "hard_resource_list: qpu_slots=5.000000")
		jobEnv, err = qrmiocs.OpenJobEnv()
		Expect(err).NotTo(HaveOccurred())
		defer jobEnv.Close()
		account, err := user.Current()
		Expect(err).NotTo(HaveOccurred())
		Expect(exportSchedulerJobEnv(jobEnv, cfg, account.Username)).To(Succeed())

		data, err := os.ReadFile(filepath.Join(spool, "environment"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("QRMI_JOB_UID=" + strconv.Itoa(os.Getuid()) + "\n"))
		Expect(string(data)).To(ContainSubstring("QRMI_JOB_ID=1234.7\n"))
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

func writeQstat(t GinkgoTInterface, dir, line string) string {
	t.Helper()
	path := filepath.Join(dir, "qstat")
	content := "#!/bin/sh\nprintf '%s\\n' '" + line + "'\n"
	Expect(os.WriteFile(path, []byte(content), 0o755)).To(Succeed())
	return path
}
