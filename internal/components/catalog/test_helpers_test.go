package catalog

import (
	"path/filepath"
	"testing"

	"wendy/internal/contracts"
)

func sampleManifest(t *testing.T) contracts.ProviderManifest {
	t.Helper()
	manifest, err := LoadManifestFile(filepath.Join("..", "..", "..", "testdata", "manifests", "s003-comfyui-gpu.json"))
	if err != nil {
		t.Fatalf("load sample manifest: %v", err)
	}
	return manifest
}

func sampleStore(t *testing.T) *Store {
	t.Helper()
	store := NewStore()
	if _, err := store.RegisterManifest(sampleManifest(t)); err != nil {
		t.Fatalf("register sample manifest: %v", err)
	}
	return store
}
