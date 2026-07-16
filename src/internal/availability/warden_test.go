package availability

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWardenProviderAccessibleStates(t *testing.T) {
	for _, ready := range []bool{true, false} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"is_accessible":%t,"message":"ok","qpu_slots_available":5}`, ready)
		}))
		status, err := WardenProvider{BaseURL: server.URL, Endpoint: "/accessible", TLSVerify: true}.Status(context.Background())
		server.Close()
		if err != nil {
			t.Fatalf("Status returned error: %v", err)
		}
		if status.Ready != ready {
			t.Fatalf("Ready mismatch: got=%t want=%t", status.Ready, ready)
		}
		wantSlots := 5
		if !ready {
			wantSlots = 0
		}
		if status.SlotsAvailable == nil || *status.SlotsAvailable != wantSlots {
			t.Fatalf("SlotsAvailable mismatch: got=%v want=%d", status.SlotsAvailable, wantSlots)
		}
	}
}

func TestWardenProviderFailsClosedOnHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer server.Close()
	status, err := WardenProvider{BaseURL: server.URL, Endpoint: "/accessible", TLSVerify: true}.Status(context.Background())
	if err == nil {
		t.Fatal("expected HTTP error")
	}
	if status.Ready {
		t.Fatal("expected fail-closed unavailable state")
	}
}

func TestWardenProviderFailsClosedOnMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"message":"missing"}`)
	}))
	defer server.Close()
	status, err := WardenProvider{BaseURL: server.URL, Endpoint: "/accessible", TLSVerify: true}.Status(context.Background())
	if err == nil {
		t.Fatal("expected malformed response error")
	}
	if status.Ready {
		t.Fatal("expected fail-closed unavailable state")
	}
}

func TestWardenProviderFailsClosedOnTrailingJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"is_accessible":true}{"extra":true}`)
	}))
	defer server.Close()
	status, err := WardenProvider{BaseURL: server.URL, Endpoint: "/accessible", TLSVerify: true}.Status(context.Background())
	if err == nil || status.Ready {
		t.Fatalf("expected trailing JSON to fail closed: status=%+v err=%v", status, err)
	}
}

func TestWardenProviderFailsClosedOnNegativeSlots(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"is_accessible":true,"qpu_slots_available":-1}`)
	}))
	defer server.Close()
	status, err := WardenProvider{BaseURL: server.URL, Endpoint: "/accessible", TLSVerify: true}.Status(context.Background())
	if err == nil || status.Ready {
		t.Fatalf("expected negative slots to fail closed: status=%+v err=%v", status, err)
	}
}

func TestWardenProviderFailsClosedOnTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		fmt.Fprint(w, `{"is_accessible":true}`)
	}))
	defer server.Close()
	provider := WardenProvider{BaseURL: server.URL, Endpoint: "/accessible", TLSVerify: true}.WithTimeout(10 * time.Millisecond)
	status, err := provider.Status(context.Background())
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if status.Ready {
		t.Fatal("expected fail-closed unavailable state")
	}
}
