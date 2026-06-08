package contracts

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

var allowedHTTPMethods = map[string]struct{}{
	"GET": {}, "POST": {}, "PUT": {}, "PATCH": {}, "DELETE": {},
}

func ValidateFixtureFile(path string, raw []byte) (FixtureFile, Report) {
	var file FixtureFile
	report := Report{Files: 1}
	if err := json.Unmarshal(raw, &file); err != nil {
		report.Findings = append(report.Findings, Finding{
			File: path, Code: "invalid_json", Message: err.Error(),
		})
		return file, report
	}

	if file.ScenarioID == "" {
		report.add(path, "", "missing_scenario_id", "fixture file must name its scenario_id")
	}
	if len(file.Fixtures) == 0 {
		report.add(path, "", "missing_fixtures", "fixture file must contain at least one fixture")
	}

	seenIDs := map[string]struct{}{}
	for i := range file.Fixtures {
		fixture := &file.Fixtures[i]
		report.Fixtures++
		if fixture.ID == "" {
			report.add(path, "", "missing_fixture_id", "fixture id is required")
			continue
		}
		if _, exists := seenIDs[fixture.ID]; exists {
			report.add(path, fixture.ID, "duplicate_fixture_id", "fixture id must be unique within its file")
		}
		seenIDs[fixture.ID] = struct{}{}

		validateFixture(path, fixture, &report)
	}

	return file, report
}

func validateFixture(path string, fixture *Fixture, report *Report) {
	switch {
	case fixture.Request != nil || fixture.Response != nil:
		validateHTTPExchange(path, fixture.ID, fixture.Request, fixture.Response, report)
	case fixture.EventList != nil:
		validateEventList(path, fixture.ID, fixture.EventList, report)
	case fixture.OwnedEvent != nil:
		validateOwnedEvent(path, fixture.ID, fixture.OwnedEvent, report)
	case fixture.Mapping != nil:
		validateMapping(path, fixture.ID, fixture.Mapping, report)
	case fixture.LivenessEventList != nil || fixture.TimeoutInvoke != nil || len(fixture.TimeoutCleanup) > 0:
		validateTimeoutPath(path, fixture, report)
	case len(fixture.Steps) > 0:
		validateOrchestration(path, fixture, report)
	default:
		report.add(path, fixture.ID, "unknown_fixture_shape", "fixture must be an HTTP exchange, event list, or orchestration")
	}
}

func validateHTTPExchange(path, id string, req *HTTPRequest, resp *HTTPResponse, report *Report) {
	if req == nil {
		report.add(path, id, "missing_request", "HTTP exchange fixture must include request")
	} else {
		validateHTTPRequest(path, id, req, report)
	}
	if resp == nil {
		report.add(path, id, "missing_response", "HTTP exchange fixture must include response")
		return
	}
	validateHTTPResponse(path, id, resp, report)
}

func validateHTTPRequest(path, id string, req *HTTPRequest, report *Report) {
	if _, ok := allowedHTTPMethods[req.Method]; !ok {
		report.add(path, id, "invalid_http_method", fmt.Sprintf("unsupported or missing method %q", req.Method))
	}
	if req.Path == "" || !strings.HasPrefix(req.Path, "/") {
		report.add(path, id, "invalid_http_path", "request path must be absolute")
	}
	validateWireQuery(path, id, req, report)
	if req.BodyFixture != "" && req.BodyBase64 != "" {
		report.add(path, id, "duplicate_request_body_source", "request must not include both body_fixture and body_base64")
	}
	if req.BodyBase64 != "" {
		if _, err := base64.StdEncoding.DecodeString(req.BodyBase64); err != nil {
			report.add(path, id, "request_body_base64_invalid", "request body_base64 must be valid standard base64")
		}
	}
}

func validateWireQuery(path, id string, req *HTTPRequest, report *Report) {
	if req.WireQuery == "" {
		return
	}
	parsed, err := url.ParseQuery(req.WireQuery)
	if err != nil {
		report.add(path, id, "invalid_wire_query", "wire_query must be a valid URL query string")
		return
	}
	if len(req.Query) == 0 {
		return
	}
	if len(parsed) != len(req.Query) {
		report.add(path, id, "wire_query_mismatch", "wire_query keys must match query")
		return
	}
	for key, value := range req.Query {
		want := fixtureQueryValues(value)
		got := append([]string(nil), parsed[key]...)
		sort.Strings(want)
		sort.Strings(got)
		if len(want) != len(got) {
			report.add(path, id, "wire_query_mismatch", fmt.Sprintf("wire_query values for %s must match query", key))
			return
		}
		for i := range want {
			if want[i] != got[i] {
				report.add(path, id, "wire_query_mismatch", fmt.Sprintf("wire_query values for %s must match query", key))
				return
			}
		}
	}
}

func fixtureQueryValues(value any) []string {
	switch typed := value.(type) {
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			values = append(values, fmt.Sprint(item))
		}
		return values
	case []string:
		return append([]string(nil), typed...)
	default:
		return []string{fmt.Sprint(value)}
	}
}

func validateHTTPResponse(path, id string, resp *HTTPResponse, report *Report) {
	if resp.Status == nil {
		report.add(path, id, "missing_status", "HTTP response must include status")
		return
	}
	if *resp.Status < 100 || *resp.Status > 599 {
		report.add(path, id, "invalid_status", "HTTP response status must be in the 100-599 range")
	}

	if resp.BodyFixture != "" {
		if resp.Headers["Content-Type"] == "" {
			report.add(path, id, "missing_binary_content_type", "binary fixture responses must include Content-Type")
		}
		return
	}

	if resp.Body == nil {
		report.add(path, id, "missing_body", "non-binary HTTP response must include body")
		return
	}

	okValue, exists := resp.Body["ok"]
	ok, okIsBool := okValue.(bool)
	if !exists || !okIsBool {
		report.add(path, id, "missing_ok", "JSON response body must include boolean ok")
		return
	}

	if *resp.Status >= 400 {
		if ok {
			report.add(path, id, "error_ok_true", "error responses must set ok=false")
		}
		validateErrorEnvelope(path, id, resp.Body, report)
		return
	}

	if !ok {
		report.add(path, id, "success_ok_false", "successful responses must set ok=true")
	}
	validateMeta(path, id, resp.Body, report)
}

func validateErrorEnvelope(path, id string, body map[string]any, report *Report) {
	errorValue, ok := body["error"].(map[string]any)
	if !ok {
		report.add(path, id, "missing_error", "error responses must include error object")
		return
	}
	if _, ok := errorValue["code"].(string); !ok {
		report.add(path, id, "missing_error_code", "error object must include code")
	}
	if _, ok := errorValue["message"].(string); !ok {
		report.add(path, id, "missing_error_message", "error object must include message")
	}
	if _, ok := errorValue["retryable"].(bool); !ok {
		report.add(path, id, "missing_error_retryable", "error object must include boolean retryable")
	}
	validateMeta(path, id, body, report)
}

func validateMeta(path, id string, body map[string]any, report *Report) {
	meta, ok := body["meta"].(map[string]any)
	if !ok {
		report.add(path, id, "missing_meta", "JSON response body must include meta")
		return
	}
	if meta["schema_version"] != "v1" {
		report.add(path, id, "invalid_schema_version", "meta.schema_version must be v1")
	}
	if _, ok := meta["request_id"].(string); !ok {
		report.add(path, id, "missing_request_id", "meta.request_id must be present")
	}
}

func validateEventList(path, id string, eventList map[string]any, report *Report) {
	if eventList["schema_version"] == "" {
		report.add(path, id, "missing_event_schema", "event list must include schema_version")
	}
	events, ok := eventList["events"].([]any)
	if !ok || len(events) == 0 {
		report.add(path, id, "missing_events", "event list must include one or more events")
	}
}

func validateOwnedEvent(path, id string, event map[string]any, report *Report) {
	for _, field := range []string{"event_id", "event_type", "occurred_at"} {
		if _, ok := event[field].(string); !ok {
			report.add(path, id, "invalid_owned_event", fmt.Sprintf("owned_event.%s must be present", field))
		}
	}
}

func validateMapping(path, id string, mapping map[string]any, report *Report) {
	if len(mapping) == 0 {
		report.add(path, id, "empty_mapping", "mapping fixture must include at least one mapping field")
	}
}

func validateOrchestration(path string, fixture *Fixture, report *Report) {
	if len(fixture.OrchestrationOrder) == 0 && fixture.Precondition == "" {
		report.add(path, fixture.ID, "missing_orchestration_context", "orchestration fixtures must list orchestration_order or precondition")
	}
	for i, step := range fixture.Steps {
		validateStep(path, fmt.Sprintf("%s.steps[%d]", fixture.ID, i), step, report)
	}
}

func validateTimeoutPath(path string, fixture *Fixture, report *Report) {
	if fixture.Precondition == "" {
		report.add(path, fixture.ID, "missing_timeout_precondition", "timeout path must include precondition")
	}
	if len(fixture.OrchestrationOrder) == 0 {
		report.add(path, fixture.ID, "missing_timeout_order", "timeout path must include orchestration_order")
	}
	if fixture.LivenessEventList == nil {
		report.add(path, fixture.ID, "missing_liveness_events", "timeout path must include liveness_event_list")
	} else {
		validateEventList(path, fixture.ID+".liveness_event_list", fixture.LivenessEventList, report)
	}
	if fixture.TimeoutInvoke == nil {
		report.add(path, fixture.ID, "missing_timeout_invoke", "timeout path must include timeout_invoke")
	} else {
		validateStep(path, fixture.ID+".timeout_invoke", *fixture.TimeoutInvoke, report)
	}
	if len(fixture.TimeoutCleanup) == 0 {
		report.add(path, fixture.ID, "missing_timeout_cleanup", "timeout path must include timeout_cleanup")
	}
	for i, step := range fixture.TimeoutCleanup {
		validateStep(path, fmt.Sprintf("%s.timeout_cleanup[%d]", fixture.ID, i), step, report)
	}
}

func validateStep(path, stepID string, step OrchestrationStep, report *Report) {
	if step.Fixture == "" && step.FixtureRef == "" {
		report.add(path, stepID, "missing_step_name", "orchestration step must name fixture or fixture_ref")
	}
	if step.Request != nil || step.Response != nil {
		validateHTTPExchange(path, stepID, step.Request, step.Response, report)
	}
}

func (r *Report) add(file, fixture, code, message string) {
	r.Findings = append(r.Findings, Finding{
		File: file, Fixture: fixture, Code: code, Message: message,
	})
}
