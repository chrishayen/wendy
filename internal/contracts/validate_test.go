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

func TestValidateFixtureFileRejectsMissingFixtureRef(t *testing.T) {
	raw := []byte(`{
		"scenario_id": "S003",
		"runner": "example-runner",
		"fixtures": [{
			"id": "happy_path",
			"precondition": "job is queued",
			"steps": [{"fixture_ref": "missing_step"}]
		}]
	}`)

	_, report := ValidateFixtureFile("inline.json", raw)
	if report.Passed() {
		t.Fatalf("expected validation finding")
	}
	if !hasFindingCode(report, "missing_fixture_ref") {
		t.Fatalf("findings = %+v", report.Findings)
	}
}

func TestValidateFixtureFileAcceptsKnownFixtureRef(t *testing.T) {
	raw := []byte(`{
		"scenario_id": "S003",
		"runner": "example-runner",
		"fixtures": [
			{
				"id": "step_a",
				"request": {"method": "GET", "path": "/v1/a"},
				"response": {"status": 200, "body": {"ok": true, "data": {}, "links": {}, "meta": {"request_id": "req_a", "schema_version": "v1"}}}
			},
			{
				"id": "happy_path",
				"precondition": "job is queued",
				"steps": [{"fixture_ref": "step_a"}]
			}
		]
	}`)

	_, report := ValidateFixtureFile("inline.json", raw)
	if !report.Passed() {
		t.Fatalf("unexpected findings: %+v", report.Findings)
	}
}

func TestValidateFixtureFileRejectsSelfFixtureRef(t *testing.T) {
	raw := []byte(`{
		"scenario_id": "S003",
		"runner": "example-runner",
		"fixtures": [{
			"id": "loop",
			"precondition": "job is queued",
			"steps": [{"fixture_ref": "loop"}]
		}]
	}`)

	_, report := ValidateFixtureFile("inline.json", raw)
	if report.Passed() {
		t.Fatalf("expected validation finding")
	}
	if !hasFindingCode(report, "self_fixture_ref") {
		t.Fatalf("findings = %+v", report.Findings)
	}
}

func TestValidateFixtureFileAcceptsReplayableEventTemplate(t *testing.T) {
	raw := []byte(`{
		"scenario_id": "S003",
		"component": "example",
		"fixtures": [{
			"id": "heartbeat_liveness",
			"event_list": {
				"schema_version": "event-list.v1",
				"replay_kind": "deterministic_request_response_sequence",
				"request_template": {"method": "POST", "path": "/v1/jobs/job_1/heartbeat", "body": {"worker_id": "runner_1"}},
				"response_template": {"status": 200, "body": {"ok": true, "data": {"updated_at": "${timestamp}"}, "links": {}, "meta": {"request_id": "${request_id}", "schema_version": "v1"}}},
				"events": [{"timestamp": "2026-06-05T20:00:37Z", "request_id": "req_1"}]
			}
		}]
	}`)

	_, report := ValidateFixtureFile("inline.json", raw)
	if !report.Passed() {
		t.Fatalf("unexpected findings: %+v", report.Findings)
	}
}

func TestValidateFixtureFileRejectsMissingEventResponseTemplate(t *testing.T) {
	raw := []byte(`{
		"scenario_id": "S003",
		"component": "example",
		"fixtures": [{
			"id": "heartbeat_liveness",
			"event_list": {
				"schema_version": "event-list.v1",
				"replay_kind": "deterministic_request_response_sequence",
				"request_template": {"method": "POST", "path": "/v1/jobs/job_1/heartbeat"},
				"events": [{"request_id": "req_1"}]
			}
		}]
	}`)

	_, report := ValidateFixtureFile("inline.json", raw)
	if report.Passed() {
		t.Fatalf("expected validation finding")
	}
	if !hasFindingCode(report, "missing_event_response_template") {
		t.Fatalf("findings = %+v", report.Findings)
	}
}

func TestValidateFixtureFileRejectsUnresolvedEventTemplate(t *testing.T) {
	raw := []byte(`{
		"scenario_id": "S003",
		"component": "example",
		"fixtures": [{
			"id": "heartbeat_liveness",
			"event_list": {
				"schema_version": "event-list.v1",
				"replay_kind": "deterministic_request_response_sequence",
				"request_template": {"method": "POST", "path": "/v1/jobs/job_1/heartbeat"},
				"response_template": {"status": 200, "body": {"ok": true, "data": {"updated_at": "${missing_timestamp}"}, "links": {}, "meta": {"request_id": "${request_id}", "schema_version": "v1"}}},
				"events": [{"request_id": "req_1"}]
			}
		}]
	}`)

	_, report := ValidateFixtureFile("inline.json", raw)
	if report.Passed() {
		t.Fatalf("expected validation finding")
	}
	if !hasFindingCode(report, "unresolved_event_template") {
		t.Fatalf("findings = %+v", report.Findings)
	}
}

func hasFindingCode(report Report, code string) bool {
	for _, finding := range report.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
