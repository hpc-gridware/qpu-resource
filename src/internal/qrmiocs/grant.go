// Copyright 2026 Pasqal, HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

package qrmiocs

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// trimCutset is the set of characters trimmed from both sides of a granted
// resource value. It mirrors the behavior of trim_token in the original C
// prolog: brackets, braces, parens, commas, semicolons, and whitespace.
const trimCutset = "[]{}(),;"

// ParseGrantedBackend parses a single backend name from the granted resource
// value supplied by the scheduler in SGE_HGR_<resource> or SGE_SGR_<resource>.
//
// The current single-backend model accepts forms like:
//
//	EMU_FREE
//	qpu=EMU_FREE
//	[EMU_FREE]
//
// It rejects parenthesized values, embedded commas, or embedded whitespace,
// because those would imply a multi-backend or weighted grant that is not
// supported.
func ParseGrantedBackend(raw string) (string, error) {
	v := trim(raw)
	if v == "" {
		return "", errors.New("granted value is empty")
	}
	if strings.ContainsAny(v, "()") {
		return "", fmt.Errorf("granted value %q contains parentheses", raw)
	}
	if i := strings.IndexByte(v, '='); i >= 0 {
		v = trim(v[i+1:])
	}
	if v == "" {
		return "", fmt.Errorf("granted value %q has no backend name", raw)
	}
	if strings.ContainsAny(v, ", \t\r\n") {
		return "", fmt.Errorf("granted value %q has multiple tokens", raw)
	}
	return v, nil
}

func trim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(trimCutset, r)
	})
}
