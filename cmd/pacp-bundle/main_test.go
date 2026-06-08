package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"pacp/internal/contracts"
	"pacp/internal/deploy"
)

func TestBundleCommandWritesRenderedFiles(t *testing.T) {
	outDir := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{
		"-bundle", "../../testdata/deploy/generic-gpu-bundle.json",
		"-out-dir", outDir,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run code = %d stderr=%s", code, stderr.String())
	}

	var report renderReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v output=%s", err, stdout.String())
	}
	if !report.OK {
		t.Fatalf("report ok=false: %+v", report)
	}
	if len(report.Data.Files) != 4 {
		t.Fatalf("rendered file count = %d", len(report.Data.Files))
	}

	nodePath := filepath.Join(outDir, "node", "node.json")
	rawNode, err := os.ReadFile(nodePath)
	if err != nil {
		t.Fatalf("read node output: %v", err)
	}
	var nodeConfig contracts.NodeConfig
	if err := json.Unmarshal(rawNode, &nodeConfig); err != nil {
		t.Fatalf("decode node config: %v", err)
	}
	if nodeConfig.NodeID != "node_linux_gpu" {
		t.Fatalf("node id = %q", nodeConfig.NodeID)
	}

	manifestPath := filepath.Join(outDir, "catalog", "svc_generic_gpu_image.manifest.json")
	rawManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest output: %v", err)
	}
	var manifest contracts.ProviderManifest
	if err := json.Unmarshal(rawManifest, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.Provider.NodeID != "node_linux_gpu" {
		t.Fatalf("manifest node id = %q", manifest.Provider.NodeID)
	}

	rawResources, err := os.ReadFile(filepath.Join(outDir, "leases", "resources.json"))
	if err != nil {
		t.Fatalf("read resources output: %v", err)
	}
	var resources deploy.ResourceSeedFile
	if err := json.Unmarshal(rawResources, &resources); err != nil {
		t.Fatalf("decode resources: %v", err)
	}
	if len(resources.Resources) != 1 || resources.Resources[0].Selector != "gpu" {
		t.Fatalf("unexpected resources: %+v", resources.Resources)
	}
}

func TestBundleCommandRequiresBundlePath(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(nil, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run code = %d", code)
	}
	if stderr.String() == "" {
		t.Fatal("expected usage error on stderr")
	}
}
