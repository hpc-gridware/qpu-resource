// Copyright 2026 Pasqal, HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

package availability

import (
	"context"
	"fmt"

	"github.com/hpc-gridware/qpu-resource/src/internal/qrmi"
)

// QRMIProvider reports a resource from qrmi_config.json through QRMI status(),
// for any vendor. It needs a build with -tags qrmi. The QRMI call cannot be
// cancelled, so the cycle timeout does not apply to it.
type QRMIProvider struct {
	ConfigPath string
	Resource   string
	ReadSlots  bool
	resource   *qrmi.Resource
}

func (p *QRMIProvider) Status(context.Context) (AvailabilityStatus, error) {
	if p.resource == nil {
		resource, err := newQRMIResource(p.ConfigPath, p.Resource)
		if err != nil {
			return AvailabilityStatus{Ready: false, Reason: "qrmi resource unavailable"}, err
		}
		p.resource = resource
	}
	status, err := p.resource.Status()
	if err != nil {
		return AvailabilityStatus{Ready: false, Reason: "qrmi status failed"}, err
	}
	return availabilityFromQRMI(status, p.ReadSlots), nil
}

func newQRMIResource(configPath, name string) (*qrmi.Resource, error) {
	cfg, err := qrmi.LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	defer cfg.Close()
	def, err := cfg.ResourceDef(name)
	if err != nil {
		return nil, fmt.Errorf("resource %q not in %s: %w", name, configPath, err)
	}
	defer def.Close()
	return qrmi.NewResourceFromDef(name, def)
}

// availabilityFromQRMI applies the rule of the Slurm license poller: a resource
// is ready only if it is online and does not report itself as unhealthy or
// busy. A resource that is not ready has no free slots.
func availabilityFromQRMI(status qrmi.ResourceStatus, readSlots bool) AvailabilityStatus {
	if !status.Online || (status.Healthy != nil && !*status.Healthy) || (status.Busy != nil && *status.Busy) {
		slots := 0
		return AvailabilityStatus{Ready: false, Reason: "qrmi reports not ready", SlotsAvailable: &slots}
	}
	result := AvailabilityStatus{Ready: true, Reason: "qrmi reports ready"}
	if readSlots {
		result.SlotsAvailable = status.AvailableSlots
	}
	return result
}
