package contracts

import (
	"strings"
	"testing"
)

func TestValidateProviderManifestRequiresCapabilityContractFields(t *testing.T) {
	manifest := validTestManifest()
	manifest.Capabilities[0].Examples = nil
	manifest.Capabilities[0].ResourceHints = nil
	manifest.Capabilities[0].ArtifactHints = nil

	errs := ValidateProviderManifest(manifest)
	for _, want := range []string{
		"capabilities[0].examples is required",
		"capabilities[0].resource_hints is required",
		"capabilities[0].artifact_hints is required",
	} {
		if !containsValidationError(errs, want) {
			t.Fatalf("errors missing %q: %#v", want, errs)
		}
	}
}

func TestValidateProviderManifestRejectsInvalidCapabilityEnums(t *testing.T) {
	manifest := validTestManifest()
	manifest.Capabilities[0].ExecutionMode = "streaming"
	manifest.Capabilities[0].SideEffects = "network"

	errs := ValidateProviderManifest(manifest)
	for _, want := range []string{
		"capabilities[0].execution_mode must be sync, async, or either",
		"capabilities[0].side_effects must be none, read, write, external, or destructive",
	} {
		if !containsValidationError(errs, want) {
			t.Fatalf("errors missing %q: %#v", want, errs)
		}
	}
}

func TestValidateProviderManifestValidatesExamplesAgainstInputSchema(t *testing.T) {
	manifest := validTestManifest()
	manifest.Capabilities[0].InputSchema = map[string]any{
		"type":     "object",
		"required": []any{"message"},
		"properties": map[string]any{
			"message": map[string]any{"type": "string"},
		},
	}
	manifest.Capabilities[0].Examples = []map[string]any{{"message": 123}}

	errs := ValidateProviderManifest(manifest)
	want := "capabilities[0].examples[0] must match input_schema: message must be string"
	if !containsValidationError(errs, want) {
		t.Fatalf("errors missing %q: %#v", want, errs)
	}
}

func TestValidateProviderManifestRejectsUnsupportedTopLevelSchemaTypes(t *testing.T) {
	manifest := validTestManifest()
	manifest.Capabilities[0].InputSchema = map[string]any{"type": "array"}
	manifest.Capabilities[0].OutputSchema = map[string]any{"type": "string"}

	errs := ValidateProviderManifest(manifest)
	for _, want := range []string{
		"capabilities[0].input_schema.type must be object when present",
		"capabilities[0].output_schema.type must be object when present",
	} {
		if !containsValidationError(errs, want) {
			t.Fatalf("errors missing %q: %#v", want, errs)
		}
	}
}

func validTestManifest() ProviderManifest {
	return ProviderManifest{
		SchemaVersion: "v1",
		Service: Service{
			ID:           "svc_contract_test",
			Name:         "Contract Test Provider",
			Description:  "Provider used by contract validation tests.",
			Version:      "v1",
			ProviderKind: "test",
		},
		Provider: Provider{Endpoint: "http://provider.example"},
		Capabilities: []Capability{{
			ID:            "cap_contract_test",
			Name:          "Contract test",
			Description:   "Return a small contract test result.",
			ExecutionMode: "sync",
			InputSchema:   map[string]any{"type": "object"},
			OutputSchema:  map[string]any{"type": "object"},
			Examples:      []map[string]any{},
			SideEffects:   "none",
			ResourceHints: []ResourceHint{},
			ArtifactHints: []ArtifactHint{},
			TimeoutHint:   "30s",
		}},
	}
}

func containsValidationError(errs []string, want string) bool {
	for _, err := range errs {
		if strings.Contains(err, want) {
			return true
		}
	}
	return false
}
