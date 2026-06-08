package testkit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"pacp/internal/contracts"
	"pacp/internal/observability"
)

type ComponentCheckOptions struct {
	BaseURL    string
	Kind       string
	Credential string
	RequestID  string
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
	ListChecks  []componentListCheck
}

type componentListCheck struct {
	Name         string
	Path         string
	ValidateItem func(json.RawMessage) error
}

func (r ComponentCheckReport) Passed() bool {
	return r.OK
}

func CheckComponent(ctx context.Context, httpClient *http.Client, opts ComponentCheckOptions) ComponentCheckReport {
	report := ComponentCheckReport{OK: true}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if requestID := strings.TrimSpace(opts.RequestID); requestID != "" {
		ctx = observability.WithRequestID(ctx, requestID)
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
	for _, check := range contract.ListChecks {
		checkComponentListEndpoint(ctx, httpClient, baseURL, opts.Credential, check, &report)
	}
	return report
}

func componentContractFor(kind string) (componentContract, bool) {
	switch strings.TrimSpace(kind) {
	case "artifacts":
		return componentContract{
			Kind:        "artifacts",
			HealthPath:  "/v1/artifacts/health",
			MetricsPath: "/v1/artifacts/metrics",
			ListChecks:  []componentListCheck{{Name: "component.surface.artifacts.list", Path: "/v1/artifacts", ValidateItem: validateArtifactListItem}},
		}, true
	case "catalog":
		return componentContract{
			Kind:        "catalog",
			HealthPath:  "/v1/catalog/health",
			MetricsPath: "/v1/catalog/metrics",
			ListChecks:  []componentListCheck{{Name: "component.surface.catalog.capabilities", Path: "/v1/catalog/capabilities?limit=1", ValidateItem: validateCatalogListItem}},
		}, true
	case "gateway":
		return componentContract{Kind: "gateway", HealthPath: "/v1/gateway/health", MetricsPath: "/v1/gateway/metrics"}, true
	case "jobs":
		return componentContract{
			Kind:        "jobs",
			HealthPath:  "/v1/jobs/health",
			MetricsPath: "/v1/jobs/metrics",
			ListChecks:  []componentListCheck{{Name: "component.surface.jobs.list", Path: "/v1/jobs", ValidateItem: validateJobListItem}},
		}, true
	case "leases":
		return componentContract{
			Kind:        "leases",
			HealthPath:  "/v1/leases/health",
			MetricsPath: "/v1/leases/metrics",
			ListChecks:  []componentListCheck{{Name: "component.surface.leases.resources", Path: "/v1/resources", ValidateItem: validateLeaseResourceListItem}},
		}, true
	case "node":
		return componentContract{
			Kind:        "node",
			HealthPath:  "/v1/node/health",
			MetricsPath: "/v1/node/metrics",
			ListChecks: []componentListCheck{
				{Name: "component.surface.node.resources", Path: "/v1/node/resources", ValidateItem: validateNodeResourceListItem},
				{Name: "component.surface.node.events", Path: "/v1/node/events", ValidateItem: validateNodeLifecycleEventListItem},
			},
		}, true
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
	if err := validateEnvelopeMeta(envelope.Meta, observability.RequestIDFromContext(ctx)); err != nil {
		result.Error = "health envelope " + err.Error()
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
	if err := validateEnvelopeMeta(envelope.Meta, observability.RequestIDFromContext(ctx)); err != nil {
		result.Error = "metrics envelope " + err.Error()
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

func checkComponentListEndpoint(ctx context.Context, httpClient *http.Client, baseURL, credential string, check componentListCheck, report *ComponentCheckReport) {
	var envelope rawSuccessEnvelope
	status, err := getEnvelopeWithCredential(ctx, httpClient, joinURLPath(baseURL, check.Path), credential, &envelope)
	result := ComponentCheckResult{Name: check.Name, HTTPStatus: status}
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
		result.Error = "list response was not ok"
		report.add(result)
		return
	}
	if err := validateEnvelopeMeta(envelope.Meta, observability.RequestIDFromContext(ctx)); err != nil {
		result.Error = "list envelope " + err.Error()
		report.add(result)
		return
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Data, &payload); err != nil {
		result.Error = "decode list payload: " + err.Error()
		report.add(result)
		return
	}
	itemsRaw, ok := payload["items"]
	if !ok {
		result.Error = "list payload is missing items"
		report.add(result)
		return
	}
	var items []json.RawMessage
	if err := json.Unmarshal(itemsRaw, &items); err != nil {
		result.Error = "list items must be an array: " + err.Error()
		report.add(result)
		return
	}
	if items == nil {
		result.Error = "list items must be an array"
		report.add(result)
		return
	}
	if check.ValidateItem != nil {
		for i, item := range items {
			if err := check.ValidateItem(item); err != nil {
				result.Error = fmt.Sprintf("list items[%d]: %s", i, err)
				report.add(result)
				return
			}
		}
	}
	cursorRaw, ok := payload["next_cursor"]
	if !ok {
		result.Error = "list payload is missing next_cursor"
		report.add(result)
		return
	}
	var cursor *string
	if err := json.Unmarshal(cursorRaw, &cursor); err != nil {
		result.Error = "next_cursor must be null or string: " + err.Error()
		report.add(result)
		return
	}
	result.OK = true
	report.add(result)
}

func validateArtifactListItem(raw json.RawMessage) error {
	var artifact contracts.Artifact
	if err := json.Unmarshal(raw, &artifact); err != nil {
		return fmt.Errorf("decode artifact: %w", err)
	}
	if artifact.ArtifactID == "" {
		return fmt.Errorf("artifact_id is required")
	}
	if artifact.Name == "" {
		return fmt.Errorf("name is required")
	}
	if artifact.MediaType == "" {
		return fmt.Errorf("media_type is required")
	}
	if artifact.Checksum == "" {
		return fmt.Errorf("checksum is required")
	}
	if artifact.CreatedAt == "" {
		return fmt.Errorf("created_at is required")
	}
	if artifact.OwnerSubjectID == "" {
		return fmt.Errorf("owner_subject_id is required")
	}
	return nil
}

func validateCatalogListItem(raw json.RawMessage) error {
	var record contracts.CatalogCapabilityRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return fmt.Errorf("decode catalog capability record: %w", err)
	}
	if record.Capability.ID == "" {
		return fmt.Errorf("capability.id is required")
	}
	if record.Capability.Name == "" {
		return fmt.Errorf("capability.name is required")
	}
	if record.Capability.ExecutionMode == "" {
		return fmt.Errorf("capability.execution_mode is required")
	}
	if record.Route.CapabilityID == "" {
		return fmt.Errorf("route.capability_id is required")
	}
	if record.Route.ServiceID == "" {
		return fmt.Errorf("route.service_id is required")
	}
	if record.Route.ProviderEndpoint == "" {
		return fmt.Errorf("route.provider_endpoint is required")
	}
	if record.Route.ProviderInvokePath == "" {
		return fmt.Errorf("route.provider_invoke_path is required")
	}
	return nil
}

func validateJobListItem(raw json.RawMessage) error {
	var job contracts.Job
	if err := json.Unmarshal(raw, &job); err != nil {
		return fmt.Errorf("decode job: %w", err)
	}
	if job.JobID == "" {
		return fmt.Errorf("job_id is required")
	}
	if job.State == "" {
		return fmt.Errorf("state is required")
	}
	if job.CreatedAt == "" {
		return fmt.Errorf("created_at is required")
	}
	if job.UpdatedAt == "" {
		return fmt.Errorf("updated_at is required")
	}
	return nil
}

func validateLeaseResourceListItem(raw json.RawMessage) error {
	var resource contracts.ResourceRecord
	if err := json.Unmarshal(raw, &resource); err != nil {
		return fmt.Errorf("decode resource: %w", err)
	}
	if resource.ResourceID == "" {
		return fmt.Errorf("resource_id is required")
	}
	if resource.Selector == "" {
		return fmt.Errorf("selector is required")
	}
	if resource.Status == "" {
		return fmt.Errorf("status is required")
	}
	return nil
}

func validateNodeResourceListItem(raw json.RawMessage) error {
	var resource contracts.NodeResource
	if err := json.Unmarshal(raw, &resource); err != nil {
		return fmt.Errorf("decode node resource: %w", err)
	}
	if resource.ResourceID == "" {
		return fmt.Errorf("resource_id is required")
	}
	return nil
}

func validateNodeLifecycleEventListItem(raw json.RawMessage) error {
	var event contracts.NodeLifecycleEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return fmt.Errorf("decode node lifecycle event: %w", err)
	}
	if event.EventID == "" {
		return fmt.Errorf("event_id is required")
	}
	if event.ServiceID == "" {
		return fmt.Errorf("service_id is required")
	}
	if event.Action == "" {
		return fmt.Errorf("action is required")
	}
	if event.Status == "" {
		return fmt.Errorf("status is required")
	}
	if event.OccurredAt == "" {
		return fmt.Errorf("occurred_at is required")
	}
	return nil
}

func getEnvelopeWithCredential(ctx context.Context, httpClient *http.Client, endpoint, credential string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	if credential != "" {
		req.Header.Set("Authorization", credential)
	}
	observability.PropagateRequestID(ctx, req)
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
