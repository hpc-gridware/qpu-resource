// Copyright 2026 Pasqal, HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

package qrmiocs_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestQrmiocs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "qrmiocs Suite")
}
