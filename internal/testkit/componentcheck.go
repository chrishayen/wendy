package testkit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"pacp/internal/contracts"
)

type ComponentCheckOptions struct {
	BaseURL    string
	Kind       string
	Credential string
}

type ComponentCheckReport struct {
	OK     bool                   `json:"ok"`
	Checks []ComponentCheckResult `json:"checks"`
}

type ComponentCheckResult struct {
	Name       string `json:"name"`
	OK         bool   `json:"ok"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Error      string `json:"error,omitempty"`
}

type componentContract struct {
	Kind        string
	HealthPath  string
	MetricsPath string
}

func (r ComponentCheckReport) Passed() bool {
	return r.OK
}

func CheckComponent(ctx context.Context, httpClient *http.Client, opts ComponentCheckOptions) ComponentCheckReport {
	report := ComponentCheckReport{OK: true}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		report.add(ComponentCheckResult{Name: "component.base_url", Error: "component URL is required"})
		return report
	}
	contract, ok := componentContractFor(opts.Kind)
	if !ok {
		report.add(ComponentCheckResult{Name: "component.kind", Error: "unsupported component kind: " + opts.Kind})
		return report
	}
	checkComponentHealth(ctx, httpClient, baseURL, opts.Credential, contract, &report)
	checkComponentMetrics(ctx, httpClient, baseURL, opts.Credential, contract, &report)
	return report
}

func componentContractFor(kind string) (componentContract, bool) {
	switch strings.TrimSpace(kind) {
	case "artifacts":
		return componentContract{Kind: "artifacts", HealthPath: "/v1/artifacts/health", MetricsPath: "/v1/artifacts/metrics"}, true
	case "catalog":
		return componentContract{Kind: "catalog", HealthPath: "/v1/catalog/health", MetricsPath: "/v1/catalog/metrics"}, true
	case "gateway":
		return componentContract{Kind: "gateway", HealthPath: "/v1/gateway/health", MetricsPath: "/v1/gateway/metrics"}, true
	case "jobs":
		return componentContract{Kind: "jobs", HealthPath: "/v1/jobs/health", MetricsPath: "/v1/jobs/metrics"}, true
	case "leases":
		return componentContract{Kind: "leases", HealthPath: "/v1/leases/health", MetricsPath: "/v1/leases/metrics"}, true
	case "node":
		return componentContract{Kind: "node", HealthPath: "/v1/node/health", MetricsPath: "/v1/node/metrics"}, true
	case "policy":
		return componentContract{Kind: "policy", HealthPath: "/v1/policy/health", MetricsPath: "/v1/policy/metrics"}, true
	case "runner":
		return componentContract{Kind: "runner", HealthPath: "/v1/runner/health", MetricsPath: "/v1/runner/metrics"}, true
	default:
		return componentContract{}, false
	}
}

func checkComponentHealth(ctx context.Context, httpClient *http.Client, baseURL, credential string, contract componentContract, report *ComponentCheckReport) {
	var envelope rawSuccessEnvelope
	status, err := getEnvelopeWithCredential(ctx, httpClient, joinURLPath(baseURL, contract.HealthPath), credential, &envelope)
	result := ComponentCheckResult{Name: "component.health", HTTPStatus: status}
	if err != nil {
		result.Error = err.Error()
		report.add(result)
		return
	}
	if status < 200 || status >= 300 {
		result.Error = fmt.Sprintf("HTTP %d", status)
		report.add(result)
		return
	}
	if !envelope.OK {
		result.Error = "health response was not ok"
		report.add(result)
		return
	}
	var health contracts.ComponentHealth
	if err := json.Unmarshal(envelope.Data, &health); err != nil {
		result.Error = "decode health: " + err.Error()
		report.add(result)
		return
	}
	if health.Status == "" {
		result.Error = "health status is required"
		report.add(result)
		return
	}
	if health.Version == "" {
		result.Error = "health version is required"
		report.add(result)
		return
	}
	if health.CheckedAt == "" {
		result.Error = "health checked_at is required"
		report.add(result)
		return
	}
	if component, _ := health.Details["component"].(string); component != contract.Kind {
		result.Error = fmt.Sprintf("health component = %q, want %q", component, contract.Kind)
		report.add(result)
		return
	}
	result.OK = true
	report.add(result)
}

func checkComponentMetrics(ctx context.Context, httpClient *http.Client, baseURL, credential string, contract componentContract, report *ComponentCheckReport) {
	var envelope rawSuccessEnvelope
	status, err := getEnvelopeWithCredential(ctx, httpClient, joinURLPath(baseURL, contract.MetricsPath), credential, &envelope)
	result := ComponentCheckResult{Name: "component.metrics", HTTPStatus: status}
	if err != nil {
		result.Error = err.Error()
		report.add(result)
		return
	}
	if status < 200 || status >= 300 {
		result.Error = fmt.Sprintf("HTTP %d", status)
		report.add(result)
		return
	}
	if !envelope.OK {
		result.Error = "metrics response was not ok"
		report.add(result)
		return
	}
	var metrics contracts.ComponentMetrics
	if err := json.Unmarshal(envelope.Data, &metrics); err != nil {
		result.Error = "decode metrics: " + err.Error()
		report.add(result)
		return
	}
	if metrics.Component != contract.Kind {
		result.Error = fmt.Sprintf("metrics component = %q, want %q", metrics.Component, contract.Kind)
		report.add(result)
		return
	}
	if metrics.Version == "" {
		result.Error = "metrics version is required"
		report.add(result)
		return
	}
	if metrics.CollectedAt == "" {
		result.Error = "metrics collected_at is required"
		report.add(result)
		return
	}
	if metrics.Samples == nil {
		result.Error = "metrics samples must be an array"
		report.add(result)
		return
	}
	result.OK = true
	report.add(result)
}

func getEnvelopeWithCredential(ctx context.Context, httpClient *http.Client, endpoint, credential string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	if credential != "" {
		req.Header.Set("Authorization", credential)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}

func (r *ComponentCheckReport) add(result ComponentCheckResult) {
	if !result.OK {
		r.OK = false
	}
	r.Checks = append(r.Checks, result)
}
