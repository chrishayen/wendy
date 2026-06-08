package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"pacp/internal/contracts"
)

type validationReport struct {
	OK    bool              `json:"ok"`
	Data  validationData    `json:"data"`
	Links map[string]any    `json:"links"`
	Meta  map[string]string `json:"meta"`
}

type validationData struct {
	Kind      string              `json:"kind"`
	Validated int                 `json:"validated"`
	Items     []validationItem    `json:"items,omitempty"`
	Findings  []validationFinding `json:"findings,omitempty"`
}

type validationItem struct {
	Path          string   `json:"path,omitempty"`
	ServiceID     string   `json:"service_id,omitempty"`
	CapabilityID  string   `json:"capability_id,omitempty"`
	CapabilityIDs []string `json:"capability_ids,omitempty"`
}

type validationFinding struct {
	Path         string `json:"path,omitempty"`
	ServiceID    string `json:"service_id,omitempty"`
	CapabilityID string `json:"capability_id,omitempty"`
	Code         string `json:"code"`
	Message      string `json:"message"`
}

type manifestFile struct {
	Path     string
	Manifest contracts.ProviderManifest
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usage(stderr)
	}
	switch args[0] {
	case "manifest":
		return validateManifestCommand(args[1:], stdout, stderr)
	case "provider-invoke":
		return validateInvokeCommand("provider-invoke", args[1:], stdout, stderr)
	case "tool-invoke":
		return validateInvokeCommand("tool-invoke", args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return usage(stderr)
	}
}

func validateManifestCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("manifest", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	paths := flags.Args()
	if len(paths) == 0 {
		fmt.Fprintln(stderr, "usage: pacp-validate manifest <manifest-file-or-dir> [...]")
		return 2
	}

	report := newReport("manifest")
	for _, path := range paths {
		manifests, findings := loadManifestPath(path)
		report.Data.Findings = append(report.Data.Findings, findings...)
		for _, entry := range manifests {
			report.Data.Validated++
			report.Data.Items = append(report.Data.Items, validationItem{
				Path:          entry.Path,
				ServiceID:     entry.Manifest.Service.ID,
				CapabilityIDs: capabilityIDs(entry.Manifest),
			})
			for _, message := range contracts.ValidateProviderManifest(entry.Manifest) {
				report.Data.Findings = append(report.Data.Findings, validationFinding{
					Path:      entry.Path,
					ServiceID: entry.Manifest.Service.ID,
					Code:      "manifest_invalid",
					Message:   message,
				})
			}
		}
	}
	return finishReport(stdout, stderr, report)
}

func validateInvokeCommand(kind string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(kind, flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifestPath := flags.String("manifest", "", "provider manifest file or directory")
	capabilityID := flags.String("capability", "", "capability id whose input schema should validate the payload")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *manifestPath == "" || *capabilityID == "" || flags.NArg() != 1 {
		fmt.Fprintf(stderr, "usage: pacp-validate %s -manifest <manifest-file-or-dir> -capability <capability-id> <payload.json>\n", kind)
		return 2
	}

	report := newReport(kind)
	manifests, findings := loadManifestPath(*manifestPath)
	report.Data.Findings = append(report.Data.Findings, findings...)
	capability, manifest, capabilityFindings := findCapability(manifests, *capabilityID)
	report.Data.Findings = append(report.Data.Findings, capabilityFindings...)
	if capability.ID != "" {
		report.Data.Items = append(report.Data.Items, validationItem{
			Path:         manifest.Path,
			ServiceID:    manifest.Manifest.Service.ID,
			CapabilityID: capability.ID,
		})
	}

	payloadPath := flags.Arg(0)
	input, payloadFindings := readInvocationInput(kind, payloadPath)
	report.Data.Findings = append(report.Data.Findings, payloadFindings...)
	if capability.ID != "" && input != nil {
		report.Data.Validated++
		if err := contracts.ValidateObject(input, capability.InputSchema); err != nil {
			report.Data.Findings = append(report.Data.Findings, validationFinding{
				Path:         payloadPath,
				ServiceID:    manifest.Manifest.Service.ID,
				CapabilityID: capability.ID,
				Code:         "input_schema_invalid",
				Message:      err.Error(),
			})
		}
	}
	return finishReport(stdout, stderr, report)
}

func usage(stderr io.Writer) int {
	fmt.Fprintln(stderr, "usage: pacp-validate <manifest|provider-invoke|tool-invoke> [options]")
	fmt.Fprintln(stderr, "  manifest <manifest-file-or-dir> [...]")
	fmt.Fprintln(stderr, "  provider-invoke -manifest <manifest-file-or-dir> -capability <capability-id> <payload.json>")
	fmt.Fprintln(stderr, "  tool-invoke -manifest <manifest-file-or-dir> -capability <capability-id> <payload.json>")
	return 2
}

func newReport(kind string) validationReport {
	return validationReport{
		OK:    true,
		Data:  validationData{Kind: kind},
		Links: map[string]any{},
		Meta:  map[string]string{"schema_version": "v1"},
	}
}

func finishReport(stdout, stderr io.Writer, report validationReport) int {
	sortFindings(report.Data.Findings)
	report.OK = len(report.Data.Findings) == 0
	if err := writeJSON(stdout, report); err != nil {
		fmt.Fprintf(stderr, "write report: %v\n", err)
		return 1
	}
	if report.OK {
		return 0
	}
	return 1
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func loadManifestPath(path string) ([]manifestFile, []validationFinding) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, []validationFinding{{Path: path, Code: "manifest_path_unreadable", Message: err.Error()}}
	}
	if !info.IsDir() {
		manifest, finding := readManifestFile(path)
		if finding != nil {
			return nil, []validationFinding{*finding}
		}
		return []manifestFile{{Path: path, Manifest: manifest}}, nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, []validationFinding{{Path: path, Code: "manifest_directory_unreadable", Message: err.Error()}}
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, []validationFinding{{Path: path, Code: "manifest_directory_empty", Message: "manifest directory contains no .json files"}}
	}

	manifests := make([]manifestFile, 0, len(names))
	findings := []validationFinding{}
	for _, name := range names {
		fullPath := filepath.Join(path, name)
		manifest, finding := readManifestFile(fullPath)
		if finding != nil {
			findings = append(findings, *finding)
			continue
		}
		manifests = append(manifests, manifestFile{Path: fullPath, Manifest: manifest})
	}
	return manifests, findings
}

func readManifestFile(path string) (contracts.ProviderManifest, *validationFinding) {
	file, err := os.Open(path)
	if err != nil {
		return contracts.ProviderManifest{}, &validationFinding{Path: path, Code: "manifest_unreadable", Message: err.Error()}
	}
	defer file.Close()
	var manifest contracts.ProviderManifest
	if err := json.NewDecoder(file).Decode(&manifest); err != nil {
		return contracts.ProviderManifest{}, &validationFinding{Path: path, Code: "manifest_json_invalid", Message: err.Error()}
	}
	return manifest, nil
}

func findCapability(manifests []manifestFile, capabilityID string) (contracts.Capability, manifestFile, []validationFinding) {
	if capabilityID == "" {
		return contracts.Capability{}, manifestFile{}, []validationFinding{{Code: "capability_required", Message: "capability id is required"}}
	}
	matches := []struct {
		manifest   manifestFile
		capability contracts.Capability
	}{}
	for _, manifest := range manifests {
		for _, capability := range manifest.Manifest.Capabilities {
			if capability.ID == capabilityID {
				matches = append(matches, struct {
					manifest   manifestFile
					capability contracts.Capability
				}{manifest: manifest, capability: capability})
			}
		}
	}
	switch len(matches) {
	case 0:
		return contracts.Capability{}, manifestFile{}, []validationFinding{{
			CapabilityID: capabilityID,
			Code:         "capability_not_found",
			Message:      "capability id was not found in the manifest input",
		}}
	case 1:
		return matches[0].capability, matches[0].manifest, nil
	default:
		return contracts.Capability{}, manifestFile{}, []validationFinding{{
			CapabilityID: capabilityID,
			Code:         "capability_not_unique",
			Message:      "capability id appears in more than one manifest",
		}}
	}
}

func readInvocationInput(kind, path string) (map[string]any, []validationFinding) {
	file, err := os.Open(path)
	if err != nil {
		return nil, []validationFinding{{Path: path, Code: "payload_unreadable", Message: err.Error()}}
	}
	defer file.Close()

	var object map[string]any
	decoder := json.NewDecoder(file)
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil {
		return nil, []validationFinding{{Path: path, Code: "payload_json_invalid", Message: err.Error()}}
	}
	inputRaw, ok := object["input"]
	if !ok {
		return nil, []validationFinding{{Path: path, Code: "payload_missing_input", Message: kind + " payload must include an input object"}}
	}
	input, ok := inputRaw.(map[string]any)
	if !ok {
		return nil, []validationFinding{{Path: path, Code: "payload_input_invalid", Message: "input must be a JSON object"}}
	}
	if dryRun, exists := object["dry_run"]; exists {
		if _, ok := dryRun.(bool); !ok {
			return nil, []validationFinding{{Path: path, Code: "payload_dry_run_invalid", Message: "dry_run must be a boolean when present"}}
		}
	}
	if kind == "tool-invoke" {
		if preferredMode, exists := object["preferred_mode"]; exists {
			if _, ok := preferredMode.(string); !ok {
				return nil, []validationFinding{{Path: path, Code: "payload_preferred_mode_invalid", Message: "preferred_mode must be a string when present"}}
			}
		}
	}
	if kind == "provider-invoke" {
		if contextValue, exists := object["context"]; exists {
			if _, ok := contextValue.(map[string]any); !ok {
				return nil, []validationFinding{{Path: path, Code: "payload_context_invalid", Message: "context must be an object when present"}}
			}
		}
	}
	return input, nil
}

func capabilityIDs(manifest contracts.ProviderManifest) []string {
	ids := make([]string, 0, len(manifest.Capabilities))
	for _, capability := range manifest.Capabilities {
		ids = append(ids, capability.ID)
	}
	sort.Strings(ids)
	return ids
}

func sortFindings(findings []validationFinding) {
	sort.SliceStable(findings, func(i, j int) bool {
		left := findings[i]
		right := findings[j]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.ServiceID != right.ServiceID {
			return left.ServiceID < right.ServiceID
		}
		if left.CapabilityID != right.CapabilityID {
			return left.CapabilityID < right.CapabilityID
		}
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		return left.Message < right.Message
	})
}
