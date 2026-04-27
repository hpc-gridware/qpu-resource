// Copyright 2026 Pasqal, HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

package qrmiocs_test

import (
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/hpc-gridware/qpu-resource/src/internal/qrmiocs"
)

var _ = Describe("Metadata TSV", func() {
	var path string

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		path = filepath.Join(dir, "acquired.tsv")
	})

	It("round-trips a single record", func() {
		rec := qrmiocs.Record{Name: "EMU_FREE", Type: 1, Token: "token-abc", AcquiredEpoch: 1730000000}
		Expect(qrmiocs.WriteAtomic(path, []qrmiocs.Record{rec})).To(Succeed())

		got, err := qrmiocs.ReadStrictSingle(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(rec))
	})

	It("ignores blank lines on read", func() {
		Expect(os.WriteFile(path, []byte("\nEMU_FREE\t1\ttoken\t42\n\n"), 0o644)).To(Succeed())
		got, err := qrmiocs.ReadStrictSingle(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(got.Name).To(Equal("EMU_FREE"))
		Expect(got.Type).To(Equal(1))
		Expect(got.Token).To(Equal("token"))
		Expect(got.AcquiredEpoch).To(BeEquivalentTo(42))
	})

	It("reports ErrMultipleRecords when more than one record is present", func() {
		Expect(os.WriteFile(path, []byte("a\t1\tt1\t1\nb\t1\tt2\t2\n"), 0o644)).To(Succeed())
		_, err := qrmiocs.ReadStrictSingle(path)
		Expect(errors.Is(err, qrmiocs.ErrMultipleRecords)).To(BeTrue())
	})

	It("reports ErrNoRecord on an empty file", func() {
		Expect(os.WriteFile(path, []byte(""), 0o644)).To(Succeed())
		_, err := qrmiocs.ReadStrictSingle(path)
		Expect(errors.Is(err, qrmiocs.ErrNoRecord)).To(BeTrue())
	})

	It("errors on a malformed line", func() {
		Expect(os.WriteFile(path, []byte("missing-fields-only\n"), 0o644)).To(Succeed())
		_, err := qrmiocs.ReadStrictSingle(path)
		Expect(err).To(HaveOccurred())
	})

	It("errors on a non-numeric type field", func() {
		Expect(os.WriteFile(path, []byte("a\tnope\ttok\t1\n"), 0o644)).To(Succeed())
		_, err := qrmiocs.ReadStrictSingle(path)
		Expect(err).To(HaveOccurred())
	})

	It("rejects records with embedded tabs", func() {
		err := qrmiocs.WriteAtomic(path, []qrmiocs.Record{{Name: "bad\tname", Type: 1, Token: "t", AcquiredEpoch: 1}})
		Expect(err).To(HaveOccurred())
	})

	It("returns os.ErrNotExist when the file is missing", func() {
		_, err := qrmiocs.ReadStrictSingle(filepath.Join(GinkgoT().TempDir(), "missing.tsv"))
		Expect(errors.Is(err, os.ErrNotExist)).To(BeTrue())
	})
})
