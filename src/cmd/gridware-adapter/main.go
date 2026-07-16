// Copyright 2026 Pasqal, HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	qconf "github.com/hpc-gridware/go-clusterscheduler/pkg/qconf/v9.0"
)

const defaultReportingPattern = "usage_patterns=qrmi:qrmi_*"
const defaultResourceName = "qpu"
const defaultSlotsResourceName = "qpu_slots"
const defaultSlotsScope = "host"

func main() {
	if len(os.Args) < 2 {
		printRootUsage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "setup-qrmi-support":
		err = runSetupQRMISupport(os.Args[2:])
	case "ensure-resource":
		err = runEnsureResource(os.Args[2:])
	case "configure-queue-hooks":
		err = runConfigureQueueHooks(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		printRootUsage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func printRootUsage() {
	fmt.Println("Gridware Cluster Scheduler (GCS) / Open Cluster Scheduler (OCS) QRMI adapter")
	fmt.Println("")
	fmt.Println("Usage:")
	fmt.Println("  adapter setup-qrmi-support [flags]")
	fmt.Println("  adapter ensure-resource [flags]")
	fmt.Println("  adapter configure-queue-hooks [flags]")
	fmt.Println("")
	fmt.Println("Subcommands:")
	fmt.Println("  setup-qrmi-support  Ensure resource, host mapping, queue hooks, and reporting params")
	fmt.Println("  ensure-resource  Ensure qpu complex entry and host complex_values")
	fmt.Println("  configure-queue-hooks  Configure queue prolog/epilog and reporting params")
}

func runEnsureResource(args []string) error {
	fs := flag.NewFlagSet("ensure-resource", flag.ContinueOnError)
	qconfPath := fs.String("qconf", "qconf", "Path to qconf executable")
	dryRun := fs.Bool("dry-run", false, "Print qconf operations without changing scheduler state")

	hostsCSV := fs.String("hosts", "", "Comma-separated execution hosts to update")
	hostValue := fs.String("host-value", "", "Single backend name assigned to each host's qpu complex value")
	enableSlots := fs.Bool("enable-qpu-slots", false, "Create qpu_slots and optionally seed host capacity")
	slotsName := fs.String("qpu-slots-name", defaultSlotsResourceName, "QPU slots complex name")
	slotsCapacity := fs.Int("qpu-slots-capacity", 1, "QPU slots capacity to set on each host; 0 clears host capacity")
	slotsScope := fs.String("qpu-slots-scope", defaultSlotsScope, "QPU slots capacity scope: host or global")

	fs.Usage = func() {
		fmt.Println("Usage: adapter ensure-resource [flags]")
		fmt.Println("")
		fmt.Println("Examples:")
		fmt.Println("  adapter ensure-resource --host-value EMU_FREE --hosts ocs-master,ocs-worker1,ocs-worker2")
		fmt.Println("  adapter ensure-resource --host-value EMU_FREE --hosts ocs-worker1")
		fmt.Println("  adapter ensure-resource --host-value EMU_FREE --hosts ocs-master,ocs-worker1")
		fmt.Println("")
		fmt.Println("Notes:")
		fmt.Println("  --host-value is required.")
		fmt.Println("")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}
	resolvedHostValue, err := parseSingleBackendName(*hostValue)
	if err != nil {
		return err
	}

	qc, err := qconf.NewCommandLineQConf(qconf.CommandLineQConfConfig{
		Executable: *qconfPath,
		DryRun:     *dryRun,
	})
	if err != nil {
		return fmt.Errorf("create qconf client: %w", err)
	}

	opts, err := resourceOptionsFromFlags(
		*enableSlots,
		*slotsName,
		*slotsCapacity,
		*slotsScope,
	)
	if err != nil {
		return err
	}
	return ensureResourceDefault(qc, parseCSV(*hostsCSV), resolvedHostValue, opts)
}

func runSetupQRMISupport(args []string) error {
	fs := flag.NewFlagSet("setup-qrmi-support", flag.ContinueOnError)
	qconfPath := fs.String("qconf", "qconf", "Path to qconf executable")
	dryRun := fs.Bool("dry-run", false, "Print qconf operations without changing scheduler state")

	hostsCSV := fs.String("hosts", "", "Comma-separated execution hosts to update (required)")
	hostValue := fs.String("host-value", "", "Single backend name assigned to each host's qpu complex value (required)")
	enableSlots := fs.Bool("enable-qpu-slots", false, "Create qpu_slots and optionally seed host capacity")
	slotsName := fs.String("qpu-slots-name", defaultSlotsResourceName, "QPU slots complex name")
	slotsCapacity := fs.Int("qpu-slots-capacity", 1, "QPU slots capacity to set on each host; 0 clears host capacity")
	slotsScope := fs.String("qpu-slots-scope", defaultSlotsScope, "QPU slots capacity scope: host or global")

	queue := fs.String("queue", "", "Cluster queue name (required)")
	prolog := fs.String("prolog", "", "Queue prolog path, use NONE to disable")
	epilog := fs.String("epilog", "", "Queue epilog path, use NONE to disable")

	fs.Usage = func() {
		fmt.Println("Usage: adapter setup-qrmi-support [flags]")
		fmt.Println("")
		fmt.Println("Example:")
		fmt.Println("  adapter setup-qrmi-support \\")
		fmt.Println("    --hosts ocs-master,ocs-worker1,ocs-worker2 \\")
		fmt.Println("    --host-value EMU_FREE \\")
		fmt.Println("    --queue all.q \\")
		fmt.Println("    --prolog /shared/gridware-adapter/bin/qrmi-ocs-prolog \\")
		fmt.Println("    --epilog /shared/gridware-adapter/bin/qrmi-ocs-epilog")
		fmt.Println("")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	hosts := parseCSV(*hostsCSV)
	if len(hosts) == 0 {
		return errors.New("--hosts is required")
	}
	resolvedHostValue, err := parseSingleBackendName(*hostValue)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*queue) == "" {
		return errors.New("--queue is required")
	}
	if strings.TrimSpace(*prolog) == "" {
		return errors.New("--prolog is required")
	}
	if strings.TrimSpace(*epilog) == "" {
		return errors.New("--epilog is required")
	}

	qc, err := qconf.NewCommandLineQConf(qconf.CommandLineQConfConfig{
		Executable: *qconfPath,
		DryRun:     *dryRun,
	})
	if err != nil {
		return fmt.Errorf("create qconf client: %w", err)
	}

	opts, err := resourceOptionsFromFlags(
		*enableSlots,
		*slotsName,
		*slotsCapacity,
		*slotsScope,
	)
	if err != nil {
		return err
	}
	if err := ensureResourceDefault(qc, hosts, resolvedHostValue, opts); err != nil {
		return err
	}
	return configureQueueHooksDefault(qc, strings.TrimSpace(*queue), strings.TrimSpace(*prolog), strings.TrimSpace(*epilog))
}

type resourceOptions struct {
	enableSlots   bool
	slotsName     string
	slotsCapacity int
	slotsScope    string
}

func resourceOptionsFromFlags(
	enableSlots bool,
	slotsName string,
	slotsCapacity int,
	slotsScope string,
) (resourceOptions, error) {
	opts := resourceOptions{
		enableSlots:   enableSlots,
		slotsName:     strings.TrimSpace(slotsName),
		slotsCapacity: slotsCapacity,
		slotsScope:    strings.ToLower(strings.TrimSpace(slotsScope)),
	}
	if opts.enableSlots && (opts.slotsName == "" || opts.slotsCapacity < 0) {
		return opts, errors.New("--qpu-slots-name cannot be empty and --qpu-slots-capacity cannot be negative")
	}
	if opts.enableSlots && opts.slotsScope != "host" && opts.slotsScope != "global" {
		return opts, errors.New("--qpu-slots-scope must be host or global")
	}
	return opts, nil
}

func ensureResourceDefault(qc qconf.QConf, hosts []string, resolvedHostValue string, opts resourceOptions) error {
	changed, err := ensureComplexEntry(qc, qpuComplexEntry())
	if err != nil {
		return err
	}
	if changed {
		fmt.Printf("ensured complex entry %q\n", defaultResourceName)
	} else {
		fmt.Printf("complex entry %q already matches requested settings\n", defaultResourceName)
	}

	if opts.enableSlots {
		if _, err := ensureComplexEntry(qc, qpuSlotsComplexEntry(opts.slotsName)); err != nil {
			return err
		}
		fmt.Printf("ensured complex entry %q\n", opts.slotsName)
	}
	if opts.enableSlots && opts.slotsScope == "global" {
		if opts.slotsCapacity > 0 {
			if err := setExecHostResource(qc, "global", opts.slotsName, fmt.Sprint(opts.slotsCapacity)); err != nil {
				return err
			}
		} else if err := clearExecHostResource(qc, "global", opts.slotsName); err != nil {
			return err
		}
	}

	if len(hosts) == 0 {
		fmt.Println("no hosts provided; skipped exechost complex_values update")
		return nil
	}

	for _, host := range hosts {
		if err := setExecHostResource(qc, host, defaultResourceName, resolvedHostValue); err != nil {
			return err
		}
		if opts.enableSlots && opts.slotsScope == "host" {
			if opts.slotsCapacity > 0 {
				if err := setExecHostResource(qc, host, opts.slotsName, fmt.Sprint(opts.slotsCapacity)); err != nil {
					return err
				}
			} else if err := clearExecHostResource(qc, host, opts.slotsName); err != nil {
				return err
			}
		}
		fmt.Printf("set %s=%s on exechost %s\n", defaultResourceName, resolvedHostValue, host)
	}
	return nil
}

// ensureComplexEntry enforces the scheduler-side qpu resource shape and updates
// the complex entry when it drifts from the expected model.
func ensureComplexEntry(
	qc qconf.QConf,
	desired qconf.ComplexEntryConfig,
) (bool, error) {
	current, err := qc.ShowComplexEntry(desired.Name)
	if err != nil {
		if addErr := qc.AddComplexEntry(desired); addErr != nil {
			return false, fmt.Errorf("add complex entry %q: %w (original show error: %v)", desired.Name, addErr, err)
		}
		return true, nil
	}

	if complexEntryMatches(current, desired) {
		return false, nil
	}

	current.Shortcut = desired.Shortcut
	current.Type = desired.Type
	current.Relop = desired.Relop
	current.Requestable = desired.Requestable
	current.Consumable = desired.Consumable
	current.Default = desired.Default
	current.Urgency = desired.Urgency

	if err := qc.ModifyComplexEntry(desired.Name, current); err != nil {
		return false, fmt.Errorf("modify complex entry %q: %w", desired.Name, err)
	}
	return true, nil
}

func qpuComplexEntry() qconf.ComplexEntryConfig {
	return qconf.ComplexEntryConfig{
		Name:        defaultResourceName,
		Shortcut:    defaultResourceName,
		Type:        qconf.ResourceTypeString,
		Relop:       "==",
		Requestable: "YES",
		Consumable:  qconf.ConsumableNO,
		Default:     "NONE",
		Urgency:     1000,
	}
}

func qpuSlotsComplexEntry(name string) qconf.ComplexEntryConfig {
	return qconf.ComplexEntryConfig{
		Name:        name,
		Shortcut:    name,
		Type:        qconf.ResourceTypeInt,
		Relop:       "<=",
		Requestable: "YES",
		Consumable:  qconf.ConsumableJOB,
		Default:     "0",
		Urgency:     0,
	}
}

func complexEntryMatches(current, desired qconf.ComplexEntryConfig) bool {
	return current.Shortcut == desired.Shortcut &&
		current.Type == desired.Type &&
		current.Relop == desired.Relop &&
		strings.EqualFold(current.Requestable, desired.Requestable) &&
		strings.EqualFold(current.Consumable, desired.Consumable) &&
		current.Default == desired.Default &&
		current.Urgency == desired.Urgency
}

func setExecHostResource(qc qconf.QConf, host, resourceName, resourceValue string) error {
	hostCfg, err := qc.ShowExecHost(host)
	if err != nil {
		return fmt.Errorf("show exechost %q: %w", host, err)
	}
	if hostCfg.ComplexValues == nil {
		hostCfg.ComplexValues = map[string]string{}
	}
	if hostCfg.ComplexValues[resourceName] == resourceValue {
		return nil
	}
	hostCfg.Name = host
	hostCfg.ComplexValues[resourceName] = resourceValue
	if err := qc.ModifyExecHost(host, hostCfg); err != nil {
		return fmt.Errorf("modify exechost %q: %w", host, err)
	}
	return nil
}

func clearExecHostResource(qc qconf.QConf, host, resourceName string) error {
	hostCfg, err := qc.ShowExecHost(host)
	if err != nil {
		return fmt.Errorf("show exechost %q: %w", host, err)
	}
	if _, ok := hostCfg.ComplexValues[resourceName]; !ok {
		return nil
	}
	hostCfg.Name = host
	delete(hostCfg.ComplexValues, resourceName)
	if err := qc.ModifyExecHost(host, hostCfg); err != nil {
		return fmt.Errorf("modify exechost %q: %w", host, err)
	}
	return nil
}

func runConfigureQueueHooks(args []string) error {
	fs := flag.NewFlagSet("configure-queue-hooks", flag.ContinueOnError)
	qconfPath := fs.String("qconf", "qconf", "Path to qconf executable")
	dryRun := fs.Bool("dry-run", false, "Print qconf operations without changing scheduler state")

	queue := fs.String("queue", "", "Cluster queue name (required)")
	prolog := fs.String("prolog", "", "Queue prolog path, use NONE to disable")
	epilog := fs.String("epilog", "", "Queue epilog path, use NONE to disable")

	fs.Usage = func() {
		fmt.Println("Usage: adapter configure-queue-hooks [flags]")
		fmt.Println("")
		fmt.Println("Example:")
		fmt.Println("  adapter configure-queue-hooks \\")
		fmt.Println("    --queue all.q \\")
		fmt.Println("    --prolog /shared/gridware-adapter/bin/qrmi-ocs-prolog \\")
		fmt.Println("    --epilog /shared/gridware-adapter/bin/qrmi-ocs-epilog")
		fmt.Println("")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*queue) == "" {
		return errors.New("--queue is required")
	}
	if strings.TrimSpace(*prolog) == "" {
		return errors.New("--prolog is required")
	}
	if strings.TrimSpace(*epilog) == "" {
		return errors.New("--epilog is required")
	}

	qc, err := qconf.NewCommandLineQConf(qconf.CommandLineQConfConfig{
		Executable: *qconfPath,
		DryRun:     *dryRun,
	})
	if err != nil {
		return fmt.Errorf("create qconf client: %w", err)
	}

	return configureQueueHooksDefault(qc, *queue, *prolog, *epilog)
}

func configureQueueHooksDefault(qc qconf.QConf, queue, prolog, epilog string) error {
	if err := configureQueueHookPaths(qc, queue, prolog, epilog); err != nil {
		return err
	}
	if err := configureGlobalQRMIReporting(qc); err != nil {
		return err
	}
	return nil
}

func configureQueueHookPaths(qc qconf.QConf, queue, prolog, epilog string) error {
	queueCfg, err := qc.ShowClusterQueue(queue)
	if err != nil {
		return fmt.Errorf("show queue %q: %w", queue, err)
	}
	queueCfg.Name = queue
	queueCfg.Prolog = []string{strings.TrimSpace(prolog)}
	queueCfg.Epilog = []string{strings.TrimSpace(epilog)}

	if err := qc.ModifyClusterQueue(queue, queueCfg); err != nil {
		return fmt.Errorf("modify queue %q hooks: %w", queue, err)
	}
	fmt.Printf("updated queue hooks on %q: prolog=%q epilog=%q\n", queue, prolog, epilog)
	return nil
}

func configureGlobalQRMIReporting(qc qconf.QConf) error {
	if err := ensureGlobalReportingPattern(qc, defaultReportingPattern); err != nil {
		return err
	}
	fmt.Printf("ensured global reporting_params contains %q\n", defaultReportingPattern)
	return nil
}

// ensureGlobalReportingPattern appends a reporting token to
// global_config.reporting_params when it is not already present.
func ensureGlobalReportingPattern(qc qconf.QConf, pattern string) error {
	globalCfg, err := qc.ShowGlobalConfiguration()
	if err != nil {
		return fmt.Errorf("show global configuration: %w", err)
	}
	if globalCfg == nil {
		return errors.New("show global configuration returned nil")
	}
	for _, token := range globalCfg.ReportingParams {
		if token == pattern {
			return nil
		}
	}
	globalCfg.ReportingParams = append(globalCfg.ReportingParams, pattern)
	if err := qc.ModifyGlobalConfig(*globalCfg); err != nil {
		return fmt.Errorf("modify global reporting_params: %w", err)
	}
	return nil
}

func parseCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func parseSingleBackendName(raw string) (string, error) {
	backend := strings.TrimSpace(raw)
	if backend == "" {
		return "", errors.New("--host-value cannot be empty")
	}
	if strings.Contains(backend, ",") {
		return "", fmt.Errorf("invalid --host-value %q: only one backend name is allowed", raw)
	}
	if strings.ContainsAny(backend, " \t\r\n") {
		return "", fmt.Errorf("invalid --host-value %q: whitespace is not allowed", raw)
	}
	return backend, nil
}
