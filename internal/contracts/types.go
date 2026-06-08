package contracts

import "encoding/json"

type FixtureFile struct {
	ScenarioID   string    `json:"scenario_id"`
	Component    string    `json:"component,omitempty"`
	Actor        string    `json:"actor,omitempty"`
	Runner       string    `json:"runner,omitempty"`
	FixtureScope string    `json:"fixture_scope,omitempty"`
	Fixtures     []Fixture `json:"fixtures"`
}

func (f FixtureFile) Owner() string {
	switch {
	case f.Component != "":
		return f.Component
	case f.Actor != "":
		return f.Actor
	case f.Runner != "":
		return f.Runner
	default:
		return ""
	}
}

type Fixture struct {
	ID                 string              `json:"id"`
	Request            *HTTPRequest        `json:"request,omitempty"`
	Response           *HTTPResponse       `json:"response,omitempty"`
	Dependencies       []string            `json:"dependencies,omitempty"`
	Precondition       string              `json:"precondition,omitempty"`
	EventList          map[string]any      `json:"event_list,omitempty"`
	OwnedEvent         map[string]any      `json:"owned_event,omitempty"`
	Mapping            map[string]any      `json:"mapping,omitempty"`
	LivenessEventList  map[string]any      `json:"liveness_event_list,omitempty"`
	TimeoutInvoke      *OrchestrationStep  `json:"timeout_invoke,omitempty"`
	TimeoutCleanup     []OrchestrationStep `json:"timeout_cleanup,omitempty"`
	OrchestrationOrder []string            `json:"orchestration_order,omitempty"`
	Steps              []OrchestrationStep `json:"steps,omitempty"`
	Assertions         []string            `json:"assertions,omitempty"`
	Raw                json.RawMessage     `json:"-"`
}

type HTTPRequest struct {
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Headers     map[string]string `json:"headers,omitempty"`
	Query       map[string]any    `json:"query,omitempty"`
	WireQuery   string            `json:"wire_query,omitempty"`
	Body        any               `json:"body,omitempty"`
	BodyFixture string            `json:"body_fixture,omitempty"`
	BodyBase64  string            `json:"body_base64,omitempty"`
}

type HTTPResponse struct {
	Status      *int              `json:"status,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        map[string]any    `json:"body,omitempty"`
	BodyFixture string            `json:"body_fixture,omitempty"`
}

type OrchestrationStep struct {
	Fixture    string        `json:"fixture,omitempty"`
	FixtureRef string        `json:"fixture_ref,omitempty"`
	Request    *HTTPRequest  `json:"request,omitempty"`
	Response   *HTTPResponse `json:"response,omitempty"`
}

type Finding struct {
	File    string
	Fixture string
	Code    string
	Message string
}

type Report struct {
	Files    int
	Fixtures int
	Findings []Finding
}

func (r Report) Passed() bool {
	return len(r.Findings) == 0
}

func (r *Report) Merge(next Report) {
	r.Files += next.Files
	r.Fixtures += next.Fixtures
	r.Findings = append(r.Findings, next.Findings...)
}
