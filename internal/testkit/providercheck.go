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
	"pacp/internal/provider"
)

type ProviderCheckOptions struct {
	BaseURL      string
	CapabilityID string
	Input        map[string]any
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
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		report.add(ProviderCheckResult{Name: "provider.base_url", Error: "provider URL is required"})
		return report
	}

	manifest, ok := checkProviderManifest(ctx, httpClient, baseURL, &report)
	if !ok {
		return report
	}
	checkProviderHealth(ctx, httpClient, baseURL, manifest, &report)
	if strings.TrimSpace(opts.CapabilityID) != "" {
		checkProviderInvoke(ctx, httpClient, baseURL, manifest, opts, &report)
	}
	return report
}

func checkProviderManifest(ctx context.Context, httpClient *http.Client, baseURL string, report *ProviderCheckReport) (contracts.ProviderManifest, bool) {
	var envelope rawSuccessEnvelope
	status, err := getEnvelope(ctx, httpClient, joinURLPath(baseURL, "/v1/provider/manifest"), &envelope)
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

func checkProviderHealth(ctx context.Context, httpClient *http.Client, baseURL string, manifest contracts.ProviderManifest, report *ProviderCheckReport) {
	healthPath := manifest.Provider.HealthPath
	if healthPath == "" {
		healthPath = "/v1/provider/health"
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	status, err := getEnvelope(ctx, httpClient, joinURLPath(baseURL, healthPath), &envelope)
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
	if !envelope.OK || envelope.Data.Status == "" {
		result.Error = "health response was not ok"
		report.add(result)
		return
	}
	if envelope.Data.Status != "healthy" {
		result.Error = "reported status " + envelope.Data.Status
		report.add(result)
		return
	}
	result.OK = true
	report.add(result)
}

func checkProviderInvoke(ctx context.Context, httpClient *http.Client, baseURL string, manifest contracts.ProviderManifest, opts ProviderCheckOptions, report *ProviderCheckReport) {
	capabilityID := strings.TrimSpace(opts.CapabilityID)
	capability, ok := findCapability(manifest, capabilityID)
	result := ProviderCheckResult{Name: "provider.invoke"}
	if !ok {
		result.Error = "capability not found in manifest: " + capabilityID
		report.add(result)
		return
	}
	input := opts.Input
	if input == nil {
		input = map[string]any{}
	}
	if err := provider.ValidateObject(input, capability.InputSchema); err != nil {
		result.Error = "input does not match manifest schema: " + err.Error()
		report.add(result)
		return
	}
	request := contracts.ProviderInvokeRequest{Input: input}
	var envelope rawSuccessEnvelope
	path := "/v1/provider/capabilities/" + url.PathEscape(capabilityID) + "/invoke"
	status, err := postEnvelope(ctx, httpClient, joinURLPath(baseURL, path), request, &envelope)
	result.HTTPStatus = status
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
		result.Error = "invoke response was not ok"
		report.add(result)
		return
	}
	var response contracts.ProviderInvokeResponse
	if err := json.Unmarshal(envelope.Data, &response); err != nil {
		result.Error = "decode invoke response: " + err.Error()
		report.add(result)
		return
	}
	if err := provider.ValidateObject(response.Output, capability.OutputSchema); err != nil {
		result.Error = "output does not match manifest schema: " + err.Error()
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

func (r *ProviderCheckReport) add(result ProviderCheckResult) {
	if !result.OK {
		r.OK = false
	}
	r.Checks = append(r.Checks, result)
}

type rawSuccessEnvelope struct {
	OK   bool            `json:"ok"`
	Data json.RawMessage `json:"data"`
}

func getEnvelope(ctx context.Context, httpClient *http.Client, endpoint string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
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

func postEnvelope(ctx context.Context, httpClient *http.Client, endpoint string, body any, out any) (int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
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
