package testkit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"

	"pacp/internal/contracts"
)

type ReplayResult struct {
	FixtureID string
	Status    int
	Body      map[string]any
	RawBody   []byte
}

func ReplayHTTPFixture(handler http.Handler, pkg FixturePackage, fixtureID string) (ReplayResult, error) {
	fixture, ok := findFixture(pkg, fixtureID)
	if !ok {
		return ReplayResult{}, fmt.Errorf("fixture %s not found in %s", fixtureID, pkg.Path)
	}
	if fixture.Request == nil || fixture.Response == nil {
		return ReplayResult{}, fmt.Errorf("fixture %s is not an HTTP exchange", fixtureID)
	}
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
	if err := compareReplayResponse(pkg, fixtureID, *fixture.Response, result.Status, raw); err != nil {
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
	query := url.Values{}
	for key, value := range fixture.Request.Query {
		for _, item := range expectedQueryValues(value) {
			query.Add(key, item)
		}
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	req := httptest.NewRequest(fixture.Request.Method, path, bytes.NewReader(body))
	for key, value := range fixture.Request.Headers {
		req.Header.Set(key, value)
	}
	if len(body) > 0 && fixture.Request.BodyFixture == "" {
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

func compareReplayResponse(pkg FixturePackage, fixtureID string, expected contracts.HTTPResponse, status int, raw []byte) error {
	if expected.Status == nil {
		return fmt.Errorf("%s: expected fixture has no status", fixtureID)
	}
	if status != *expected.Status {
		return fmt.Errorf("%s: status %d, want %d: %s", fixtureID, status, *expected.Status, string(raw))
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
