// Copyright 2026 Pasqal and HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

// Package qrmi wraps the QRMI C API (libqrmi) so Go callers do not have to
// touch C string lifetimes or return-code values directly.
//
// The cgo bridge lives in qrmi_real.go and is only compiled when the build
// tag "qrmi" is set. Without that tag, the package compiles using the
// stubs in qrmi_stub.go so the rest of the repository can be built and
// tested on machines that do not have libqrmi or the QRMI headers
// installed. Production builds of the queue hooks must pass -tags qrmi.
package qrmi

import "errors"

// ErrNotAvailable is returned by stub implementations when the package is
// built without the qrmi tag. Callers can use it to surface a clear error
// instead of a runtime nil dereference.
var ErrNotAvailable = errors.New("qrmi: built without -tags qrmi; no libqrmi support")

// EnvVar is one entry from a resource definition's environment list. The
// key is the variable name expected by the QRMI runtime (without backend
// prefix); the value is the configured value from qrmi_config.json.
type EnvVar struct {
	Key   string
	Value string
}
