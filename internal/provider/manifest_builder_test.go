package provider

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"pacp/internal/contracts"
)

func TestManifestBuilderBuildsProviderServer(t *testing.T) {
	server, err := NewManifestBuilder(testBuilderService(), contracts.Provider{Endpoint: "http://localhost:18088"}).
		AddCapability(testBuilderEchoCapability(), func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
			return contracts.ProviderInvokeResponse{Output: map[string]any{"message": req.Input["message"]}}, nil
		}).
		Server()
	if err != nil {
		t.Fatalf("build server: %v", err)
	}

	manifest := doJSON(t, server, http.MethodGet, "/v1/provider/manifest", nil, http.StatusOK)
	providerData := manifest["provider"].(map[string]any)
	if providerData["health_path"] != "/v1/provider/health" {
		t.Fatalf("manifest provider = %#v", providerData)
	}
	response := doJSON(t, server, http.MethodPost, "/v1/provider/capabilities/cap_builder_echo/invoke", map[string]any{
		"input": map[string]any{"message": "hello"},
	}, http.StatusOK)
	output := response["output"].(map[string]any)
	if output["message"] != "hello" {
		t.Fatalf("invoke response = %#v", response)
	}
}

func TestManifestBuilderReturnsManifestAndHandlers(t *testing.T) {
	manifest, handlers, err := NewManifestBuilder(testBuilderService(), contracts.Provider{
		Endpoint:   "http://localhost:18088",
		HealthPath: "/healthz",
	}).
		AddCapability(testBuilderEchoCapability(), func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
			return contracts.ProviderInvokeResponse{Output: map[string]any{"message": req.Input["message"]}}, nil
		}).
		Build()
	if err != nil {
		t.Fatalf("build manifest: %v", err)
	}
	if manifest.SchemaVersion != "v1" || manifest.Provider.HealthPath != "/healthz" {
		t.Fatalf("manifest = %#v", manifest)
	}
	if handlers["cap_builder_echo"] == nil {
		t.Fatalf("handlers = %#v", handlers)
	}
}

func TestManifestBuilderRejectsMissingHandler(t *testing.T) {
	_, _, err := NewManifestBuilder(testBuilderService(), contracts.Provider{Endpoint: "http://localhost:18088"}).
		AddCapability(testBuilderEchoCapability(), nil).
		Build()
	if !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), "handlers.cap_builder_echo is required") {
		t.Fatalf("err = %v", err)
	}
}

func testBuilderService() contracts.Service {
	return contracts.Service{
		ID:           "svc_builder",
		Name:         "Builder",
		Description:  "Provider builder test service.",
		Version:      "0.1.0",
		ProviderKind: "test",
	}
}

func testBuilderEchoCapability() contracts.Capability {
	return contracts.Capability{
		ID:            "cap_builder_echo",
		Name:          "Builder Echo",
		Description:   "Echo capability declared through the manifest builder.",
		ExecutionMode: "sync",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []any{"message"},
			"properties": map[string]any{
				"message": map[string]any{"type": "string"},
			},
		},
		OutputSchema: map[string]any{
			"type":     "object",
			"required": []any{"message"},
			"properties": map[string]any{
				"message": map[string]any{"type": "string"},
			},
		},
		Examples:      []map[string]any{},
		SideEffects:   "none",
		ResourceHints: []contracts.ResourceHint{},
		ArtifactHints: []contracts.ArtifactHint{},
		TimeoutHint:   "30s",
	}
}
