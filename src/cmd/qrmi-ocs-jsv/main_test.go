package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "qrmi_config.json")
	cfg := `{"resources":[{"name":"EMU_FREE","type":"pasqal-cloud"},{"name":"PASQAL_LOCAL","type":"pasqal-local"}]}`
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVerify(t *testing.T) {
	cfg := writeConfig(t)
	cases := []struct {
		name   string
		params map[string]string
		slots  string
		want   []string
	}{
		{"no qpu", map[string]string{"l_hard": "h_rt=60"}, "qpu_slots", []string{"RESULT STATE ACCEPT"}},
		{"slots present", map[string]string{"l_hard": "qpu=PASQAL_LOCAL,qpu_slots=2"}, "qpu_slots", []string{"RESULT STATE ACCEPT"}},
		{"slots added", map[string]string{"l_hard": "qpu=PASQAL_LOCAL,h_rt=60"}, "qpu_slots", []string{
			"PARAM l_hard h_rt=60,qpu=PASQAL_LOCAL,qpu_slots=1",
			"RESULT STATE CORRECT added qpu_slots=1",
		}},
		{"slots disabled", map[string]string{"l_hard": "qpu=EMU_FREE"}, "", []string{"RESULT STATE ACCEPT"}},
		{"soft qpu", map[string]string{"l_soft": "qpu=EMU_FREE"}, "qpu_slots", []string{"RESULT STATE REJECT qpu must be a hard resource request"}},
		{"wildcard", map[string]string{"l_hard": "qpu=*"}, "qpu_slots", []string{"RESULT STATE REJECT qpu=* must name exactly one backend"}},
		{"alternatives", map[string]string{"l_hard": "qpu=EMU_FREE|PASQAL_LOCAL"}, "qpu_slots", []string{"RESULT STATE REJECT qpu=EMU_FREE|PASQAL_LOCAL must name exactly one backend"}},
		{"unknown", map[string]string{"l_hard": "qpu=TYPO"}, "qpu_slots", []string{"RESULT STATE REJECT qpu=TYPO is not in " + cfg}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := verify(tc.params, cfg, tc.slots)
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestVerifyMissingConfigRejectsWait(t *testing.T) {
	got := verify(map[string]string{"l_hard": "qpu=EMU_FREE"}, filepath.Join(t.TempDir(), "missing.json"), "qpu_slots")
	if len(got) != 1 || !strings.HasPrefix(got[0], "RESULT STATE REJECT_WAIT ") {
		t.Fatalf("got %q", got)
	}
}

func TestProtocolResetsParamsPerJob(t *testing.T) {
	in := strings.NewReader("START\nPARAM l_hard qpu=TYPO\nBEGIN\nSTART\nPARAM CLIENT qsub\nBEGIN\nQUIT\nSTART\n")
	var out bytes.Buffer
	if err := runProtocol(in, &out, writeConfig(t), "qpu_slots"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 || lines[0] != "STARTED" || !strings.HasPrefix(lines[1], "RESULT STATE REJECT ") ||
		lines[2] != "STARTED" || lines[3] != "RESULT STATE ACCEPT" {
		t.Fatalf("unexpected protocol output:\n%s", out.String())
	}
}
