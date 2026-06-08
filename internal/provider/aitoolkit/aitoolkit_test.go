package aitoolkit

import (
	"bytes"
	"encoding/base64"
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

	updatedPath := filepath.Join(workspace, "datasets", "updated-product")
	if err := os.MkdirAll(updatedPath, 0o700); err != nil {
		t.Fatalf("mkdir updated dataset: %v", err)
	}
	if err := os.WriteFile(filepath.Join(updatedPath, "image-a.png"), []byte("fake image"), 0o600); err != nil {
		t.Fatalf("write updated image a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(updatedPath, "image-b.webp"), []byte("fake image"), 0o600); err != nil {
		t.Fatalf("write updated image b: %v", err)
	}

	updated := invoke(t, server, DefaultDatasetUpdateCapability, map[string]any{
		"dataset_id": "product_photos",
		"name":       "Updated Product Photos",
		"path":       "datasets/updated-product",
		"metadata":   map[string]any{"source": "curated"},
	}, http.StatusOK)
	if updated["name"] != "Updated Product Photos" || updated["path"] != "datasets/updated-product" || updated["image_count"].(float64) != 2 {
		t.Fatalf("updated = %#v", updated)
	}
	metadata := updated["metadata"].(map[string]any)
	if metadata["source"] != "curated" {
		t.Fatalf("metadata = %#v", metadata)
	}

	reloaded := newTestServer(t, Config{Endpoint: "http://provider.local", WorkspaceRoot: workspace, DryRun: true})
	persisted := invoke(t, reloaded, DefaultDatasetInspectCapability, map[string]any{"dataset_id": "product_photos"}, http.StatusOK)
	if persisted["name"] != "Updated Product Photos" || persisted["image_count"].(float64) != 2 {
		t.Fatalf("persisted = %#v", persisted)
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

	_ = invoke(t, server, DefaultDatasetRegisterCapability, map[string]any{
		"dataset_id": "product_photos",
		"name":       "Product Photos",
		"path":       "datasets/product",
	}, http.StatusOK)
	updateEnvelope := invokeEnvelope(t, server, DefaultDatasetUpdateCapability, map[string]any{
		"dataset_id": "product_photos",
		"path":       outside,
	}, http.StatusBadRequest)
	updateErr := updateEnvelope["error"].(map[string]any)
	if updateErr["code"] != "validation_failed" || !strings.Contains(updateErr["message"].(string), "outside") {
		t.Fatalf("update error = %#v", updateErr)
	}
}

func TestRejectsDatasetUpdateWithoutMutation(t *testing.T) {
	workspace := newWorkspace(t)
	server := newTestServer(t, Config{Endpoint: "http://provider.local", WorkspaceRoot: workspace, DryRun: true})
	_ = invoke(t, server, DefaultDatasetRegisterCapability, map[string]any{
		"dataset_id": "product_photos",
		"name":       "Product Photos",
		"path":       "datasets/product",
	}, http.StatusOK)

	envelope := invokeEnvelope(t, server, DefaultDatasetUpdateCapability, map[string]any{
		"dataset_id": "product_photos",
	}, http.StatusBadRequest)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "validation_failed" || !strings.Contains(errObj["message"].(string), "at least one") {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestDatasetUploadAddsImageAndPersists(t *testing.T) {
	workspace := newWorkspace(t)
	server := newTestServer(t, Config{Endpoint: "http://provider.local", WorkspaceRoot: workspace, DryRun: true})
	_ = invoke(t, server, DefaultDatasetRegisterCapability, map[string]any{
		"dataset_id": "product_photos",
		"name":       "Product Photos",
		"path":       "datasets/product",
	}, http.StatusOK)

	body := []byte("fake webp image")
	output := invoke(t, server, DefaultDatasetUploadCapability, map[string]any{
		"dataset_id":     "product_photos",
		"filename":       "image-b.webp",
		"media_type":     "image/webp",
		"content_base64": base64.StdEncoding.EncodeToString(body),
	}, http.StatusOK)
	dataset := output["dataset"].(map[string]any)
	if dataset["image_count"].(float64) != 2 {
		t.Fatalf("dataset = %#v", dataset)
	}
	uploaded := output["uploaded"].(map[string]any)
	if uploaded["filename"] != "image-b.webp" || uploaded["media_type"] != "image/webp" || uploaded["checksum"] == "" || uploaded["size"].(float64) != float64(len(body)) {
		t.Fatalf("uploaded = %#v", uploaded)
	}
	storedPath := filepath.Join(workspace, "datasets", "product", "image-b.webp")
	stored, err := os.ReadFile(storedPath)
	if err != nil {
		t.Fatalf("read uploaded image: %v", err)
	}
	if string(stored) != string(body) {
		t.Fatalf("stored body = %q", stored)
	}

	reloaded := newTestServer(t, Config{Endpoint: "http://provider.local", WorkspaceRoot: workspace, DryRun: true})
	persisted := invoke(t, reloaded, DefaultDatasetInspectCapability, map[string]any{"dataset_id": "product_photos"}, http.StatusOK)
	if persisted["image_count"].(float64) != 2 {
		t.Fatalf("persisted = %#v", persisted)
	}
}

func TestDatasetUploadRejectsUnsafeFilename(t *testing.T) {
	workspace := newWorkspace(t)
	server := newTestServer(t, Config{Endpoint: "http://provider.local", WorkspaceRoot: workspace, DryRun: true})
	_ = invoke(t, server, DefaultDatasetRegisterCapability, map[string]any{
		"dataset_id": "product_photos",
		"name":       "Product Photos",
		"path":       "datasets/product",
	}, http.StatusOK)

	envelope := invokeEnvelope(t, server, DefaultDatasetUploadCapability, map[string]any{
		"dataset_id":     "product_photos",
		"filename":       "../escape.png",
		"content_base64": base64.StdEncoding.EncodeToString([]byte("fake image")),
	}, http.StatusBadRequest)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "validation_failed" || !strings.Contains(errObj["message"].(string), "filename") {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestDatasetUploadRejectsMediaTypeMismatch(t *testing.T) {
	workspace := newWorkspace(t)
	server := newTestServer(t, Config{Endpoint: "http://provider.local", WorkspaceRoot: workspace, DryRun: true})
	_ = invoke(t, server, DefaultDatasetRegisterCapability, map[string]any{
		"dataset_id": "product_photos",
		"name":       "Product Photos",
		"path":       "datasets/product",
	}, http.StatusOK)

	envelope := invokeEnvelope(t, server, DefaultDatasetUploadCapability, map[string]any{
		"dataset_id":     "product_photos",
		"filename":       "image-c.png",
		"media_type":     "image/webp",
		"content_base64": base64.StdEncoding.EncodeToString([]byte("fake image")),
	}, http.StatusBadRequest)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "validation_failed" || !strings.Contains(errObj["message"].(string), "media_type") {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestManifestIncludesIndexCapabilities(t *testing.T) {
	workspace := newWorkspace(t)
	server := newTestServer(t, Config{Endpoint: "http://provider.local", WorkspaceRoot: workspace, DryRun: true})
	req := httptest.NewRequest(http.MethodGet, "/v1/provider/manifest", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	data := envelope["data"].(map[string]any)
	capabilities := data["capabilities"].([]any)
	found := map[string]bool{}
	for _, raw := range capabilities {
		capability := raw.(map[string]any)
		id, _ := capability["id"].(string)
		found[id] = true
		if id == DefaultTrainCapability {
			inputSchema := capability["input_schema"].(map[string]any)
			properties := inputSchema["properties"].(map[string]any)
			preset := properties["preset"].(map[string]any)
			enum := preset["enum"].([]any)
			if len(enum) != 1 || enum[0] != defaultPreset {
				t.Fatalf("preset enum = %#v", enum)
			}
		}
	}
	for _, capabilityID := range []string{DefaultDatasetUploadCapability, DefaultDatasetUpdateCapability, DefaultOutputListCapability, DefaultOutputInspectCapability} {
		if !found[capabilityID] {
			t.Fatalf("capability %s missing from %#v", capabilityID, capabilities)
		}
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

func TestRejectsUnsupportedTrainingPreset(t *testing.T) {
	workspace := newWorkspace(t)
	server := newTestServer(t, Config{Endpoint: "http://provider.local", WorkspaceRoot: workspace, DryRun: true})
	_ = invoke(t, server, DefaultDatasetRegisterCapability, map[string]any{
		"dataset_id": "product_photos",
		"name":       "Product Photos",
		"path":       "datasets/product",
	}, http.StatusOK)

	envelope := invokeEnvelope(t, server, DefaultTrainCapability, map[string]any{
		"dataset_id":  "product_photos",
		"output_name": "product_lora",
		"preset":      "unsupported-lora",
	}, http.StatusBadRequest)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "validation_failed" || !strings.Contains(errObj["message"].(string), "preset") {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestTrainingOutputIndexListInspectAndPersistence(t *testing.T) {
	workspace := newWorkspace(t)
	server := newTestServer(t, Config{Endpoint: "http://provider.local", WorkspaceRoot: workspace, DryRun: true})
	_ = invoke(t, server, DefaultDatasetRegisterCapability, map[string]any{
		"dataset_id": "product_photos",
		"name":       "Product Photos",
		"path":       "datasets/product",
	}, http.StatusOK)
	_ = invoke(t, server, DefaultTrainCapability, map[string]any{
		"dataset_id":  "product_photos",
		"output_name": "product_lora",
		"steps":       12,
		"rank":        8,
	}, http.StatusOK)

	list := invoke(t, server, DefaultOutputListCapability, map[string]any{"dataset_id": "product_photos"}, http.StatusOK)
	if list["count"].(float64) != 1 {
		t.Fatalf("list = %#v", list)
	}
	items := list["items"].([]any)
	output := items[0].(map[string]any)
	if output["output_id"] != "lora_product_lora" || output["dry_run"] != true {
		t.Fatalf("output = %#v", output)
	}

	inspected := invoke(t, server, DefaultOutputInspectCapability, map[string]any{"output_id": "lora_product_lora"}, http.StatusOK)
	if inspected["dataset_id"] != "product_photos" || inspected["preset"] != defaultPreset || inspected["steps"].(float64) != 12 {
		t.Fatalf("inspected = %#v", inspected)
	}

	reloaded := newTestServer(t, Config{Endpoint: "http://provider.local", WorkspaceRoot: workspace, DryRun: true})
	persisted := invoke(t, reloaded, DefaultOutputInspectCapability, map[string]any{"output_id": "lora_product_lora"}, http.StatusOK)
	if persisted["output_name"] != "product_lora" || persisted["rank"].(float64) != 8 {
		t.Fatalf("persisted = %#v", persisted)
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
