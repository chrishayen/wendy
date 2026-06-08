package deploy

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestRenderBundleProducesNativeComponentInputs(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/deploy/generic-gpu-bundle.json")
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	bundle, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse bundle: %v", err)
	}
	rendered, err := Render(bundle)
	if err != nil {
		t.Fatalf("render bundle: %v", err)
	}

	if rendered.NodeConfig.NodeID != "node_linux_gpu" {
		t.Fatalf("node id = %q", rendered.NodeConfig.NodeID)
	}
	if len(rendered.NodeConfig.Services) != 1 {
		t.Fatalf("service count = %d", len(rendered.NodeConfig.Services))
	}
	service := rendered.NodeConfig.Services[0]
	if service.ServiceID != "svc_generic_gpu_image" {
		t.Fatalf("service id = %q", service.ServiceID)
	}
	if service.ProviderEndpoint != "http://linux-gpu-node:18088" {
		t.Fatalf("provider endpoint = %q", service.ProviderEndpoint)
	}
	if service.Manifest == nil {
		t.Fatal("service manifest was not embedded in node config")
	}
	if service.Manifest.Provider.NodeID != "node_linux_gpu" {
		t.Fatalf("manifest provider node id = %q", service.Manifest.Provider.NodeID)
	}
	if service.Manifest.Provider.HealthPath != defaultHealthPath {
		t.Fatalf("manifest health path = %q", service.Manifest.Provider.HealthPath)
	}
	if service.Manifest.Capabilities[0].ServiceID != "svc_generic_gpu_image" {
		t.Fatalf("capability service id = %q", service.Manifest.Capabilities[0].ServiceID)
	}

	if len(rendered.ResourceSeed.Resources) != 1 {
		t.Fatalf("resource seed count = %d", len(rendered.ResourceSeed.Resources))
	}
	resource := rendered.ResourceSeed.Resources[0]
	if resource.Selector != "gpu" {
		t.Fatalf("resource selector = %q", resource.Selector)
	}
	if resource.Status != "available" {
		t.Fatalf("resource status = %q", resource.Status)
	}
	if resource.NodeID != "node_linux_gpu" {
		t.Fatalf("resource node id = %q", resource.NodeID)
	}

	files, err := rendered.Files()
	if err != nil {
		t.Fatalf("files: %v", err)
	}
	wantPaths := map[string]bool{
		"node/node.json":                              false,
		"leases/resources.json":                       false,
		"catalog/svc_generic_gpu_image.manifest.json": false,
		"policy/policy-seed.json":                     false,
	}
	for _, file := range files {
		if _, ok := wantPaths[file.Path]; ok {
			wantPaths[file.Path] = true
		}
		if !json.Valid(file.Data) {
			t.Fatalf("%s is not valid JSON", file.Path)
		}
	}
	for path, found := range wantPaths {
		if !found {
			t.Fatalf("missing rendered path %s", path)
		}
	}
}

func TestRenderRejectsDuplicateCapabilitiesAcrossServices(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/deploy/generic-gpu-bundle.json")
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	bundle, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse bundle: %v", err)
	}
	duplicate := bundle.Services[0]
	duplicate.Manifest.Service.ID = "svc_generic_gpu_image_alt"
	bundle.Services = append(bundle.Services, duplicate)

	_, err = Render(bundle)
	if err == nil {
		t.Fatal("expected duplicate capability error")
	}
	if !strings.Contains(err.Error(), "duplicates another capability") {
		t.Fatalf("error did not mention duplicate capability: %v", err)
	}
}

func TestRenderRejectsProviderNodeMismatch(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/deploy/generic-gpu-bundle.json")
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	bundle, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse bundle: %v", err)
	}
	bundle.Services[0].Manifest.Provider.NodeID = "node_other"

	_, err = Render(bundle)
	if err == nil {
		t.Fatal("expected node mismatch error")
	}
	if !strings.Contains(err.Error(), "provider.node_id must match") {
		t.Fatalf("error did not mention provider node mismatch: %v", err)
	}
}
