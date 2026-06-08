package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"log"
	"net/http"

	"pacp/internal/contracts"
	"pacp/internal/provider"
)

func main() {
	addr := flag.String("addr", "localhost:18088", "listen address")
	endpoint := flag.String("endpoint", "http://localhost:18088", "provider endpoint advertised in manifest")
	flag.Parse()

	manifest := fakeManifest(*endpoint)
	server, err := provider.NewServer(manifest, map[string]provider.CapabilityHandler{
		"cap_echo":       echoHandler,
		"cap_fake_image": fakeImageHandler,
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("serving fake provider addr=%s", *addr)
	if err := http.ListenAndServe(*addr, server); err != nil {
		log.Fatal(err)
	}
}

func echoHandler(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
	return contracts.ProviderInvokeResponse{Output: map[string]any{"message": req.Input["message"]}}, nil
}

func fakeImageHandler(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
	body := []byte("artifact bytes")
	sum := sha256.Sum256(body)
	checksum := "sha256:" + hex.EncodeToString(sum[:])
	return contracts.ProviderInvokeResponse{
		Output: map[string]any{"artifact_count": 1},
		Artifacts: []contracts.ProviderArtifact{{
			Name:          "fake-image.txt",
			MediaType:     "text/plain",
			ContentBase64: base64.StdEncoding.EncodeToString(body),
			Checksum:      checksum,
		}},
	}, nil
}

func fakeManifest(endpoint string) contracts.ProviderManifest {
	return contracts.ProviderManifest{
		SchemaVersion: "v1",
		Service: contracts.Service{
			ID:           "svc_fake_provider",
			Name:         "Fake Provider",
			Description:  "Fake provider for local development and contract tests.",
			Version:      "0.1.0",
			ProviderKind: "fake",
			Tags:         []string{"fake", "development"},
		},
		Provider: contracts.Provider{Endpoint: endpoint, HealthPath: "/v1/provider/health"},
		Capabilities: []contracts.Capability{
			{
				ID:            "cap_echo",
				Name:          "Echo",
				Description:   "Echo a message for smoke tests.",
				Tags:          []string{"test"},
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
				Examples:      []map[string]any{{"message": "hello"}},
				SideEffects:   "none",
				ResourceHints: []contracts.ResourceHint{},
				ArtifactHints: []contracts.ArtifactHint{},
				TimeoutHint:   "30s",
			},
			{
				ID:            "cap_fake_image",
				Name:          "Fake image artifact",
				Description:   "Return a deterministic fake artifact payload for runner and artifact-store tests.",
				Tags:          []string{"test", "artifact"},
				ExecutionMode: "sync",
				InputSchema: map[string]any{
					"type":     "object",
					"required": []any{"prompt"},
					"properties": map[string]any{
						"prompt": map[string]any{"type": "string"},
					},
				},
				OutputSchema: map[string]any{
					"type":     "object",
					"required": []any{"artifact_count"},
					"properties": map[string]any{
						"artifact_count": map[string]any{"type": "integer"},
					},
				},
				Examples:      []map[string]any{{"prompt": "red mug"}},
				SideEffects:   "external",
				ResourceHints: []contracts.ResourceHint{},
				ArtifactHints: []contracts.ArtifactHint{{MediaType: "text/plain", Count: "one"}},
				TimeoutHint:   "30s",
			},
		},
	}
}
