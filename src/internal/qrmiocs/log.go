// Copyright 2026 Pasqal, HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

package qrmiocs

import (
	"fmt"
	"io"
	"os"
)

// Logger writes prefixed log lines so prolog and epilog output is easy to
// recognize in scheduler logs. Output format matches the C version exactly:
//
//	<component>[<LEVEL>]: <message>
type Logger struct {
	component string
	stdout    io.Writer
	stderr    io.Writer
}

// NewLogger returns a Logger that writes to os.Stdout for INFO and os.Stderr
// for ERROR/WARN. The component is the binary name used in the prefix
// (for example "qrmi-ocs-prolog").
func NewLogger(component string) *Logger {
	return &Logger{component: component, stdout: os.Stdout, stderr: os.Stderr}
}

// Info writes an INFO line to stdout.
func (l *Logger) Info(format string, args ...any) {
	l.write(l.stdout, "INFO", format, args...)
}

// Warn writes a WARN line to stderr.
func (l *Logger) Warn(format string, args ...any) {
	l.write(l.stderr, "WARN", format, args...)
}

// Error writes an ERROR line to stderr.
func (l *Logger) Error(format string, args ...any) {
	l.write(l.stderr, "ERROR", format, args...)
}

// QRMILog writes a QRMI runtime log record using the hook logger.
func (l *Logger) QRMILog(level, target, message string) {
	if target == "" {
		target = "qrmi"
	}
	switch level {
	case "ERROR":
		l.Error("QRMI %s: %s", target, message)
	case "WARN":
		l.Warn("QRMI %s: %s", target, message)
	case "INFO":
		l.Info("QRMI %s: %s", target, message)
	case "DEBUG", "TRACE":
		l.Info("QRMI %s %s: %s", level, target, message)
	default:
		l.Info("QRMI %s %s: %s", level, target, message)
	}
}

func (l *Logger) write(w io.Writer, level, format string, args ...any) {
	fmt.Fprintf(w, "%s[%s]: ", l.component, level)
	fmt.Fprintf(w, format, args...)
	fmt.Fprintln(w)
}
