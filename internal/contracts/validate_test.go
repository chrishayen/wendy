package contracts

import "testing"

func TestValidateFixtureFileRejectsMissingErrorEnvelope(t *testing.T) {
	raw := []byte(`{
		"scenario_id": "S003",
		"component": "example",
		"fixtures": [{
			"id": "bad_error",
			"request": {"method": "GET", "path": "/v1/example"},
			"response": {"status": 404, "body": {"ok": false, "meta": {"request_id": "req_1", "schema_version": "v1"}}}
		}]
	}`)

	_, report := ValidateFixtureFile("inline.json", raw)
	if report.Passed() {
		t.Fatalf("expected validation finding")
	}
	if report.Findings[0].Code != "missing_error" {
		t.Fatalf("finding code = %s, want missing_error", report.Findings[0].Code)
	}
}

func TestValidateFixtureFileAcceptsBinaryResponse(t *testing.T) {
	raw := []byte(`{
		"scenario_id": "S003",
		"component": "example",
		"fixtures": [{
			"id": "binary_ok",
			"request": {"method": "GET", "path": "/v1/example/content"},
			"response": {"status": 200, "headers": {"Content-Type": "image/png"}, "body_fixture": "image.base64"}
		}]
	}`)

	_, report := ValidateFixtureFile("inline.json", raw)
	if !report.Passed() {
		t.Fatalf("unexpected findings: %+v", report.Findings)
	}
}
