package availability

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type WardenProvider struct {
	BaseURL   string
	Endpoint  string
	TLSVerify bool
	Client    *http.Client
}

func (p WardenProvider) Status(ctx context.Context) (AvailabilityStatus, error) {
	client := p.Client
	if client == nil {
		client = p.httpClient(0)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.accessibleURL(), nil)
	if err != nil {
		return AvailabilityStatus{Ready: false, Reason: "invalid warden URL"}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return AvailabilityStatus{Ready: false, Reason: "warden request failed"}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return AvailabilityStatus{Ready: false, Reason: "warden returned HTTP error"}, fmt.Errorf("warden returned status %s", resp.Status)
	}
	var body struct {
		IsAccessible      *bool `json:"is_accessible"`
		QPUSlotsAvailable *int  `json:"qpu_slots_available"`
	}
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&body); err != nil {
		return AvailabilityStatus{Ready: false, Reason: "warden response malformed"}, err
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return AvailabilityStatus{Ready: false, Reason: "warden response malformed"}, err
	}
	if body.IsAccessible == nil {
		return AvailabilityStatus{Ready: false, Reason: "warden response missing is_accessible"}, fmt.Errorf("missing is_accessible")
	}
	if !*body.IsAccessible {
		slots := 0
		return AvailabilityStatus{Ready: false, Reason: "warden reports unavailable", SlotsAvailable: &slots}, nil
	}
	if body.QPUSlotsAvailable != nil && *body.QPUSlotsAvailable < 0 {
		return AvailabilityStatus{Ready: false, Reason: "warden response has invalid slots"}, fmt.Errorf("negative qpu_slots_available")
	}
	return AvailabilityStatus{Ready: true, Reason: "warden reports accessible", SlotsAvailable: body.QPUSlotsAvailable}, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func (p WardenProvider) WithTimeout(timeout time.Duration) WardenProvider {
	p.Client = p.httpClient(timeout)
	return p
}

func (p WardenProvider) httpClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if !p.TLSVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &http.Client{Transport: transport, Timeout: timeout}
}

func (p WardenProvider) accessibleURL() string {
	base := strings.TrimRight(p.BaseURL, "/")
	endpoint := "/" + strings.TrimLeft(p.Endpoint, "/")
	u, err := url.JoinPath(base, endpoint)
	if err != nil {
		return base + endpoint
	}
	return u
}
