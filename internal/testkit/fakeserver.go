package testkit

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"

	"wendy/internal/contracts"
)

type FixtureServer struct {
	packageDir string
	routes     map[string][]fixtureRoute
	served     map[int]int
	nextID     int
	mu         sync.Mutex
}

type fixtureRoute struct {
	id       int
	request  contracts.HTTPRequest
	response contracts.HTTPResponse
}

func NewFixtureServer(pkg FixturePackage) *FixtureServer {
	server := &FixtureServer{
		packageDir: filepath.Dir(pkg.AbsPath),
		routes:     map[string][]fixtureRoute{},
		served:     map[int]int{},
	}
	for _, fixture := range pkg.File.Fixtures {
		server.addRoute(fixture.Request, fixture.Response)
		for _, step := range fixture.Steps {
			server.addRoute(step.Request, step.Response)
		}
		if fixture.TimeoutInvoke != nil {
			server.addRoute(fixture.TimeoutInvoke.Request, fixture.TimeoutInvoke.Response)
		}
		for _, step := range fixture.TimeoutCleanup {
			server.addRoute(step.Request, step.Response)
		}
		server.addEventListRoutes(fixture.EventList)
		server.addEventListRoutes(fixture.LivenessEventList)
	}
	return server
}

func (s *FixtureServer) addRoute(request *contracts.HTTPRequest, response *contracts.HTTPResponse) {
	if request == nil || response == nil {
		return
	}
	key := request.Method + " " + request.Path
	id := s.nextID
	s.nextID++
	s.routes[key] = append(s.routes[key], fixtureRoute{
		id:       id,
		request:  *request,
		response: *response,
	})
}

func (s *FixtureServer) addEventListRoutes(eventList map[string]any) {
	if len(eventList) == 0 {
		return
	}
	events, _ := eventList["events"].([]any)
	if len(events) == 0 {
		return
	}
	if requestTemplate, ok := eventList["request_template"]; ok {
		responseTemplate, ok := eventList["response_template"]
		if !ok {
			return
		}
		for _, rawEvent := range events {
			event, ok := rawEvent.(map[string]any)
			if !ok {
				continue
			}
			s.addTemplatedRoute(requestTemplate, responseTemplate, event)
		}
		return
	}

	requestTemplates, _ := eventList["request_templates"].(map[string]any)
	responseTemplates, _ := eventList["response_templates"].(map[string]any)
	if len(requestTemplates) == 0 || len(responseTemplates) == 0 {
		return
	}
	names := make([]string, 0, len(requestTemplates))
	for name := range requestTemplates {
		if _, ok := responseTemplates[name]; ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, rawEvent := range events {
		event, ok := rawEvent.(map[string]any)
		if !ok {
			continue
		}
		for _, name := range names {
			s.addTemplatedRoute(requestTemplates[name], responseTemplates[name], event)
		}
	}
}

func (s *FixtureServer) addTemplatedRoute(requestTemplate any, responseTemplate any, event map[string]any) {
	var request contracts.HTTPRequest
	if !decodeTemplate(requestTemplate, event, &request) {
		return
	}
	var response contracts.HTTPResponse
	if !decodeTemplate(responseTemplate, event, &response) {
		return
	}
	s.addRoute(&request, &response)
}

func decodeTemplate(template any, event map[string]any, out any) bool {
	raw, err := json.Marshal(substituteTemplate(template, event))
	if err != nil {
		return false
	}
	return json.Unmarshal(raw, out) == nil
}

func substituteTemplate(value any, event map[string]any) any {
	switch typed := value.(type) {
	case string:
		out := typed
		for key, replacement := range event {
			out = strings.ReplaceAll(out, "${"+key+"}", fmt.Sprint(replacement))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = substituteTemplate(item, event)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = substituteTemplate(item, event)
		}
		return out
	default:
		return value
	}
}

func (s *FixtureServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok": false,
			"error": map[string]any{
				"code": "fixture_request_read_failed", "message": err.Error(), "retryable": false,
			},
			"links": map[string]any{},
			"meta":  map[string]any{"request_id": "req_fixture_request_read_failed", "schema_version": "v1"},
		})
		return
	}

	key := r.Method + " " + r.URL.Path
	routes := s.routes[key]
	matches := make([]fixtureRoute, 0, len(routes))
	for _, route := range routes {
		if s.requestMatches(r, body, route.request) {
			matches = append(matches, route)
		}
	}
	if len(matches) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"ok": false,
			"error": map[string]any{
				"code": "fixture_not_found", "message": "no fixture matches request", "retryable": false,
				"details": map[string]any{"candidate_count": len(routes)},
			},
			"links": map[string]any{},
			"meta":  map[string]any{"request_id": "req_fixture_not_found", "schema_version": "v1"},
		})
		return
	}
	response := s.nextResponse(matches)
	s.writeFixtureResponse(w, response)
}

func (s *FixtureServer) nextResponse(matches []fixtureRoute) contracts.HTTPResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	chosen := matches[len(matches)-1]
	for _, match := range matches {
		if s.served[match.id] == 0 {
			chosen = match
			break
		}
	}
	s.served[chosen.id]++
	return chosen.response
}

func (s *FixtureServer) writeFixtureResponse(w http.ResponseWriter, response contracts.HTTPResponse) {
	status := http.StatusOK
	if response.Status != nil {
		status = *response.Status
	}
	for key, value := range response.Headers {
		w.Header().Set(key, value)
	}

	if response.BodyFixture != "" {
		bytes, err := readBase64Fixture(filepath.Join(s.packageDir, response.BodyFixture))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"ok": false,
				"error": map[string]any{
					"code": "fixture_read_failed", "message": err.Error(), "retryable": false,
				},
				"links": map[string]any{},
				"meta":  map[string]any{"request_id": "req_fixture_read_failed", "schema_version": "v1"},
			})
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write(bytes)
		return
	}

	writeJSON(w, status, response.Body)
}

func (s *FixtureServer) requestMatches(r *http.Request, body []byte, fixture contracts.HTTPRequest) bool {
	if fixture.WireQuery != "" {
		if r.URL.RawQuery != fixture.WireQuery {
			return false
		}
	} else if !queryMatches(r.URL.Query(), fixture.Query) {
		return false
	}
	for name, value := range fixture.Headers {
		if r.Header.Get(name) != value {
			return false
		}
	}
	if fixture.BodyFixture != "" {
		expected, err := readBase64Fixture(filepath.Join(s.packageDir, fixture.BodyFixture))
		return err == nil && reflect.DeepEqual(expected, body)
	}
	if fixture.BodyBase64 != "" {
		expected, err := base64.StdEncoding.DecodeString(fixture.BodyBase64)
		return err == nil && reflect.DeepEqual(expected, body)
	}
	if fixture.Body != nil {
		var actual any
		if err := json.Unmarshal(body, &actual); err != nil {
			return false
		}
		return reflect.DeepEqual(actual, fixture.Body)
	}
	return strings.TrimSpace(string(body)) == ""
}

func queryMatches(actual map[string][]string, expected map[string]any) bool {
	if len(expected) == 0 {
		return len(actual) == 0
	}
	if len(actual) != len(expected) {
		return false
	}
	for key, value := range expected {
		expectedValues := expectedQueryValues(value)
		actualValues := append([]string(nil), actual[key]...)
		sort.Strings(expectedValues)
		sort.Strings(actualValues)
		if !reflect.DeepEqual(actualValues, expectedValues) {
			return false
		}
	}
	return true
}

func expectedQueryValues(value any) []string {
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

func readBase64Fixture(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(string(raw))
}

func writeJSON(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
