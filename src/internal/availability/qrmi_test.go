package availability

import (
	"testing"

	"github.com/hpc-gridware/qpu-resource/src/internal/qrmi"
)

func TestAvailabilityFromQRMI(t *testing.T) {
	yes, no, four := true, false, 4
	cases := []struct {
		name      string
		status    qrmi.ResourceStatus
		readSlots bool
		ready     bool
		slots     *int
	}{
		{"online without health or busy", qrmi.ResourceStatus{Online: true}, false, true, nil},
		{"online with slots", qrmi.ResourceStatus{Online: true, Busy: &no, AvailableSlots: &four}, true, true, &four},
		{"slots not read", qrmi.ResourceStatus{Online: true, AvailableSlots: &four}, false, true, nil},
		{"offline or paused", qrmi.ResourceStatus{Online: false, AvailableSlots: &four}, true, false, new(int)},
		{"unhealthy", qrmi.ResourceStatus{Online: true, Healthy: &no}, true, false, new(int)},
		{"busy", qrmi.ResourceStatus{Online: true, Healthy: &yes, Busy: &yes}, true, false, new(int)},
	}
	for _, tc := range cases {
		got := availabilityFromQRMI(tc.status, tc.readSlots)
		if got.Ready != tc.ready {
			t.Errorf("%s: Ready=%t, want %t", tc.name, got.Ready, tc.ready)
		}
		if (got.SlotsAvailable == nil) != (tc.slots == nil) || (got.SlotsAvailable != nil && *got.SlotsAvailable != *tc.slots) {
			t.Errorf("%s: SlotsAvailable=%v, want %v", tc.name, got.SlotsAvailable, tc.slots)
		}
	}
}

func TestQRMIProviderConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LoadSensor.Provider = "qrmi"
	if _, _, err := cfg.Provider(); err == nil {
		t.Fatal("expected an error without qrmi.resource")
	}
	cfg.QRMI.Resource = "PASQAL_LOCAL"
	cfg.LoadSensor.SlotsResourceName = "qpu_slots"
	provider, _, err := cfg.Provider()
	if err != nil {
		t.Fatalf("Provider returned error: %v", err)
	}
	p := provider.(*QRMIProvider)
	if p.ConfigPath != "/etc/qrmi/qrmi_config.json" || p.Resource != "PASQAL_LOCAL" || !p.ReadSlots {
		t.Fatalf("unexpected provider: %+v", p)
	}
}
