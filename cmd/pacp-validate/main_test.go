package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateManifestAcceptsValidManifest(t *testing.T) {
	manifestPath := writeValidationManifest(t, validValidationManifest("svc_validate", "cap_validate_echo"))
	var stdout, stderr bytes.Buffer
	code := run([]string{"manifest", manifestPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	report := decodeValidationReport(t, stdout.Bytes())
	if !report.OK || report.Data.Validated != 1 {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Data.Items) != 1 || report.Data.Items[0].CapabilityIDs[0] != "cap_validate_echo" {
		t.Fatalf("items = %+v", report.Data.Items)
	}
}

func TestValidateManifestRejectsUnsupportedVersion(t *testing.T) {
	manifest := validValidationManifest("svc_validate", "cap_validate_echo")
	manifest["schema_version"] = "v2"
	manifestPath := writeValidationManifest(t, manifest)
	var stdout, stderr bytes.Buffer
	code := run([]string{"manifest", manifestPath}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	report := decodeValidationReport(t, stdout.Bytes())
	if report.OK || len(report.Data.Findings) == 0 {
		t.Fatalf("report = %+v", report)
	}
	if !hasFinding(report.Data.Findings, "manifest_invalid", "schema_version must be v1") {
		t.Fatalf("findings = %+v", report.Data.Findings)
	}
}

func TestValidateProviderInvokeChecksCapabilityInputSchema(t *testing.T) {
	manifestPath := writeValidationManifest(t, validValidationManifest("svc_validate", "cap_validate_echo"))
	payloadPath := writeValidationJSON(t, "payload.json", map[string]any{
		"input":   map[string]any{"message": "hello", "count": 2},
		"context": map[string]any{"request_id": "req_validate"},
	})
	var stdout, stderr bytes.Buffer
	code := run([]string{"provider-invoke", "-manifest", manifestPath, "-capability", "cap_validate_echo", payloadPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	report := decodeValidationReport(t, stdout.Bytes())
	if !report.OK || report.Data.Validated != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestValidateProviderInvokeRejectsMissingRequiredInput(t *testing.T) {
	manifestPath := writeValidationManifest(t, validValidationManifest("svc_validate", "cap_validate_echo"))
	payloadPath := writeValidationJSON(t, "payload.json", map[string]any{
		"input": map[string]any{"count": 2},
	})
	var stdout, stderr bytes.Buffer
	code := run([]string{"provider-invoke", "-manifest", manifestPath, "-capability", "cap_validate_echo", payloadPath}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	report := decodeValidationReport(t, stdout.Bytes())
	if report.OK || len(report.Data.Findings) != 1 {
		t.Fatalf("report = %+v", report)
	}
	if report.Data.Findings[0].Code != "input_schema_invalid" || report.Data.Findings[0].Message != "message is required" {
		t.Fatalf("findings = %+v", report.Data.Findings)
	}
}

func TestValidateToolInvokeChecksEnvelopeFields(t *testing.T) {
	manifestPath := writeValidationManifest(t, validValidationManifest("svc_validate", "cap_validate_echo"))
	payloadPath := writeValidationJSON(t, "payload.json", map[string]any{
		"input":          map[string]any{"message": "hello"},
		"preferred_mode": 3,
	})
	var stdout, stderr bytes.Buffer
	code := run([]string{"tool-invoke", "-manifest", manifestPath, "-capability", "cap_validate_echo", payloadPath}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	report := decodeValidationReport(t, stdout.Bytes())
	if len(report.Data.Findings) != 1 || report.Data.Findings[0].Code != "payload_preferred_mode_invalid" {
		t.Fatalf("findings = %+v", report.Data.Findings)
	}
}

func TestValidateInvokeReportsMissingCapability(t *testing.T) {
	manifestPath := writeValidationManifest(t, validValidationManifest("svc_validate", "cap_validate_echo"))
	payloadPath := writeValidationJSON(t, "payload.json", map[string]any{
		"input": map[string]any{"message": "hello"},
	})
	var stdout, stderr bytes.Buffer
	code := run([]string{"provider-invoke", "-manifest", manifestPath, "-capability", "cap_missing", payloadPath}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	report := decodeValidationReport(t, stdout.Bytes())
	if len(report.Data.Findings) != 1 || report.Data.Findings[0].Code != "capability_not_found" {
		t.Fatalf("findings = %+v", report.Data.Findings)
	}
}

func decodeValidationReport(t *testing.T, raw []byte) validationReport {
	t.Helper()
	var report validationReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("decode report: %v output=%s", err, string(raw))
	}
	return report
}

func hasFinding(findings []validationFinding, code, message string) bool {
	for _, finding := range findings {
		if finding.Code == code && strings.Contains(finding.Message, message) {
			return true
		}
	}
	return false
}

func writeValidationManifest(t *testing.T, manifest map[string]any) string {
	t.Helper()
	return writeValidationJSON(t, "manifest.json", manifest)
}

func writeValidationJSON(t *testing.T, name string, value map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func validValidationManifest(serviceID, capabilityID string) map[string]any {
	return map[string]any{
		"schema_version": "v1",
		"service": map[string]any{
			"id":            serviceID,
			"name":          "Validation Provider",
			"description":   "Provider used by pacp-validate tests.",
			"version":       "v1",
			"provider_kind": "test",
		},
		"provider": map[string]any{
			"endpoint": "http://provider.local:18088",
		},
		"capabilities": []any{
			map[string]any{
				"id":             capabilityID,
				"name":           "Echo",
				"description":    "Echo a message.",
				"execution_mode": "sync",
				"input_schema": map[string]any{
					"type":     "object",
					"required": []any{"message"},
					"properties": map[string]any{
						"message": map[string]any{"type": "string"},
						"count":   map[string]any{"type": "integer", "minimum": 1},
					},
				},
				"output_schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"message": map[string]any{"type": "string"},
					},
				},
				"examples":       []any{},
				"side_effects":   "none",
				"resource_hints": []any{},
				"artifact_hints": []any{},
				"timeout_hint":   "30s",
			},
		},
	}
}
