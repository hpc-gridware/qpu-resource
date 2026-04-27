// Copyright 2026 Pasqal, HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

package qrmiocs_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/hpc-gridware/qpu-resource/src/internal/qrmiocs"
)

var _ = Describe("RustLogFromDebug", func() {
	DescribeTable("maps numeric scheduler debug levels",
		func(in, want string) {
			got, ok := qrmiocs.RustLogFromDebug(in)
			Expect(ok).To(BeTrue())
			Expect(got).To(Equal(want))
		},
		Entry("0 maps to error", "0", "error"),
		Entry("1 maps to error", "1", "error"),
		Entry("2 maps to error", "2", "error"),
		Entry("3 maps to info", "3", "info"),
		Entry("4 maps to debug", "4", "debug"),
		Entry("5 maps to trace", "5", "trace"),
		Entry("9 maps to trace", "9", "trace"),
	)

	DescribeTable("accepts literal level words",
		func(in, want string) {
			got, ok := qrmiocs.RustLogFromDebug(in)
			Expect(ok).To(BeTrue())
			Expect(got).To(Equal(want))
		},
		Entry("error", "error", "error"),
		Entry("WARN", "WARN", "warn"),
		Entry("Info", "Info", "info"),
		Entry("debug with whitespace", "  debug  ", "debug"),
		Entry("TRACE", "TRACE", "trace"),
	)

	It("returns ok=false for empty input", func() {
		_, ok := qrmiocs.RustLogFromDebug("")
		Expect(ok).To(BeFalse())
	})

	It("returns ok=false for unknown words", func() {
		_, ok := qrmiocs.RustLogFromDebug("verbose")
		Expect(ok).To(BeFalse())
	})
})
