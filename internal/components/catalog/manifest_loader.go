package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"pacp/internal/contracts"
)

func LoadManifestFile(path string) (contracts.ProviderManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return contracts.ProviderManifest{}, err
	}
	defer file.Close()

	var manifest contracts.ProviderManifest
	if err := json.NewDecoder(file).Decode(&manifest); err != nil {
		return contracts.ProviderManifest{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return manifest, nil
}

func LoadManifests(path string) ([]contracts.ProviderManifest, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		manifest, err := LoadManifestFile(path)
		if err != nil {
			return nil, err
		}
		return []contracts.ProviderManifest{manifest}, nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	var manifests []contracts.ProviderManifest
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		manifest, err := LoadManifestFile(filepath.Join(path, entry.Name()))
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, manifest)
	}
	return manifests, nil
}
