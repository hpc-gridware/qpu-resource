// Copyright 2026 Pasqal, HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/hpc-gridware/qpu-resource/src/internal/availability"
)

const defaultConfigPath = "/etc/qrmi-ocs-load-sensor.yaml"

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, in io.Reader, out io.Writer, errOut io.Writer) error {
	fs := flag.NewFlagSet("qrmi-ocs-load-sensor", flag.ContinueOnError)
	fs.SetOutput(errOut)
	configPath := fs.String("config", "", "Load Sensor YAML configuration path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	resolvedConfigPath := resolveConfigPath(*configPath)
	cfg, configErr := availability.LoadConfig(resolvedConfigPath)
	cfg.ApplyDefaults()
	provider, timeout, providerErr := cfg.Provider()
	if configErr != nil {
		providerErr = fmt.Errorf("load config: %w", configErr)
	}
	if !cfg.LoadSensor.Enabled {
		providerErr = fmt.Errorf("load sensor is disabled")
	}
	if providerErr != nil {
		fmt.Fprintf(errOut, "load sensor startup failed: %v\n", providerErr)
		provider = failedProvider{err: providerErr}
	}
	scope, resourceName, slotsResourceName := protocolFields(cfg)
	return runProtocol(
		context.Background(),
		provider,
		scope,
		resourceName,
		slotsResourceName,
		timeout,
		in,
		out,
		errOut,
	)
}

type failedProvider struct {
	err error
}

func (p failedProvider) Status(context.Context) (availability.AvailabilityStatus, error) {
	return availability.AvailabilityStatus{Ready: false, Reason: "startup failure"}, p.err
}

func protocolFields(cfg availability.Config) (string, string, string) {
	scope := cfg.LoadSensor.Scope
	if !validProtocolField(scope) {
		scope = "global"
	}
	resourceName := cfg.LoadSensor.ResourceName
	if !validProtocolField(resourceName) {
		resourceName = "qpu_ready"
	}
	slotsResourceName := cfg.LoadSensor.SlotsResourceName
	if slotsResourceName != "" && !validProtocolField(slotsResourceName) {
		slotsResourceName = ""
	}
	return scope, resourceName, slotsResourceName
}

func validProtocolField(value string) bool {
	return value != "" && !strings.ContainsAny(value, ": \t\r\n")
}

func resolveConfigPath(configPath string) string {
	if strings.TrimSpace(configPath) != "" {
		return strings.TrimSpace(configPath)
	}
	if envPath := strings.TrimSpace(os.Getenv("QRMI_OCS_LOAD_SENSOR_CONFIG")); envPath != "" {
		return envPath
	}
	return defaultConfigPath
}

func runProtocol(
	ctx context.Context,
	provider availability.AvailabilityProvider,
	scope string,
	resourceName string,
	slotsResourceName string,
	timeout time.Duration,
	in io.Reader,
	out io.Writer,
	errOut io.Writer,
) error {
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "quit" {
			return nil
		}
		cycleCtx, cancel := context.WithTimeout(ctx, timeout)
		status, err := provider.Status(cycleCtx)
		cancel()
		if err != nil {
			fmt.Fprintf(errOut, "availability provider failed: %v\n", err)
			status = availability.AvailabilityStatus{Ready: false, Reason: "provider error"}
		}
		if status.Reason != "" {
			fmt.Fprintf(errOut, "availability status: ready=%t reason=%s\n", status.Ready, status.Reason)
		}
		if err := writeReport(out, scope, resourceName, slotsResourceName, status); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func writeReport(out io.Writer, scope, resourceName, slotsResourceName string, status availability.AvailabilityStatus) error {
	value := "0"
	if status.Ready {
		value = "1"
	}
	if _, err := fmt.Fprintf(out, "begin\n%s:%s:%s\n", scope, resourceName, value); err != nil {
		return err
	}
	if slotsResourceName != "" {
		slots := 0
		if status.SlotsAvailable != nil {
			slots = *status.SlotsAvailable
		}
		if slots < 0 {
			slots = 0
		}
		if _, err := fmt.Fprintf(out, "%s:%s:%d\n", scope, slotsResourceName, slots); err != nil {
			return err
		}
	}
	_, err := fmt.Fprint(out, "end\n")
	return err
}
