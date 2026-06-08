package aitoolkit

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDatasetRegisterListAndInspect(t *testing.T) {
	workspace := newWorkspace(t)
	server := newTestServer(t, Config{Endpoint: "http://provider.local", WorkspaceRoot: workspace, DryRun: true})

	registered := invoke(t, server, DefaultDatasetRegisterCapability, map[string]any{
		"dataset_id": "product_photos",
		"name":       "Product Photos",
		"path":       "datasets/product",
		"metadata":   map[string]any{"source": "operator"},
	}, http.StatusOK)
	if registered["dataset_id"] != "product_photos" || registered["image_count"].(float64) != 1 {
		t.Fatalf("registered = %#v", registered)
	}

	list := invoke(t, server, DefaultDatasetListCapability, map[string]any{}, http.StatusOK)
	if list["count"].(float64) != 1 {
		t.Fatalf("list = %#v", list)
	}

	inspected := invoke(t, server, DefaultDatasetInspectCapability, map[string]any{"dataset_id": "product_photos"}, http.StatusOK)
	if inspected["name"] != "Product Photos" || inspected["path"] != "datasets/product" {
		t.Fatalf("inspected = %#v", inspected)
	}
}

func TestRejectsDatasetPathOutsideWorkspace(t *testing.T) {
	workspace := newWorkspace(t)
	outside := t.TempDir()
	server := newTestServer(t, Config{Endpoint: "http://provider.local", WorkspaceRoot: workspace, DryRun: true})

	envelope := invokeEnvelope(t, server, DefaultDatasetRegisterCapability, map[string]any{
		"dataset_id": "outside",
		"name":       "Outside",
		"path":       outside,
	}, http.StatusBadRequest)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "validation_failed" || !strings.Contains(errObj["message"].(string), "outside") {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestDryRunTrainingProducesOutputAndArtifact(t *testing.T) {
	workspace := newWorkspace(t)
	server := newTestServer(t, Config{Endpoint: "http://provider.local", WorkspaceRoot: workspace, DryRun: true})
	_ = invoke(t, server, DefaultDatasetRegisterCapability, map[string]any{
		"dataset_id": "product_photos",
		"name":       "Product Photos",
		"path":       "datasets/product",
	}, http.StatusOK)

	data := invokeData(t, server, DefaultTrainCapability, map[string]any{
		"dataset_id":  "product_photos",
		"output_name": "product_lora",
		"preset":      "z-image-turbo-lora",
		"steps":       12,
		"rank":        8,
	}, http.StatusOK)
	output := data["output"].(map[string]any)
	if output["output_id"] != "lora_product_lora" || output["dry_run"] != true || output["steps"].(float64) != 12 || output["rank"].(float64) != 8 {
		t.Fatalf("output = %#v", output)
	}
	artifacts := data["artifacts"].([]any)
	if len(artifacts) != 1 {
		t.Fatalf("artifacts = %#v", artifacts)
	}
	artifact := artifacts[0].(map[string]any)
	if artifact["media_type"] != "application/json" || artifact["checksum"] == "" {
		t.Fatalf("artifact = %#v", artifact)
	}
}

func TestMapsTrainingCommandFailureToProviderUnavailable(t *testing.T) {
	workspace := newWorkspace(t)
	server := newTestServer(t, Config{
		Endpoint:      "http://provider.local",
		WorkspaceRoot: workspace,
		TrainCommand:  []string{"/bin/false"},
	})
	_ = invoke(t, server, DefaultDatasetRegisterCapability, map[string]any{
		"dataset_id": "product_photos",
		"name":       "Product Photos",
		"path":       "datasets/product",
	}, http.StatusOK)

	envelope := invokeEnvelope(t, server, DefaultTrainCapability, map[string]any{
		"dataset_id":  "product_photos",
		"output_name": "product_lora",
	}, http.StatusInternalServerError)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "provider_unavailable" {
		t.Fatalf("error = %#v", errObj)
	}
}

func newWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dataset := filepath.Join(root, "datasets", "product")
	if err := os.MkdirAll(dataset, 0o700); err != nil {
		t.Fatalf("mkdir dataset: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataset, "image.png"), []byte("fake image"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
	return root
}

func newTestServer(t *testing.T, cfg Config) http.Handler {
	t.Helper()
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("new AI-Toolkit provider: %v", err)
	}
	return server
}

func invoke(t *testing.T, handler http.Handler, capabilityID string, input map[string]any, wantStatus int) map[string]any {
	t.Helper()
	data := invokeData(t, handler, capabilityID, input, wantStatus)
	return data["output"].(map[string]any)
}

func invokeData(t *testing.T, handler http.Handler, capabilityID string, input map[string]any, wantStatus int) map[string]any {
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
	if err := json.NewEncoder(&raw).Encode(map[string]any{"input": input}); err != nil {
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
