package testkit

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFakeComponentHandlersPassComponentChecks(t *testing.T) {
	kinds := []string{"artifacts", "catalog", "gateway", "jobs", "leases", "node", "policy", "runner"}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			handler, err := NewFakeComponentHandler(FakeComponentConfig{
				Kind: kind,
				Now:  fixedFakeClock,
			})
			if err != nil {
				t.Fatalf("new fake component: %v", err)
			}
			server := httptest.NewServer(handler)
			defer server.Close()

			report := CheckComponent(context.Background(), server.Client(), ComponentCheckOptions{
				BaseURL:   server.URL,
				Kind:      kind,
				RequestID: "req_fake_component",
			})
			if !report.Passed() {
				t.Fatalf("report = %#v", report)
			}
		})
	}
}

func TestFakeComponentHandlerRequiresCredential(t *testing.T) {
	handler, err := NewFakeComponentHandler(FakeComponentConfig{
		Kind:       "jobs",
		Credential: "component-token",
		Now:        fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake component: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	denied := CheckComponent(context.Background(), server.Client(), ComponentCheckOptions{
		BaseURL:   server.URL,
		Kind:      "jobs",
		RequestID: "req_fake_component",
	})
	if denied.Passed() {
		t.Fatalf("unauthenticated check unexpectedly passed: %#v", denied)
	}

	allowed := CheckComponent(context.Background(), server.Client(), ComponentCheckOptions{
		BaseURL:    server.URL,
		Kind:       "jobs",
		Credential: "Bearer component-token",
		RequestID:  "req_fake_component",
	})
	if !allowed.Passed() {
		t.Fatalf("authenticated check failed: %#v", allowed)
	}
}

func TestFakeProviderHandlerPassesProviderCheck(t *testing.T) {
	handler, err := NewFakeProviderHandler(FakeProviderConfig{
		Endpoint:   "http://provider.fake",
		Credential: "provider-token",
		Now:        fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake provider: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	report := CheckProvider(context.Background(), server.Client(), ProviderCheckOptions{
		BaseURL:      server.URL,
		CapabilityID: "cap_echo",
		Input:        map[string]any{"message": "hello"},
		Credential:   "Bearer provider-token",
		RequestID:    "req_fake_provider",
	})
	if !report.Passed() {
		t.Fatalf("report = %#v", report)
	}
}

func TestFakeProviderHandlerRequiresCredential(t *testing.T) {
	handler, err := NewFakeProviderHandler(FakeProviderConfig{
		Endpoint:   "http://provider.fake",
		Credential: "provider-token",
		Now:        fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake provider: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	report := CheckProvider(context.Background(), server.Client(), ProviderCheckOptions{
		BaseURL:   server.URL,
		RequestID: "req_fake_provider",
	})
	if report.Passed() {
		t.Fatalf("unauthenticated provider check unexpectedly passed: %#v", report)
	}
}

func fixedFakeClock() time.Time {
	return time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)
}
