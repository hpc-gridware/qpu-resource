// Copyright 2026 Pasqal, HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

package qrmiocs

import (
	"strconv"
	"strings"
)

// RustLogFromDebug maps a scheduler debug level value to a RUST_LOG value.
// The input may be a numeric level (matching the C prolog mapping
// 2 -> error, 3 -> info, 4 -> debug, >=5 -> trace) or one of the literal
// log level words error, warn, info, debug, trace.
//
// Returns ok=false if the input does not map to a known level so the caller
// can decide whether to leave RUST_LOG untouched.
func RustLogFromDebug(raw string) (level string, ok bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", false
	}
	if n, err := strconv.Atoi(v); err == nil {
		switch {
		case n <= 2:
			return "error", true
		case n == 3:
			return "info", true
		case n == 4:
			return "debug", true
		default:
			return "trace", true
		}
	}
	switch strings.ToLower(v) {
	case "error", "warn", "info", "debug", "trace":
		return strings.ToLower(v), true
	}
	return "", false
}
