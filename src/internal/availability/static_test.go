package availability

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestStaticProviderConfiguredStates(t *testing.T) {
	for _, ready := range []bool{true, false} {
		status, err := (StaticProvider{Ready: ready}).Status(context.Background())
		if err != nil {
			t.Fatalf("Status returned error: %v", err)
		}
		if status.Ready != ready {
			t.Fatalf("Ready mismatch: got=%t want=%t", status.Ready, ready)
		}
	}
}

func TestStaticProviderStateFileChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready")
	provider := StaticProvider{StateFile: path}
	if err := os.WriteFile(path, []byte("0\n"), 0o644); err != nil {
		t.Fatalf("write state file failed: %v", err)
	}
	status, err := provider.Status(context.Background())
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if status.Ready {
		t.Fatal("expected unavailable state")
	}
	if err := os.WriteFile(path, []byte("1\n"), 0o644); err != nil {
		t.Fatalf("write state file failed: %v", err)
	}
	status, err = provider.Status(context.Background())
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if !status.Ready {
		t.Fatal("expected ready state")
	}
}

func TestStaticProviderMalformedStateFileFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready")
	if err := os.WriteFile(path, []byte("yes\n"), 0o644); err != nil {
		t.Fatalf("write state file failed: %v", err)
	}
	status, err := (StaticProvider{StateFile: path}).Status(context.Background())
	if err == nil {
		t.Fatal("expected malformed state error")
	}
	if status.Ready {
		t.Fatal("expected fail-closed unavailable state")
	}
}
