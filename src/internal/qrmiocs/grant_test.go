// Copyright 2026 Pasqal, HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

package qrmiocs_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/hpc-gridware/qpu-resource/src/internal/qrmiocs"
)

var _ = Describe("ParseGrantedBackend", func() {
	DescribeTable("accepts forms produced by the scheduler",
		func(in, want string) {
			got, err := qrmiocs.ParseGrantedBackend(in)
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("plain backend name", "EMU_FREE", "EMU_FREE"),
		Entry("with surrounding whitespace", "  EMU_FREE  ", "EMU_FREE"),
		Entry("name=value form", "qpu=EMU_FREE", "EMU_FREE"),
		Entry("trailing semicolon", "EMU_FREE;", "EMU_FREE"),
		Entry("brackets stripped", "[EMU_FREE]", "EMU_FREE"),
		Entry("braces stripped", "{EMU_FREE}", "EMU_FREE"),
		Entry("name=value with brackets", "[qpu=EMU_FREE]", "EMU_FREE"),
	)

	DescribeTable("rejects multi-token or weighted grants",
		func(in string) {
			_, err := qrmiocs.ParseGrantedBackend(in)
			Expect(err).To(HaveOccurred())
		},
		Entry("empty", ""),
		Entry("whitespace only", "   "),
		Entry("parens with weight", "EMU_FREE(2)"),
		Entry("comma list", "EMU_FREE,FREE_EMU"),
		Entry("space list", "EMU_FREE FREE_EMU"),
		Entry("name= with empty rhs", "qpu="),
	)
})
