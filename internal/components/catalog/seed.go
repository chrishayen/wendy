package catalog

import "pacp/internal/contracts"

func S003Manifest() contracts.ProviderManifest {
	return contracts.ProviderManifest{
		SchemaVersion: "v1",
		Service: contracts.Service{
			ID:           "svc_comfyui_gpu",
			Name:         "ComfyUI GPU Provider",
			Description:  "Node-managed image generation provider.",
			Version:      "v1",
			ProviderKind: "comfyui",
			Tags:         []string{"image", "gpu"},
		},
		Provider: contracts.Provider{
			Endpoint:   "http://node_linux_gpu:8188",
			NodeID:     "node_linux_gpu",
			HealthPath: "/v1/provider/health",
		},
		Capabilities: []contracts.Capability{
			{
				ID:            "cap_image_generate_gpu",
				Name:          "GPU image generation",
				Description:   "Generate an image using a GPU-backed provider.",
				ExecutionMode: "async",
				InputSchema: map[string]any{
					"type":     "object",
					"required": []any{"prompt"},
					"properties": map[string]any{
						"prompt": map[string]any{"type": "string"},
						"width":  map[string]any{"type": "integer"},
						"height": map[string]any{"type": "integer"},
						"seed":   map[string]any{"type": "integer"},
					},
				},
				OutputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"artifact_refs": map[string]any{
							"type":  "array",
							"items": map[string]any{"type": "string"},
						},
					},
				},
				Examples:    []map[string]any{},
				SideEffects: "external",
				ResourceHints: []contracts.ResourceHint{
					{Selector: "gpu", Required: true, Quantity: 1},
				},
				ArtifactHints: []contracts.ArtifactHint{
					{MediaType: "image/png", Count: "one"},
				},
				TimeoutHint: "15m",
			},
		},
	}
}

func NewS003Store() (*Store, error) {
	store := NewStore()
	_, err := store.RegisterManifest(S003Manifest())
	return store, err
}
