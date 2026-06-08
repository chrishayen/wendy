package testkit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"pacp/internal/contracts"
)

func TestCheckComponentValidatesHealthAndMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer component-token" {
			writeTestErrorEnvelope(t, w, http.StatusUnauthorized, "unauthorized", "missing token")
			return
		}
		switch r.URL.Path {
		case "/v1/jobs/health":
			writeTestEnvelope(t, w, http.StatusOK, contracts.NewComponentHealth("jobs", nil))
		case "/v1/jobs/metrics":
			writeTestEnvelope(t, w, http.StatusOK, contracts.NewComponentMetrics("jobs", []contracts.MetricSample{}))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	report := CheckComponent(context.Background(), server.Client(), ComponentCheckOptions{
		BaseURL:    server.URL,
		Kind:       "jobs",
		Credential: "Bearer component-token",
	})

	if !report.Passed() || len(report.Checks) != 2 {
		t.Fatalf("report = %#v", report)
	}
}

func TestCheckComponentRejectsUnknownKind(t *testing.T) {
	report := CheckComponent(context.Background(), nil, ComponentCheckOptions{
		BaseURL: "http://component.invalid",
		Kind:    "unknown",
	})
	if report.Passed() || len(report.Checks) != 1 || report.Checks[0].Name != "component.kind" {
		t.Fatalf("report = %#v", report)
	}
}

func TestCheckComponentFailsOnWrongMetricComponent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/leases/health":
			writeTestEnvelope(t, w, http.StatusOK, contracts.NewComponentHealth("leases", nil))
		case "/v1/leases/metrics":
			writeTestEnvelope(t, w, http.StatusOK, contracts.NewComponentMetrics("jobs", []contracts.MetricSample{}))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	report := CheckComponent(context.Background(), server.Client(), ComponentCheckOptions{
		BaseURL: server.URL,
		Kind:    "leases",
	})
	if report.Passed() {
		t.Fatalf("report unexpectedly passed: %#v", report)
	}
	if report.Checks[1].Name != "component.metrics" || report.Checks[1].Error == "" {
		t.Fatalf("report = %#v", report)
	}
}
