package testkit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"pacp/internal/contracts"
)

type Manifest struct {
	ScenarioID      string               `json:"scenario_id"`
	LatestSourceRun string               `json:"latest_source_run"`
	Status          string               `json:"status"`
	FixtureSets     []ManifestFixtureSet `json:"fixture_sets"`
}

type ManifestFixtureSet struct {
	Owner          string   `json:"owner"`
	Path           string   `json:"path"`
	BinaryFixtures []string `json:"binary_fixtures,omitempty"`
}

type Scenario struct {
	Root     string
	Manifest Manifest
	Packages []FixturePackage
}

type FixturePackage struct {
	Owner          string
	Path           string
	AbsPath        string
	BinaryFixtures []string
	File           contracts.FixtureFile
}

func LoadScenario(root, manifestRel string) (Scenario, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return Scenario{}, err
	}

	manifestPath, err := cleanJoin(rootAbs, manifestRel)
	if err != nil {
		return Scenario{}, err
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return Scenario{}, err
	}

	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Scenario{}, fmt.Errorf("decode manifest: %w", err)
	}
	scenario := Scenario{Root: rootAbs, Manifest: manifest}
	for _, set := range manifest.FixtureSets {
		fixturePath, err := cleanJoin(rootAbs, set.Path)
		if err != nil {
			return Scenario{}, err
		}
		fixtureRaw, err := os.ReadFile(fixturePath)
		if err != nil {
			return Scenario{}, err
		}
		file, report := contracts.ValidateFixtureFile(set.Path, fixtureRaw)
		if !report.Passed() {
			return Scenario{}, fmt.Errorf("fixture %s failed base validation before load: %s", set.Path, report.Findings[0].Code)
		}
		scenario.Packages = append(scenario.Packages, FixturePackage{
			Owner:          set.Owner,
			Path:           set.Path,
			AbsPath:        fixturePath,
			BinaryFixtures: set.BinaryFixtures,
			File:           file,
		})
	}

	return scenario, nil
}

func ValidateScenario(s Scenario) contracts.Report {
	report := contracts.Report{}
	for _, pkg := range s.Packages {
		raw, err := os.ReadFile(pkg.AbsPath)
		if err != nil {
			report.Findings = append(report.Findings, contracts.Finding{
				File: pkg.Path, Code: "read_failed", Message: err.Error(),
			})
			continue
		}
		_, next := contracts.ValidateFixtureFile(pkg.Path, raw)
		report.Merge(next)
		if pkg.File.ScenarioID != s.Manifest.ScenarioID {
			report.Findings = append(report.Findings, contracts.Finding{
				File: pkg.Path, Code: "scenario_mismatch",
				Message: fmt.Sprintf("fixture scenario_id %q does not match manifest %q", pkg.File.ScenarioID, s.Manifest.ScenarioID),
			})
		}
	}
	return report
}

func FindPackage(s Scenario, owner string) (FixturePackage, bool) {
	for _, pkg := range s.Packages {
		if pkg.Owner == owner {
			return pkg, true
		}
	}
	return FixturePackage{}, false
}

func cleanJoin(root, rel string) (string, error) {
	cleanRel := filepath.Clean(rel)
	if filepath.IsAbs(cleanRel) || cleanRel == ".." || len(cleanRel) >= 3 && cleanRel[:3] == "../" {
		return "", fmt.Errorf("path %q escapes fixture root", rel)
	}
	return filepath.Join(root, cleanRel), nil
}
