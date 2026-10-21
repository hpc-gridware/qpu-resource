// Copyright 2026 Pasqal and HPC Gridware GmbHand its contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/hpc-gridware/qpu-resource/src/internal/qrmiocs"
)

func TestProlog(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "qrmi-ocs-prolog-go Suite")
}

var _ = Describe("prolog run()", func() {
	BeforeEach(func() {
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", "")
		GinkgoT().Setenv("RUST_LOG", "")
		GinkgoT().Setenv("JOB_ID", "")
		GinkgoT().Setenv("SGE_TASK_ID", "")
		checkTrustedExecutable = func(path string) (string, error) { return path, nil }
		DeferCleanup(func() { checkTrustedExecutable = trustedExecutable })
	})

	It("does nothing when the job requests no backend", func() {
		spool := GinkgoT().TempDir()
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", spool)
		GinkgoT().Setenv("JOB_ID", "42")
		cfg := defaultHookConfig()
		cfg.QstatPath = writeQstat(GinkgoT(), spool, "hard_resource_list: h_rt=60")
		Expect(runWithConfig("", cfg)).To(Succeed())
		_, err := os.Stat(filepath.Join(spool, "environment"))
		Expect(os.IsNotExist(err)).To(BeTrue())
	})

	It("rejects a weighted backend as a job error", func() {
		spool := GinkgoT().TempDir()
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", spool)
		GinkgoT().Setenv("JOB_ID", "42")
		cfg := defaultHookConfig()
		cfg.QstatPath = writeQstat(GinkgoT(), spool, "hard_resource_list: qpu=EMU_FREE(2)")
		err := runWithConfig("", cfg)
		Expect(qrmiocs.ExitCode(err)).To(Equal(qrmiocs.ExitJobError))
	})

	It("ignores a soft resource request", func() {
		spool := GinkgoT().TempDir()
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", spool)
		GinkgoT().Setenv("JOB_ID", "42")
		cfg := defaultHookConfig()
		cfg.QstatPath = writeQstat(GinkgoT(), spool, "soft_resource_list: qpu=EMU_FREE")
		Expect(runWithConfig("", cfg)).To(Succeed())
	})

	It("rejects a backend the host does not offer as a job error", func() {
		spool := GinkgoT().TempDir()
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", spool)
		GinkgoT().Setenv("JOB_ID", "42")
		cfg := defaultHookConfig()
		cfg.QstatPath = writeQstat(GinkgoT(), spool, "hard_resource_list: qpu=PASQAL_LOCAL")
		writeQconf(GinkgoT(), spool, "complex_values        slots=20,qpu=EMU_FREE")
		err := runWithConfig("", cfg)
		Expect(err).To(HaveOccurred())
		Expect(qrmiocs.ExitCode(err)).To(Equal(qrmiocs.ExitJobError))
		Expect(err.Error()).To(ContainSubstring("does not offer qpu=PASQAL_LOCAL"))
	})

	It("reads a scoped request and continuation lines", func() {
		out := []byte("hard_resource_list:              slurm_shim=TRUE\n" +
			"master_hard_resource_list:       h_rt=60, \\\n" +
			"                                 qpu=PASQAL_LOCAL\n" +
			"soft_resource_list:              qpu=EMU_FREE\n")
		backend, err := parseJobRequestedResource(out, "qpu")
		Expect(err).NotTo(HaveOccurred())
		Expect(backend).To(Equal("PASQAL_LOCAL"))
		offered, ok := resourceFromList(fieldValue([]byte("hostname ocs-worker2\n"+
			"complex_values        qpu_slots=1,slots=20, \\\n"+
			"                      qpu=PASQAL_LOCAL\n"), "complex_values"), "qpu")
		Expect(ok).To(BeTrue())
		Expect(offered).To(Equal("PASQAL_LOCAL"))
	})

	It("requires an absolute, root-owned qstat", func() {
		_, _, err := parseHookArgs([]string{"--qstat=qstat"})
		Expect(err).To(HaveOccurred())
		_, err = trustedExecutable("qstat")
		Expect(err).To(HaveOccurred())
	})

	It("records QRMI_PLUGIN_ERROR in the job env on failure", func() {
		spool := GinkgoT().TempDir()
		jobEnvPath := filepath.Join(spool, "environment")
		GinkgoT().Setenv("SGE_JOB_SPOOL_DIR", spool)
		GinkgoT().Setenv("JOB_ID", "42")
		cfg := defaultHookConfig()
		cfg.QstatPath = writeQstat(GinkgoT(), spool, "hard_resource_list: qpu=EMU_FREE")
		writeQconf(GinkgoT(), spool, "complex_values        qpu=EMU_FREE")
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
			"--log-level=4",
			"alice",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(owner).To(Equal("alice"))
		Expect(cfg).To(Equal(hookConfig{
			ConfigPath:        "/etc/site/qrmi.json",
			ResourceName:      "qpu_alt",
			SlotsResourceName: "capacity",
			QstatPath:         "/opt/ocs/bin/qstat",
			LogLevel:          "4",
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
	return writeScript(t, filepath.Join(dir, "qstat"), line)
}

// writeQconf writes the fake qconf next to the fake qstat, where the
// prolog looks for it.
func writeQconf(t GinkgoTInterface, dir, line string) string {
	t.Helper()
	return writeScript(t, filepath.Join(dir, "qconf"), line)
}

func writeScript(t GinkgoTInterface, path, line string) string {
	t.Helper()
	content := "#!/bin/sh\nprintf '%s\\n' '" + line + "'\n"
	Expect(os.WriteFile(path, []byte(content), 0o755)).To(Succeed())
	return path
}
