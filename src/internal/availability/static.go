package availability

import (
	"context"
	"fmt"
	"os"
	"strings"
)

type StaticProvider struct {
	Ready     bool
	StateFile string
}

func (p StaticProvider) Status(context.Context) (AvailabilityStatus, error) {
	if strings.TrimSpace(p.StateFile) == "" {
		return AvailabilityStatus{Ready: p.Ready, Reason: "configured static state"}, nil
	}
	raw, err := os.ReadFile(p.StateFile)
	if err != nil {
		return AvailabilityStatus{Ready: false, Reason: "state file unavailable"}, err
	}
	switch strings.TrimSpace(string(raw)) {
	case "1":
		return AvailabilityStatus{Ready: true, Reason: "state file ready"}, nil
	case "0":
		return AvailabilityStatus{Ready: false, Reason: "state file unavailable"}, nil
	default:
		return AvailabilityStatus{Ready: false, Reason: "state file malformed"}, fmt.Errorf("invalid state file value %q", strings.TrimSpace(string(raw)))
	}
}
