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
		case "/v1/jobs":
			writeTestEnvelope(t, w, http.StatusOK, map[string]any{"items": []any{}, "next_cursor": nil})
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

	if !report.Passed() || len(report.Checks) != 3 {
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
		case "/v1/resources":
			writeTestEnvelope(t, w, http.StatusOK, map[string]any{"items": []any{}, "next_cursor": nil})
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

func TestCheckComponentFailsOnMalformedListSurface(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/artifacts/health":
			writeTestEnvelope(t, w, http.StatusOK, contracts.NewComponentHealth("artifacts", nil))
		case "/v1/artifacts/metrics":
			writeTestEnvelope(t, w, http.StatusOK, contracts.NewComponentMetrics("artifacts", []contracts.MetricSample{}))
		case "/v1/artifacts":
			writeTestEnvelope(t, w, http.StatusOK, map[string]any{"items": nil, "next_cursor": nil})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	report := CheckComponent(context.Background(), server.Client(), ComponentCheckOptions{
		BaseURL: server.URL,
		Kind:    "artifacts",
	})
	if report.Passed() {
		t.Fatalf("report unexpectedly passed: %#v", report)
	}
	last := report.Checks[len(report.Checks)-1]
	if last.Name != "component.surface.artifacts.list" || last.Error == "" {
		t.Fatalf("report = %#v", report)
	}
}
