package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hpc-gridware/qpu-resource/src/internal/availability"
)

type sequenceProvider struct {
	statuses []availability.AvailabilityStatus
	errs     []error
	calls    int
}

func (p *sequenceProvider) Status(context.Context) (availability.AvailabilityStatus, error) {
	i := p.calls
	p.calls++
	if i >= len(p.statuses) {
		return availability.AvailabilityStatus{Ready: false, Reason: "exhausted"}, nil
	}
	var err error
	if i < len(p.errs) {
		err = p.errs[i]
	}
	return p.statuses[i], err
}

func TestProtocolMultipleCyclesAndQuit(t *testing.T) {
	provider := &sequenceProvider{
		statuses: []availability.AvailabilityStatus{
			{Ready: true, Reason: "one"},
			{Ready: false, Reason: "two"},
		},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runProtocol(
		context.Background(),
		provider,
		"global",
		"qpu_ready",
		"",
		time.Second,
		strings.NewReader("\n\nquit\n"),
		&stdout,
		&stderr,
	)
	if err != nil {
		t.Fatalf("runProtocol returned error: %v", err)
	}
	want := "begin\nglobal:qpu_ready:1\nend\nbegin\nglobal:qpu_ready:0\nend\n"
	if stdout.String() != want {
		t.Fatalf("stdout mismatch:\ngot  %q\nwant %q", stdout.String(), want)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls mismatch: got=%d want=2", provider.calls)
	}
}

func TestProtocolFailClosedAndStdoutProtocolOnly(t *testing.T) {
	provider := &sequenceProvider{
		statuses: []availability.AvailabilityStatus{{Ready: true, Reason: "ignored"}},
		errs:     []error{errors.New("boom")},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runProtocol(
		context.Background(),
		provider,
		"global",
		"qpu_ready",
		"",
		time.Second,
		strings.NewReader("\nquit\n"),
		&stdout,
		&stderr,
	)
	if err != nil {
		t.Fatalf("runProtocol returned error: %v", err)
	}
	if stdout.String() != "begin\nglobal:qpu_ready:0\nend\n" {
		t.Fatalf("stdout contains non-protocol output: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "availability provider failed") {
		t.Fatalf("stderr missing provider error: %q", stderr.String())
	}
}

func TestProtocolReportsDynamicSlots(t *testing.T) {
	slots := 5
	provider := &sequenceProvider{
		statuses: []availability.AvailabilityStatus{{Ready: true, Reason: "ok", SlotsAvailable: &slots}},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runProtocol(
		context.Background(),
		provider,
		"global",
		"qpu_ready",
		"qpu_slots",
		time.Second,
		strings.NewReader("\nquit\n"),
		&stdout,
		&stderr,
	)
	if err != nil {
		t.Fatalf("runProtocol returned error: %v", err)
	}
	want := "begin\nglobal:qpu_ready:1\nglobal:qpu_slots:5\nend\n"
	if stdout.String() != want {
		t.Fatalf("stdout mismatch:\ngot  %q\nwant %q", stdout.String(), want)
	}
}

func TestResolveConfigPath(t *testing.T) {
	t.Setenv("QRMI_OCS_LOAD_SENSOR_CONFIG", "/tmp/env.yaml")
	if got := resolveConfigPath(" /tmp/flag.yaml "); got != "/tmp/flag.yaml" {
		t.Fatalf("flag config path mismatch: got=%q", got)
	}
	if got := resolveConfigPath(""); got != "/tmp/env.yaml" {
		t.Fatalf("env config path mismatch: got=%q", got)
	}
	t.Setenv("QRMI_OCS_LOAD_SENSOR_CONFIG", "")
	if got := resolveConfigPath(""); got != defaultConfigPath {
		t.Fatalf("default config path mismatch: got=%q", got)
	}
}

func TestRunFailsClosedOnMalformedStartupConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "load-sensor.yaml")
	if err := os.WriteFile(path, []byte("load_sensor: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(
		[]string{"--config", path},
		strings.NewReader("\nquit\n"),
		&stdout,
		&stderr,
	)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if stdout.String() != "begin\nglobal:qpu_ready:0\nend\n" {
		t.Fatalf("stdout did not fail closed: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "load sensor startup failed") {
		t.Fatalf("stderr missing startup error: %q", stderr.String())
	}
}

func TestWriteReportClampsNegativeSlots(t *testing.T) {
	slots := -1
	var stdout bytes.Buffer
	err := writeReport(
		&stdout,
		"global",
		"qpu_ready",
		"qpu_slots",
		availability.AvailabilityStatus{Ready: true, SlotsAvailable: &slots},
	)
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "begin\nglobal:qpu_ready:1\nglobal:qpu_slots:0\nend\n" {
		t.Fatalf("negative slots were not clamped: %q", stdout.String())
	}
}
