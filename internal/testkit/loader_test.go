package testkit

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"pacp/internal/contracts"
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

func TestValidateScenarioReportsMissingBinaryFixture(t *testing.T) {
	scenario := loadTempScenario(t, `{
  "scenario_id": "S999",
  "latest_source_run": "runs/S999/run-001.md",
  "status": "fixture-ready",
  "fixture_sets": [
    {"owner": "provider", "path": "provider/fixtures.json"}
  ]
}`, `{
  "scenario_id": "S999",
  "component": "provider",
  "fixtures": [
    {
      "id": "binary_missing",
      "request": {"method": "GET", "path": "/v1/provider/content"},
      "response": {"status": 200, "headers": {"Content-Type": "image/png"}, "body_fixture": "missing.base64"}
    }
  ]
}`)

	report := ValidateScenario(scenario)
	if report.Passed() || !hasFinding(report, "response_body_fixture_read_failed") {
		t.Fatalf("report = %#v", report)
	}
}

func TestValidateScenarioReportsInvalidBinaryFixture(t *testing.T) {
	scenario := loadTempScenario(t, `{
  "scenario_id": "S999",
  "latest_source_run": "runs/S999/run-001.md",
  "status": "fixture-ready",
  "fixture_sets": [
    {"owner": "provider", "path": "provider/fixtures.json", "binary_fixtures": ["provider/bad.base64"]}
  ]
}`, `{
  "scenario_id": "S999",
  "component": "provider",
  "fixtures": [
    {
      "id": "binary_invalid",
      "request": {"method": "GET", "path": "/v1/provider/content"},
      "response": {"status": 200, "headers": {"Content-Type": "image/png"}, "body_fixture": "bad.base64"}
    }
  ]
}`)
	if err := os.WriteFile(filepath.Join(scenario.Root, "provider", "bad.base64"), []byte("not base64"), 0o600); err != nil {
		t.Fatalf("write bad fixture: %v", err)
	}

	report := ValidateScenario(scenario)
	if report.Passed() || !hasFinding(report, "manifest_binary_fixture_invalid_base64") || !hasFinding(report, "response_body_fixture_invalid_base64") {
		t.Fatalf("report = %#v", report)
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

func loadTempScenario(t *testing.T, manifest, fixtures string) Scenario {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "provider"), 0o700); err != nil {
		t.Fatalf("mkdir provider: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "provider", "fixtures.json"), []byte(fixtures), 0o600); err != nil {
		t.Fatalf("write fixtures: %v", err)
	}
	scenario, err := LoadScenario(root, "manifest.json")
	if err != nil {
		t.Fatalf("load scenario: %v", err)
	}
	return scenario
}

func hasFinding(report contracts.Report, code string) bool {
	for _, finding := range report.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
