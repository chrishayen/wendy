package testkit

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
		validatePackageBinaryFixtures(s.Root, pkg, &report)
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

func validatePackageBinaryFixtures(root string, pkg FixturePackage, report *contracts.Report) {
	for _, rel := range pkg.BinaryFixtures {
		validateBase64Fixture(root, rel, pkg.Path, "", "manifest_binary_fixture", report)
	}
	packageDir := filepath.Dir(pkg.AbsPath)
	for _, fixture := range pkg.File.Fixtures {
		validateFixtureBinaryRefs(packageDir, pkg.Path, fixture.ID, fixture, report)
	}
}

func validateFixtureBinaryRefs(packageDir, path, fixtureID string, fixture contracts.Fixture, report *contracts.Report) {
	validateRequestBinaryRef(packageDir, path, fixtureID, fixture.Request, report)
	validateResponseBinaryRef(packageDir, path, fixtureID, fixture.Response, report)
	for _, step := range fixture.Steps {
		validateStepBinaryRefs(packageDir, path, fixtureID, step, report)
	}
	if fixture.TimeoutInvoke != nil {
		validateStepBinaryRefs(packageDir, path, fixtureID, *fixture.TimeoutInvoke, report)
	}
	for _, step := range fixture.TimeoutCleanup {
		validateStepBinaryRefs(packageDir, path, fixtureID, step, report)
	}
}

func validateStepBinaryRefs(packageDir, path, fixtureID string, step contracts.OrchestrationStep, report *contracts.Report) {
	validateRequestBinaryRef(packageDir, path, fixtureID, step.Request, report)
	validateResponseBinaryRef(packageDir, path, fixtureID, step.Response, report)
}

func validateRequestBinaryRef(packageDir, path, fixtureID string, req *contracts.HTTPRequest, report *contracts.Report) {
	if req == nil || req.BodyFixture == "" {
		return
	}
	validateBase64Fixture(packageDir, req.BodyFixture, path, fixtureID, "request_body_fixture", report)
}

func validateResponseBinaryRef(packageDir, path, fixtureID string, resp *contracts.HTTPResponse, report *contracts.Report) {
	if resp == nil || resp.BodyFixture == "" {
		return
	}
	validateBase64Fixture(packageDir, resp.BodyFixture, path, fixtureID, "response_body_fixture", report)
}

func validateBase64Fixture(root, rel, reportPath, fixtureID, kind string, report *contracts.Report) {
	absPath, err := cleanJoin(root, rel)
	if err != nil {
		report.Findings = append(report.Findings, contracts.Finding{
			File: reportPath, Fixture: fixtureID, Code: kind + "_path_invalid", Message: err.Error(),
		})
		return
	}
	raw, err := os.ReadFile(absPath)
	if err != nil {
		report.Findings = append(report.Findings, contracts.Finding{
			File: reportPath, Fixture: fixtureID, Code: kind + "_read_failed", Message: err.Error(),
		})
		return
	}
	if _, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw))); err != nil {
		report.Findings = append(report.Findings, contracts.Finding{
			File: reportPath, Fixture: fixtureID, Code: kind + "_invalid_base64", Message: err.Error(),
		})
	}
}
