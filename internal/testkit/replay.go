package testkit

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"sort"

	"wendy/internal/contracts"
)

type ReplayResult struct {
	FixtureID string
	Status    int
	Body      map[string]any
	RawBody   []byte
}

type ScenarioReplayReport struct {
	Packages  int
	Exchanges int
	Findings  []ScenarioReplayFinding
}

type ScenarioReplayFinding struct {
	Owner     string
	Path      string
	FixtureID string
	Message   string
}

func (r ScenarioReplayReport) Passed() bool {
	return len(r.Findings) == 0
}

func ReplayScenarioFixtures(s Scenario) ScenarioReplayReport {
	report := ScenarioReplayReport{}
	for _, pkg := range s.Packages {
		report.Packages++
		server := NewFixtureServer(pkg)
		for _, fixture := range pkg.File.Fixtures {
			for _, exchange := range fixtureReplayExchanges(fixture) {
				report.Exchanges++
				if _, err := replayHTTPExchange(server, pkg, exchange.fixtureID, exchange.request, exchange.response); err != nil {
					report.Findings = append(report.Findings, ScenarioReplayFinding{
						Owner:     pkg.Owner,
						Path:      pkg.Path,
						FixtureID: exchange.fixtureID,
						Message:   err.Error(),
					})
				}
			}
		}
	}
	return report
}

func ReplayHTTPFixture(handler http.Handler, pkg FixturePackage, fixtureID string) (ReplayResult, error) {
	fixture, ok := findFixture(pkg, fixtureID)
	if !ok {
		return ReplayResult{}, fmt.Errorf("fixture %s not found in %s", fixtureID, pkg.Path)
	}
	if fixture.Request == nil || fixture.Response == nil {
		return ReplayResult{}, fmt.Errorf("fixture %s is not an HTTP exchange", fixtureID)
	}
	result, err := replayHTTPExchange(handler, pkg, fixtureID, fixture.Request, fixture.Response)
	if err != nil {
		return result, err
	}
	return result, nil
}

type replayExchange struct {
	fixtureID string
	request   *contracts.HTTPRequest
	response  *contracts.HTTPResponse
}

func fixtureReplayExchanges(fixture contracts.Fixture) []replayExchange {
	exchanges := []replayExchange{}
	if fixture.Request != nil && fixture.Response != nil {
		exchanges = append(exchanges, replayExchange{fixtureID: fixture.ID, request: fixture.Request, response: fixture.Response})
	}
	for i := range fixture.Steps {
		step := fixture.Steps[i]
		if step.Request != nil && step.Response != nil {
			exchanges = append(exchanges, replayExchange{fixtureID: stepFixtureID(fixture.ID, "step", i, step), request: step.Request, response: step.Response})
		}
	}
	if fixture.TimeoutInvoke != nil && fixture.TimeoutInvoke.Request != nil && fixture.TimeoutInvoke.Response != nil {
		exchanges = append(exchanges, replayExchange{fixtureID: stepFixtureID(fixture.ID, "timeout_invoke", 0, *fixture.TimeoutInvoke), request: fixture.TimeoutInvoke.Request, response: fixture.TimeoutInvoke.Response})
	}
	for i := range fixture.TimeoutCleanup {
		step := fixture.TimeoutCleanup[i]
		if step.Request != nil && step.Response != nil {
			exchanges = append(exchanges, replayExchange{fixtureID: stepFixtureID(fixture.ID, "timeout_cleanup", i, step), request: step.Request, response: step.Response})
		}
	}
	exchanges = append(exchanges, eventListReplayExchanges(fixture.ID, "event_list", fixture.EventList)...)
	exchanges = append(exchanges, eventListReplayExchanges(fixture.ID, "liveness_event_list", fixture.LivenessEventList)...)
	return exchanges
}

func stepFixtureID(fixtureID, kind string, index int, step contracts.OrchestrationStep) string {
	switch {
	case step.Fixture != "":
		return step.Fixture
	case step.FixtureRef != "":
		return step.FixtureRef
	default:
		return fmt.Sprintf("%s.%s.%d", fixtureID, kind, index)
	}
}

func eventListReplayExchanges(fixtureID, listName string, eventList map[string]any) []replayExchange {
	if len(eventList) == 0 {
		return nil
	}
	events, _ := eventList["events"].([]any)
	if len(events) == 0 {
		return nil
	}
	if requestTemplate, ok := eventList["request_template"]; ok {
		responseTemplate, ok := eventList["response_template"]
		if !ok {
			return nil
		}
		exchanges := make([]replayExchange, 0, len(events))
		for i, rawEvent := range events {
			if exchange, ok := eventReplayExchange(fixtureID, listName, "", i, rawEvent, requestTemplate, responseTemplate); ok {
				exchanges = append(exchanges, exchange)
			}
		}
		return exchanges
	}

	requestTemplates, _ := eventList["request_templates"].(map[string]any)
	responseTemplates, _ := eventList["response_templates"].(map[string]any)
	if len(requestTemplates) == 0 || len(responseTemplates) == 0 {
		return nil
	}
	names := make([]string, 0, len(requestTemplates))
	for name := range requestTemplates {
		if _, ok := responseTemplates[name]; ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	exchanges := []replayExchange{}
	for i, rawEvent := range events {
		for _, name := range names {
			if exchange, ok := eventReplayExchange(fixtureID, listName, name, i, rawEvent, requestTemplates[name], responseTemplates[name]); ok {
				exchanges = append(exchanges, exchange)
			}
		}
	}
	return exchanges
}

func eventReplayExchange(fixtureID, listName, templateName string, index int, rawEvent any, requestTemplate any, responseTemplate any) (replayExchange, bool) {
	event, ok := rawEvent.(map[string]any)
	if !ok {
		return replayExchange{}, false
	}
	var request contracts.HTTPRequest
	if !decodeTemplate(requestTemplate, event, &request) {
		return replayExchange{}, false
	}
	var response contracts.HTTPResponse
	if !decodeTemplate(responseTemplate, event, &response) {
		return replayExchange{}, false
	}
	id := fmt.Sprintf("%s.%s.%d", fixtureID, listName, index)
	if templateName != "" {
		id = fmt.Sprintf("%s.%s.%s.%d", fixtureID, listName, templateName, index)
	}
	return replayExchange{fixtureID: id, request: &request, response: &response}, true
}

func replayHTTPExchange(handler http.Handler, pkg FixturePackage, fixtureID string, request *contracts.HTTPRequest, response *contracts.HTTPResponse) (ReplayResult, error) {
	fixture := contracts.Fixture{ID: fixtureID, Request: request, Response: response}
	req, err := requestFromFixture(pkg, fixture)
	if err != nil {
		return ReplayResult{}, err
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	resp := rec.Result()
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return ReplayResult{}, err
	}
	result := ReplayResult{FixtureID: fixtureID, Status: rec.Code, RawBody: raw}
	if err := compareReplayResponse(pkg, fixtureID, *fixture.Response, result.Status, resp.Header, raw); err != nil {
		return result, err
	}
	if fixture.Response.Body != nil {
		if err := json.Unmarshal(raw, &result.Body); err != nil {
			return result, fmt.Errorf("%s: decode actual response: %w", fixtureID, err)
		}
	}
	return result, nil
}

func findFixture(pkg FixturePackage, fixtureID string) (contracts.Fixture, bool) {
	for _, fixture := range pkg.File.Fixtures {
		if fixture.ID == fixtureID {
			return fixture, true
		}
	}
	return contracts.Fixture{}, false
}

func requestFromFixture(pkg FixturePackage, fixture contracts.Fixture) (*http.Request, error) {
	body, err := requestBodyFromFixture(pkg, *fixture.Request)
	if err != nil {
		return nil, err
	}
	path := fixture.Request.Path
	if fixture.Request.WireQuery != "" {
		path += "?" + fixture.Request.WireQuery
	} else {
		query := url.Values{}
		for key, value := range fixture.Request.Query {
			for _, item := range expectedQueryValues(value) {
				query.Add(key, item)
			}
		}
		if encoded := query.Encode(); encoded != "" {
			path += "?" + encoded
		}
	}
	req := httptest.NewRequest(fixture.Request.Method, path, bytes.NewReader(body))
	for key, value := range fixture.Request.Headers {
		req.Header.Set(key, value)
	}
	if len(body) > 0 && fixture.Request.Body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if req.Header.Get("X-Request-ID") == "" {
		if requestID := expectedRequestID(fixture.Response); requestID != "" {
			req.Header.Set("X-Request-ID", requestID)
		}
	}
	return req, nil
}

func requestBodyFromFixture(pkg FixturePackage, req contracts.HTTPRequest) ([]byte, error) {
	if req.BodyFixture != "" {
		return readBase64Fixture(filepath.Join(filepath.Dir(pkg.AbsPath), req.BodyFixture))
	}
	if req.BodyBase64 != "" {
		body, err := base64.StdEncoding.DecodeString(req.BodyBase64)
		if err != nil {
			return nil, fmt.Errorf("request body_base64 is invalid: %w", err)
		}
		return body, nil
	}
	if req.Body == nil {
		return nil, nil
	}
	return json.Marshal(req.Body)
}

func expectedRequestID(resp *contracts.HTTPResponse) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	meta, _ := resp.Body["meta"].(map[string]any)
	value, _ := meta["request_id"].(string)
	return value
}

func compareReplayResponse(pkg FixturePackage, fixtureID string, expected contracts.HTTPResponse, status int, headers http.Header, raw []byte) error {
	if expected.Status == nil {
		return fmt.Errorf("%s: expected fixture has no status", fixtureID)
	}
	if status != *expected.Status {
		return fmt.Errorf("%s: status %d, want %d: %s", fixtureID, status, *expected.Status, string(raw))
	}
	for key, value := range expected.Headers {
		if got := headers.Get(key); got != value {
			return fmt.Errorf("%s: header %s = %q, want %q", fixtureID, key, got, value)
		}
	}
	if expected.BodyFixture != "" {
		expectedBytes, err := readBase64Fixture(filepath.Join(filepath.Dir(pkg.AbsPath), expected.BodyFixture))
		if err != nil {
			return fmt.Errorf("%s: read expected binary fixture: %w", fixtureID, err)
		}
		if !bytes.Equal(raw, expectedBytes) {
			return fmt.Errorf("%s: binary response did not match fixture", fixtureID)
		}
		return nil
	}
	if expected.Body == nil {
		return nil
	}
	var actual map[string]any
	if err := json.Unmarshal(raw, &actual); err != nil {
		return fmt.Errorf("%s: decode actual response: %w", fixtureID, err)
	}
	if err := compareJSONSubset("$", expected.Body, actual); err != nil {
		return fmt.Errorf("%s: %w", fixtureID, err)
	}
	return nil
}

func compareJSONSubset(path string, expected any, actual any) error {
	switch typedExpected := expected.(type) {
	case map[string]any:
		typedActual, ok := actual.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: expected object, got %T", path, actual)
		}
		for key, value := range typedExpected {
			if _, ok := typedActual[key]; !ok {
				return fmt.Errorf("%s.%s: missing", path, key)
			}
			if err := compareJSONSubset(path+"."+key, value, typedActual[key]); err != nil {
				return err
			}
		}
	case []any:
		typedActual, ok := actual.([]any)
		if !ok {
			return fmt.Errorf("%s: expected array, got %T", path, actual)
		}
		if len(typedActual) != len(typedExpected) {
			return fmt.Errorf("%s: length %d, want %d", path, len(typedActual), len(typedExpected))
		}
		for i := range typedExpected {
			if err := compareJSONSubset(fmt.Sprintf("%s[%d]", path, i), typedExpected[i], typedActual[i]); err != nil {
				return err
			}
		}
	default:
		if !reflect.DeepEqual(expected, actual) {
			return fmt.Errorf("%s: got %#v, want %#v", path, actual, expected)
		}
	}
	return nil
}
