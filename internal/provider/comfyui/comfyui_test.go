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
)

func TestDryRunGeneratesImageArtifact(t *testing.T) {
	server := newTestServer(t, Config{Endpoint: "http://provider.local", DryRun: true})

	data := invoke(t, server, DefaultCapabilityID, map[string]any{
		"prompt": "red mug",
		"width":  512,
		"height": 512,
		"seed":   42,
		"steps":  12,
	}, http.StatusOK)

	output := data["output"].(map[string]any)
	if output["dry_run"] != true || output["image_count"].(float64) != 1 {
		t.Fatalf("output = %#v", output)
	}
	artifacts := data["artifacts"].([]any)
	if len(artifacts) != 1 {
		t.Fatalf("artifacts = %#v", artifacts)
	}
	artifact := artifacts[0].(map[string]any)
	if artifact["media_type"] != "image/png" || artifact["checksum"] == "" {
		t.Fatalf("artifact = %#v", artifact)
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
	if output["prompt_id"] != "prompt_1" || output["image_count"].(float64) != 1 || output["dry_run"] != false {
		t.Fatalf("output = %#v", output)
	}
	artifacts := data["artifacts"].([]any)
	artifact := artifacts[0].(map[string]any)
	if artifact["name"] != "image_0001.png" || artifact["media_type"] != "image/png" {
		t.Fatalf("artifact = %#v", artifact)
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
	envelope := invokeEnvelope(t, server, DefaultCapabilityID, map[string]any{"prompt": "red mug"}, http.StatusInternalServerError)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "provider_unavailable" || !strings.Contains(errObj["message"].(string), "HTTP 503") {
		t.Fatalf("error = %#v", errObj)
	}
}

func newTestServer(t *testing.T, cfg Config) http.Handler {
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
	var raw bytes.Buffer
	if err := json.NewEncoder(&raw).Encode(map[string]any{"input": input, "context": map[string]any{"request_id": "req_test"}}); err != nil {
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
