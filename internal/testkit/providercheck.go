package testkit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"pacp/internal/contracts"
	"pacp/internal/observability"
	"pacp/internal/provider"
)

type ProviderCheckOptions struct {
	BaseURL      string
	CapabilityID string
	Input        map[string]any
	Credential   string
	RequestID    string
}

type ProviderExpectedErrorOptions struct {
	BaseURL        string
	CapabilityID   string
	Input          map[string]any
	Credential     string
	RequestID      string
	WantHTTPStatus int
	WantCode       string
}

type ProviderCheckReport struct {
	OK     bool                  `json:"ok"`
	Checks []ProviderCheckResult `json:"checks"`
}

type ProviderCheckResult struct {
	Name       string `json:"name"`
	OK         bool   `json:"ok"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Error      string `json:"error,omitempty"`
}

func (r ProviderCheckReport) Passed() bool {
	return r.OK
}

func CheckProvider(ctx context.Context, httpClient *http.Client, opts ProviderCheckOptions) ProviderCheckReport {
	report := ProviderCheckReport{OK: true}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if requestID := strings.TrimSpace(opts.RequestID); requestID != "" {
		ctx = observability.WithRequestID(ctx, requestID)
	}
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		report.add(ProviderCheckResult{Name: "provider.base_url", Error: "provider URL is required"})
		return report
	}

	credential := strings.TrimSpace(opts.Credential)
	opts.Credential = credential
	manifest, ok := checkProviderManifest(ctx, httpClient, baseURL, credential, &report)
	if !ok {
		return report
	}
	checkProviderHealth(ctx, httpClient, baseURL, credential, manifest, &report)
	invoked := false
	if strings.TrimSpace(opts.CapabilityID) != "" {
		var response contracts.ProviderInvokeResponse
		var capability contracts.Capability
		response, capability, invoked = checkProviderInvoke(ctx, httpClient, baseURL, manifest, opts, &report)
		if invoked {
			checkProviderArtifactMetadata(capability, response, &report)
			checkProviderInvalidInput(ctx, httpClient, baseURL, credential, manifest, strings.TrimSpace(opts.CapabilityID), &report)
		}
	}
	checkProviderMetrics(ctx, httpClient, baseURL, credential, strings.TrimSpace(opts.CapabilityID), invoked, &report)
	return report
}

func checkProviderManifest(ctx context.Context, httpClient *http.Client, baseURL, credential string, report *ProviderCheckReport) (contracts.ProviderManifest, bool) {
	var envelope rawSuccessEnvelope
	status, err := getEnvelope(ctx, httpClient, joinURLPath(baseURL, "/v1/provider/manifest"), credential, &envelope)
	result := ProviderCheckResult{Name: "provider.manifest", HTTPStatus: status}
	if err != nil {
		result.Error = err.Error()
		report.add(result)
		return contracts.ProviderManifest{}, false
	}
	if status < 200 || status >= 300 {
		result.Error = fmt.Sprintf("HTTP %d", status)
		report.add(result)
		return contracts.ProviderManifest{}, false
	}
	if !envelope.OK {
		result.Error = "manifest response was not ok"
		report.add(result)
		return contracts.ProviderManifest{}, false
	}
	if err := validateEnvelopeMeta(envelope.Meta, observability.RequestIDFromContext(ctx)); err != nil {
		result.Error = "manifest envelope " + err.Error()
		report.add(result)
		return contracts.ProviderManifest{}, false
	}
	var manifest contracts.ProviderManifest
	if err := json.Unmarshal(envelope.Data, &manifest); err != nil {
		result.Error = "decode manifest: " + err.Error()
		report.add(result)
		return contracts.ProviderManifest{}, false
	}
	if errs := contracts.ValidateProviderManifest(manifest); len(errs) > 0 {
		result.Error = strings.Join(errs, "; ")
		report.add(result)
		return contracts.ProviderManifest{}, false
	}
	result.OK = true
	report.add(result)
	return manifest, true
}

func checkProviderHealth(ctx context.Context, httpClient *http.Client, baseURL, credential string, manifest contracts.ProviderManifest, report *ProviderCheckReport) {
	healthPath := manifest.Provider.HealthPath
	if healthPath == "" {
		healthPath = "/v1/provider/health"
	}
	var envelope rawSuccessEnvelope
	status, err := getEnvelope(ctx, httpClient, joinURLPath(baseURL, healthPath), credential, &envelope)
	result := ProviderCheckResult{Name: "provider.health", HTTPStatus: status}
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
	var health struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(envelope.Data, &health); err != nil {
		result.Error = "decode health: " + err.Error()
		report.add(result)
		return
	}
	if health.Status == "" {
		result.Error = "health response was not ok"
		report.add(result)
		return
	}
	if health.Status != "healthy" {
		result.Error = "reported status " + health.Status
		report.add(result)
		return
	}
	result.OK = true
	report.add(result)
}

func CheckProviderExpectedError(ctx context.Context, httpClient *http.Client, opts ProviderExpectedErrorOptions) ProviderCheckReport {
	report := ProviderCheckReport{OK: true}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if requestID := strings.TrimSpace(opts.RequestID); requestID != "" {
		ctx = observability.WithRequestID(ctx, requestID)
	}
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		report.add(ProviderCheckResult{Name: "provider.base_url", Error: "provider URL is required"})
		return report
	}
	capabilityID := strings.TrimSpace(opts.CapabilityID)
	if capabilityID == "" {
		report.add(ProviderCheckResult{Name: "provider.expected_error", Error: "capability id is required"})
		return report
	}
	input := opts.Input
	if input == nil {
		input = map[string]any{}
	}
	request := contracts.ProviderInvokeRequest{
		Input: input,
		Context: contracts.ProviderInvokeContext{
			RequestID: observability.RequestIDFromContext(ctx),
		},
	}
	var envelope rawErrorEnvelope
	path := "/v1/provider/capabilities/" + url.PathEscape(capabilityID) + "/invoke"
	status, err := postEnvelope(ctx, httpClient, joinURLPath(baseURL, path), opts.Credential, request, &envelope)
	result := ProviderCheckResult{Name: "provider.expected_error", HTTPStatus: status}
	if err != nil {
		result.Error = err.Error()
		report.add(result)
		return report
	}
	wantStatus := opts.WantHTTPStatus
	if wantStatus == 0 {
		wantStatus = http.StatusInternalServerError
	}
	if status != wantStatus {
		result.Error = fmt.Sprintf("HTTP %d, want %d", status, wantStatus)
		report.add(result)
		return report
	}
	if envelope.OK {
		result.Error = "expected error response was ok"
		report.add(result)
		return report
	}
	if err := validateEnvelopeMeta(envelope.Meta, observability.RequestIDFromContext(ctx)); err != nil {
		result.Error = "expected error envelope " + err.Error()
		report.add(result)
		return report
	}
	wantCode := strings.TrimSpace(opts.WantCode)
	if wantCode != "" && envelope.Error.Code != wantCode {
		result.Error = fmt.Sprintf("error code = %q, want %q", envelope.Error.Code, wantCode)
		report.add(result)
		return report
	}
	if envelope.Error.Message == "" {
		result.Error = "error message is required"
		report.add(result)
		return report
	}
	result.OK = true
	report.add(result)
	return report
}

func checkProviderInvoke(ctx context.Context, httpClient *http.Client, baseURL string, manifest contracts.ProviderManifest, opts ProviderCheckOptions, report *ProviderCheckReport) (contracts.ProviderInvokeResponse, contracts.Capability, bool) {
	capabilityID := strings.TrimSpace(opts.CapabilityID)
	capability, ok := findCapability(manifest, capabilityID)
	result := ProviderCheckResult{Name: "provider.invoke"}
	if !ok {
		result.Error = "capability not found in manifest: " + capabilityID
		report.add(result)
		return contracts.ProviderInvokeResponse{}, contracts.Capability{}, false
	}
	input := opts.Input
	if input == nil {
		input = map[string]any{}
	}
	if err := provider.ValidateObject(input, capability.InputSchema); err != nil {
		result.Error = "input does not match manifest schema: " + err.Error()
		report.add(result)
		return contracts.ProviderInvokeResponse{}, capability, false
	}
	request := contracts.ProviderInvokeRequest{
		Input: input,
		Context: contracts.ProviderInvokeContext{
			RequestID: observability.RequestIDFromContext(ctx),
		},
	}
	var envelope rawSuccessEnvelope
	path := "/v1/provider/capabilities/" + url.PathEscape(capabilityID) + "/invoke"
	status, err := postEnvelope(ctx, httpClient, joinURLPath(baseURL, path), opts.Credential, request, &envelope)
	result.HTTPStatus = status
	if err != nil {
		result.Error = err.Error()
		report.add(result)
		return contracts.ProviderInvokeResponse{}, capability, false
	}
	if status < 200 || status >= 300 {
		result.Error = fmt.Sprintf("HTTP %d", status)
		report.add(result)
		return contracts.ProviderInvokeResponse{}, capability, false
	}
	if !envelope.OK {
		result.Error = "invoke response was not ok"
		report.add(result)
		return contracts.ProviderInvokeResponse{}, capability, false
	}
	if err := validateEnvelopeMeta(envelope.Meta, observability.RequestIDFromContext(ctx)); err != nil {
		result.Error = "invoke envelope " + err.Error()
		report.add(result)
		return contracts.ProviderInvokeResponse{}, capability, false
	}
	var response contracts.ProviderInvokeResponse
	if err := json.Unmarshal(envelope.Data, &response); err != nil {
		result.Error = "decode invoke response: " + err.Error()
		report.add(result)
		return contracts.ProviderInvokeResponse{}, capability, false
	}
	if err := provider.ValidateObject(response.Output, capability.OutputSchema); err != nil {
		result.Error = "output does not match manifest schema: " + err.Error()
		report.add(result)
		return contracts.ProviderInvokeResponse{}, capability, false
	}
	result.OK = true
	report.add(result)
	return response, capability, true
}

func checkProviderArtifactMetadata(capability contracts.Capability, response contracts.ProviderInvokeResponse, report *ProviderCheckReport) {
	requiresArtifacts := capabilityRequiresArtifacts(capability)
	hasArtifacts := len(response.Artifacts) > 0 || len(response.ContentRefs) > 0
	if !requiresArtifacts && !hasArtifacts {
		return
	}
	result := ProviderCheckResult{Name: "provider.artifact_metadata"}
	if requiresArtifacts && !hasArtifacts {
		result.Error = "capability advertises artifact output but response contained no artifacts or content refs"
		report.add(result)
		return
	}
	allowedMediaTypes := artifactHintMediaTypes(capability)
	for i, artifact := range response.Artifacts {
		if artifact.Name == "" {
			result.Error = fmt.Sprintf("artifacts[%d].name is required", i)
			report.add(result)
			return
		}
		if artifact.MediaType == "" {
			result.Error = fmt.Sprintf("artifacts[%d].media_type is required", i)
			report.add(result)
			return
		}
		if !mediaTypeAllowed(artifact.MediaType, allowedMediaTypes) {
			result.Error = fmt.Sprintf("artifacts[%d].media_type = %q is not advertised by artifact_hints", i, artifact.MediaType)
			report.add(result)
			return
		}
		if artifact.ContentBase64 == "" && artifact.Checksum == "" {
			result.Error = fmt.Sprintf("artifacts[%d] must include content_base64 or checksum", i)
			report.add(result)
			return
		}
	}
	for i, ref := range response.ContentRefs {
		if ref.ContentRef == "" {
			result.Error = fmt.Sprintf("content_refs[%d].content_ref is required", i)
			report.add(result)
			return
		}
		if ref.Name == "" {
			result.Error = fmt.Sprintf("content_refs[%d].name is required", i)
			report.add(result)
			return
		}
		if ref.MediaType == "" {
			result.Error = fmt.Sprintf("content_refs[%d].media_type is required", i)
			report.add(result)
			return
		}
		if !mediaTypeAllowed(ref.MediaType, allowedMediaTypes) {
			result.Error = fmt.Sprintf("content_refs[%d].media_type = %q is not advertised by artifact_hints", i, ref.MediaType)
			report.add(result)
			return
		}
		if ref.Size <= 0 {
			result.Error = fmt.Sprintf("content_refs[%d].size must be positive", i)
			report.add(result)
			return
		}
		if ref.Checksum == "" {
			result.Error = fmt.Sprintf("content_refs[%d].checksum is required", i)
			report.add(result)
			return
		}
		if ref.ExpiresAt == "" {
			result.Error = fmt.Sprintf("content_refs[%d].expires_at is required", i)
			report.add(result)
			return
		}
	}
	result.OK = true
	report.add(result)
}

func capabilityRequiresArtifacts(capability contracts.Capability) bool {
	for _, hint := range capability.ArtifactHints {
		count := strings.ToLower(strings.TrimSpace(hint.Count))
		if count == "" || !strings.HasPrefix(count, "zero") {
			return true
		}
	}
	return false
}

func artifactHintMediaTypes(capability contracts.Capability) map[string]bool {
	types := map[string]bool{}
	for _, hint := range capability.ArtifactHints {
		mediaType := strings.TrimSpace(hint.MediaType)
		if mediaType != "" {
			types[mediaType] = true
		}
	}
	return types
}

func mediaTypeAllowed(mediaType string, allowed map[string]bool) bool {
	return len(allowed) == 0 || allowed[mediaType]
}

func checkProviderInvalidInput(ctx context.Context, httpClient *http.Client, baseURL, credential string, manifest contracts.ProviderManifest, capabilityID string, report *ProviderCheckReport) {
	capability, ok := findCapability(manifest, capabilityID)
	if !ok || len(requiredFields(capability.InputSchema)) == 0 {
		return
	}
	request := contracts.ProviderInvokeRequest{
		Input: map[string]any{},
		Context: contracts.ProviderInvokeContext{
			RequestID: observability.RequestIDFromContext(ctx),
		},
	}
	var envelope rawErrorEnvelope
	path := "/v1/provider/capabilities/" + url.PathEscape(capabilityID) + "/invoke"
	status, err := postEnvelope(ctx, httpClient, joinURLPath(baseURL, path), credential, request, &envelope)
	result := ProviderCheckResult{Name: "provider.invalid_input", HTTPStatus: status}
	if err != nil {
		result.Error = err.Error()
		report.add(result)
		return
	}
	if status != http.StatusBadRequest {
		result.Error = fmt.Sprintf("HTTP %d, want 400", status)
		report.add(result)
		return
	}
	if envelope.OK {
		result.Error = "invalid input response was ok"
		report.add(result)
		return
	}
	if err := validateEnvelopeMeta(envelope.Meta, observability.RequestIDFromContext(ctx)); err != nil {
		result.Error = "invalid input envelope " + err.Error()
		report.add(result)
		return
	}
	if envelope.Error.Code != "validation_failed" {
		result.Error = "invalid input error code was " + envelope.Error.Code
		report.add(result)
		return
	}
	result.OK = true
	report.add(result)
}

func checkProviderMetrics(ctx context.Context, httpClient *http.Client, baseURL, credential, capabilityID string, expectInvocation bool, report *ProviderCheckReport) {
	var envelope rawSuccessEnvelope
	status, err := getEnvelope(ctx, httpClient, joinURLPath(baseURL, "/v1/provider/metrics"), credential, &envelope)
	result := ProviderCheckResult{Name: "provider.metrics", HTTPStatus: status}
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
	if metrics.Component == "" {
		result.Error = "metrics component is required"
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
	if expectInvocation && !hasProviderInvocationMetric(metrics.Samples, capabilityID) {
		result.Error = "metrics missing provider_invocations_total for " + capabilityID
		report.add(result)
		return
	}
	result.OK = true
	report.add(result)
}

func findCapability(manifest contracts.ProviderManifest, capabilityID string) (contracts.Capability, bool) {
	for _, capability := range manifest.Capabilities {
		if capability.ID == capabilityID {
			return capability, true
		}
	}
	return contracts.Capability{}, false
}

func hasProviderInvocationMetric(samples []contracts.MetricSample, capabilityID string) bool {
	for _, sample := range samples {
		if sample.Name != "provider_invocations_total" {
			continue
		}
		if sample.Labels["capability_id"] == capabilityID && sample.Value > 0 {
			return true
		}
	}
	return false
}

func (r *ProviderCheckReport) add(result ProviderCheckResult) {
	if !result.OK {
		r.OK = false
	}
	r.Checks = append(r.Checks, result)
}

type rawSuccessEnvelope struct {
	OK   bool            `json:"ok"`
	Data json.RawMessage `json:"data"`
	Meta map[string]any  `json:"meta"`
}

type rawErrorEnvelope struct {
	OK    bool                  `json:"ok"`
	Error contracts.ErrorObject `json:"error"`
	Meta  map[string]any        `json:"meta"`
}

func validateEnvelopeMeta(meta map[string]any, expectedRequestID string) error {
	if meta == nil {
		return fmt.Errorf("meta is required")
	}
	requestID, _ := meta["request_id"].(string)
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return fmt.Errorf("meta.request_id is required")
	}
	expectedRequestID = strings.TrimSpace(expectedRequestID)
	if expectedRequestID != "" && requestID != expectedRequestID {
		return fmt.Errorf("meta.request_id = %q, want %q", requestID, expectedRequestID)
	}
	schemaVersion, _ := meta["schema_version"].(string)
	if strings.TrimSpace(schemaVersion) == "" {
		return fmt.Errorf("meta.schema_version is required")
	}
	return nil
}

func requiredFields(schema map[string]any) []string {
	raw, ok := schema["required"]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if value, ok := item.(string); ok {
				out = append(out, value)
			}
		}
		return out
	default:
		return nil
	}
}

func getEnvelope(ctx context.Context, httpClient *http.Client, endpoint, credential string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	credential = strings.TrimSpace(credential)
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

func postEnvelope(ctx context.Context, httpClient *http.Client, endpoint, credential string, body any, out any) (int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	credential = strings.TrimSpace(credential)
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

func joinURLPath(baseURL, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return baseURL
	}
	if strings.HasPrefix(path, "/") {
		return baseURL + path
	}
	return baseURL + "/" + path
}
