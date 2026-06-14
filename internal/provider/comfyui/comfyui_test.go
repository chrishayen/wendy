package comfyui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wendy/internal/testkit"
)

func TestDryRunValidatesWithoutContentRefs(t *testing.T) {
	server := newTestServer(t, Config{Endpoint: "http://provider.local", DryRun: true})

	data := invokeWithContext(t, server, DefaultCapabilityID, map[string]any{
		"prompt": "red mug",
		"width":  512,
		"height": 512,
		"seed":   42,
		"steps":  12,
	}, map[string]any{
		"subject_id": "sub_test",
		"request_id": "req_test",
		"job_id":     "job_test",
		"dry_run":    true,
	}, http.StatusOK)

	output := data["output"].(map[string]any)
	if output["result"] != "dry_run_valid" || output["filename"] != nil {
		t.Fatalf("output = %#v", output)
	}
	contentRefs := data["content_refs"].([]any)
	if len(contentRefs) != 0 {
		t.Fatalf("content_refs = %#v", contentRefs)
	}
}

func TestRejectsInvalidDimensions(t *testing.T) {
	server := newTestServer(t, Config{Endpoint: "http://provider.local", DryRun: true})
	envelope := invokeEnvelope(t, server, DefaultCapabilityID, map[string]any{
		"prompt": "red mug",
		"width":  513,
		"height": 512,
	}, http.StatusBadRequest)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "validation_failed" || !strings.Contains(errObj["message"].(string), "width") {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestRejectsMissingLora(t *testing.T) {
	server := newTestServer(t, Config{
		Endpoint:        "http://provider.local",
		DryRun:          true,
		LoraCatalogPath: writeLoraCatalog(t),
	})
	envelope := invokeEnvelope(t, server, DefaultCapabilityID, map[string]any{
		"prompt": "red mug",
		"width":  512,
		"height": 512,
		"lora":   "missing-lora",
	}, http.StatusBadRequest)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "validation_failed" || !strings.Contains(errObj["message"].(string), "missing-lora") {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestSubmitsWorkflowAndFetchesComfyUIImage(t *testing.T) {
	imageBody := dryRunPNG()
	var promptBody map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/prompt":
			if err := json.NewDecoder(r.Body).Decode(&promptBody); err != nil {
				t.Fatalf("decode prompt body: %v", err)
			}
			writeJSON(t, w, map[string]any{"prompt_id": "prompt_1"})
		case r.Method == http.MethodGet && r.URL.Path == "/history/prompt_1":
			writeJSON(t, w, map[string]any{
				"prompt_1": map[string]any{
					"outputs": map[string]any{
						"9": map[string]any{"images": []any{map[string]any{
							"filename":  "image_0001.png",
							"subfolder": "",
							"type":      "output",
						}}},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/view":
			if r.URL.Query().Get("filename") != "image_0001.png" || r.URL.Query().Get("type") != "output" {
				t.Fatalf("query = %s", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(imageBody)
		default:
			t.Fatalf("unexpected backend request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer backend.Close()

	server := newTestServer(t, Config{
		Endpoint:     "http://provider.local",
		ComfyUIURL:   backend.URL,
		WorkflowPath: writeWorkflow(t),
		PollInterval: time.Millisecond,
	})
	data := invoke(t, server, DefaultCapabilityID, map[string]any{
		"prompt": "red mug",
		"width":  768,
		"height": 512,
		"seed":   7,
		"steps":  8,
	}, http.StatusOK)

	rendered := promptBody["prompt"].(map[string]any)
	node := rendered["6"].(map[string]any)
	inputs := node["inputs"].(map[string]any)
	if inputs["text"] != "red mug" || inputs["width"].(float64) != 768 || inputs["height"].(float64) != 512 || inputs["seed"].(float64) != 7 || inputs["steps"].(float64) != 8 {
		t.Fatalf("rendered workflow inputs = %#v", inputs)
	}
	output := data["output"].(map[string]any)
	if output["result"] != "image_generated" || output["filename"] != "job_test.png" {
		t.Fatalf("output = %#v", output)
	}
	contentRefs := data["content_refs"].([]any)
	contentRef := contentRefs[0].(map[string]any)
	if contentRef["content_ref"] != "pcr_test" || contentRef["name"] != "job_test.png" || contentRef["media_type"] != "image/png" {
		t.Fatalf("content_ref = %#v", contentRef)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/provider/artifacts/pcr_test/content", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("content status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Digest") == "" || rec.Body.Len() != len(imageBody) {
		t.Fatalf("content headers=%v len=%d", rec.Header(), rec.Body.Len())
	}
}

func TestMapsComfyUIFailureToProviderUnavailable(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "backend down", http.StatusServiceUnavailable)
	}))
	defer backend.Close()
	server := newTestServer(t, Config{
		Endpoint:     "http://provider.local",
		ComfyUIURL:   backend.URL,
		WorkflowPath: writeWorkflow(t),
	})
	envelope := invokeEnvelope(t, server, DefaultCapabilityID, map[string]any{"prompt": "red mug", "width": 512, "height": 512}, http.StatusServiceUnavailable)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "provider_unavailable" || errObj["message"] != "ComfyUI backend is unavailable" {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestReplaysS003ProviderFixtures(t *testing.T) {
	pkg := loadC10Package(t)
	server := newTestServer(t, Config{
		Endpoint:     "http://provider.local",
		ServiceID:    "svc_comfyui_gpu",
		ServiceName:  "ComfyUI GPU",
		CapabilityID: "cap_image_generate_gpu",
		DryRun:       true,
		RunnerTokens: []string{"token_s003_runner"},
		AgentTokens:  []string{"token_s003_agent"},
	})
	server.SetClock(fixedTime(t, "2026-06-05T20:00:00Z"))

	for _, fixtureID := range []string{
		"provider_invoke_success",
		"provider_content_ok",
		"provider_run_status_route_not_found",
		"provider_run_cancel_route_not_found",
		"provider_invoke_dry_run",
		"provider_invalid_input",
		"provider_invalid_width_out_of_range",
		"provider_invalid_height_not_multiple_of_8",
		"provider_missing_context",
		"provider_invoke_unauthorized",
		"provider_invoke_forbidden",
		"provider_content_unauthorized",
		"provider_content_forbidden",
		"provider_content_not_found",
	} {
		replayFixture(t, server, pkg, fixtureID)
	}

	server.SetClock(fixedTime(t, "2026-06-05T20:00:06Z"))
	replayFixture(t, server, pkg, "provider_health_ok")

	server.SetClock(fixedTime(t, "2026-06-05T20:00:00Z"))
	server.MarkContentUnavailable("pcr_s003_0001", true)
	replayFixture(t, server, pkg, "provider_content_unavailable")
	server.MarkContentUnavailable("pcr_s003_0001", false)

	server.SetClock(fixedTime(t, "2026-06-05T20:15:01Z"))
	replayFixture(t, server, pkg, "provider_content_expired")
}

func TestReplaysS003ProviderBackendFailures(t *testing.T) {
	pkg := loadC10Package(t)
	backendDown := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "backend down", http.StatusServiceUnavailable)
	}))
	defer backendDown.Close()
	unavailable := newTestServer(t, Config{
		Endpoint:     "http://provider.local",
		ServiceID:    "svc_comfyui_gpu",
		CapabilityID: "cap_image_generate_gpu",
		ComfyUIURL:   backendDown.URL,
		WorkflowPath: writeWorkflow(t),
		RunnerTokens: []string{"token_s003_runner"},
	})
	replayFixture(t, unavailable, pkg, "provider_backend_unavailable")

	slowBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/prompt":
			writeJSON(t, w, map[string]any{"prompt_id": "prompt_slow"})
		case "/history/prompt_slow":
			time.Sleep(20 * time.Millisecond)
			writeJSON(t, w, map[string]any{"prompt_slow": map[string]any{"outputs": map[string]any{}}})
		default:
			t.Fatalf("unexpected backend request %s", r.URL.Path)
		}
	}))
	defer slowBackend.Close()
	timeout := newTestServer(t, Config{
		Endpoint:     "http://provider.local",
		ServiceID:    "svc_comfyui_gpu",
		CapabilityID: "cap_image_generate_gpu",
		ComfyUIURL:   slowBackend.URL,
		WorkflowPath: writeWorkflow(t),
		Timeout:      time.Millisecond,
		PollInterval: time.Millisecond,
		RunnerTokens: []string{"token_s003_runner"},
	})
	replayFixture(t, timeout, pkg, "provider_timeout")
}

func newTestServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return server
}

func invoke(t *testing.T, handler http.Handler, capabilityID string, input map[string]any, wantStatus int) map[string]any {
	t.Helper()
	envelope := invokeEnvelope(t, handler, capabilityID, input, wantStatus)
	if !envelope["ok"].(bool) {
		t.Fatalf("error envelope = %#v", envelope)
	}
	return envelope["data"].(map[string]any)
}

func invokeEnvelope(t *testing.T, handler http.Handler, capabilityID string, input map[string]any, wantStatus int) map[string]any {
	t.Helper()
	return invokeEnvelopeWithContext(t, handler, capabilityID, input, map[string]any{
		"subject_id":        "sub_test",
		"request_id":        "req_test",
		"job_id":            "job_test",
		"resource_lease_id": "lease_test",
		"dry_run":           false,
	}, wantStatus)
}

func invokeWithContext(t *testing.T, handler http.Handler, capabilityID string, input map[string]any, context map[string]any, wantStatus int) map[string]any {
	t.Helper()
	envelope := invokeEnvelopeWithContext(t, handler, capabilityID, input, context, wantStatus)
	if !envelope["ok"].(bool) {
		t.Fatalf("error envelope = %#v", envelope)
	}
	return envelope["data"].(map[string]any)
}

func invokeEnvelopeWithContext(t *testing.T, handler http.Handler, capabilityID string, input map[string]any, context map[string]any, wantStatus int) map[string]any {
	t.Helper()
	var raw bytes.Buffer
	if err := json.NewEncoder(&raw).Encode(map[string]any{"input": input, "context": context}); err != nil {
		t.Fatalf("encode invoke: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/provider/capabilities/"+capabilityID+"/invoke", &raw)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, wantStatus, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return envelope
}

func loadC10Package(t *testing.T) testkit.FixturePackage {
	t.Helper()
	root := filepath.Join("..", "..", "..", "testdata", "contract-sim")
	scenario, err := testkit.LoadScenario(root, filepath.Join("fixtures", "S003", "manifest.json"))
	if err != nil {
		t.Fatalf("load scenario: %v", err)
	}
	pkg, ok := testkit.FindPackage(scenario, "c10-comfyui-provider")
	if !ok {
		t.Fatalf("c10 package not found")
	}
	return pkg
}

func replayFixture(t *testing.T, handler http.Handler, pkg testkit.FixturePackage, fixtureID string) {
	t.Helper()
	if _, err := testkit.ReplayHTTPFixture(handler, pkg, fixtureID); err != nil {
		t.Fatalf("replay %s: %v", fixtureID, err)
	}
}

func fixedTime(t *testing.T, value string) func() time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time: %v", err)
	}
	return func() time.Time { return parsed }
}

func writeWorkflow(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workflow.json")
	body := []byte(`{
  "6": {
    "class_type": "CLIPTextEncode",
    "inputs": {
      "text": "{{prompt}}",
      "width": "{{width}}",
      "height": "{{height}}",
      "seed": "{{seed}}",
      "steps": "{{steps}}",
      "lora": "{{lora}}"
    }
  }
}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	return path
}

func writeLoraCatalog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "loras.json")
	body := []byte(`{"items":[{"id":"product-photo","name":"Product Photo"}]}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write lora catalog: %v", err)
	}
	return path
}

func writeJSON(t *testing.T, w http.ResponseWriter, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
