package testkit

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestLoadAndValidateS003Scenario(t *testing.T) {
	scenario := loadS003(t)
	if scenario.Manifest.Status != "fixture-ready" {
		t.Fatalf("manifest status = %q, want fixture-ready", scenario.Manifest.Status)
	}
	if len(scenario.Packages) != 10 {
		t.Fatalf("package count = %d, want 10", len(scenario.Packages))
	}

	report := ValidateScenario(scenario)
	if !report.Passed() {
		t.Fatalf("contract fixture validation failed: %+v", report.Findings[:min(len(report.Findings), 10)])
	}
	if report.Fixtures == 0 {
		t.Fatalf("expected fixtures to be counted")
	}
}

func TestGatewayFixtureServerServesPublicTools(t *testing.T) {
	scenario := loadS003(t)
	pkg, ok := FindPackage(scenario, "c04-agent-tool-gateway")
	if !ok {
		t.Fatalf("gateway package not found")
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/tools", nil)
	rec := httptest.NewRecorder()
	NewFixtureServer(pkg).ServeHTTP(rec, req)
	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["ok"] != true {
		t.Fatalf("ok = %v, want true", body["ok"])
	}
}

func TestProviderFixtureServerServesBinaryContent(t *testing.T) {
	scenario := loadS003(t)
	pkg, ok := FindPackage(scenario, "c10-comfyui-provider")
	if !ok {
		t.Fatalf("provider package not found")
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/provider/artifacts/pcr_s003_0001/content", nil)
	rec := httptest.NewRecorder()
	NewFixtureServer(pkg).ServeHTTP(rec, req)
	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("content type = %q, want image/png", resp.Header.Get("Content-Type"))
	}
	bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(bytes) != 68 {
		t.Fatalf("binary length = %d, want 68", len(bytes))
	}
}

func loadS003(t *testing.T) Scenario {
	t.Helper()
	root := filepath.Join("..", "..", "testdata", "contract-sim")
	scenario, err := LoadScenario(root, filepath.Join("fixtures", "S003", "manifest.json"))
	if err != nil {
		t.Fatalf("load scenario: %v", err)
	}
	return scenario
}
