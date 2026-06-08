package contracts

import (
	"fmt"
	"strings"
)

func ValidateProviderManifest(manifest ProviderManifest) []string {
	var errs []string
	if manifest.SchemaVersion != "v1" {
		errs = append(errs, "schema_version must be v1")
	}
	if !strings.HasPrefix(manifest.Service.ID, "svc_") {
		errs = append(errs, "service.id must start with svc_")
	}
	if manifest.Service.Name == "" {
		errs = append(errs, "service.name is required")
	}
	if manifest.Service.Description == "" {
		errs = append(errs, "service.description is required")
	}
	if manifest.Service.Version == "" {
		errs = append(errs, "service.version is required")
	}
	if manifest.Service.ProviderKind == "" {
		errs = append(errs, "service.provider_kind is required")
	}
	if manifest.Provider.Endpoint == "" {
		errs = append(errs, "provider.endpoint is required")
	}
	if len(manifest.Capabilities) == 0 {
		errs = append(errs, "capabilities must contain at least one capability")
	}
	seenCapabilities := map[string]struct{}{}
	for i, cap := range manifest.Capabilities {
		prefix := fmt.Sprintf("capabilities[%d]", i)
		if !strings.HasPrefix(cap.ID, "cap_") {
			errs = append(errs, prefix+".id must start with cap_")
		}
		if _, exists := seenCapabilities[cap.ID]; exists {
			errs = append(errs, prefix+".id duplicates another capability")
		}
		seenCapabilities[cap.ID] = struct{}{}
		if cap.Name == "" {
			errs = append(errs, prefix+".name is required")
		}
		if cap.Description == "" {
			errs = append(errs, prefix+".description is required")
		}
		if cap.ExecutionMode == "" {
			errs = append(errs, prefix+".execution_mode is required")
		} else if !validExecutionMode(cap.ExecutionMode) {
			errs = append(errs, prefix+".execution_mode must be sync, async, or either")
		}
		if cap.InputSchema == nil {
			errs = append(errs, prefix+".input_schema is required")
		} else {
			errs = append(errs, validateCapabilityObjectSchema(prefix+".input_schema", cap.InputSchema)...)
		}
		if cap.OutputSchema == nil {
			errs = append(errs, prefix+".output_schema is required")
		} else {
			errs = append(errs, validateCapabilityObjectSchema(prefix+".output_schema", cap.OutputSchema)...)
		}
		if cap.Examples == nil {
			errs = append(errs, prefix+".examples is required")
		} else if cap.InputSchema != nil {
			for j, example := range cap.Examples {
				if err := ValidateObject(example, cap.InputSchema); err != nil {
					errs = append(errs, fmt.Sprintf("%s.examples[%d] must match input_schema: %s", prefix, j, err.Error()))
				}
			}
		}
		if cap.SideEffects == "" {
			errs = append(errs, prefix+".side_effects is required")
		} else if !validSideEffects(cap.SideEffects) {
			errs = append(errs, prefix+".side_effects must be none, read, write, external, or destructive")
		}
		if cap.ResourceHints == nil {
			errs = append(errs, prefix+".resource_hints is required")
		}
		if cap.ArtifactHints == nil {
			errs = append(errs, prefix+".artifact_hints is required")
		}
		if cap.TimeoutHint == "" {
			errs = append(errs, prefix+".timeout_hint is required")
		}
	}
	return errs
}

func validExecutionMode(value string) bool {
	switch value {
	case "sync", "async", "either":
		return true
	default:
		return false
	}
}

func validateCapabilityObjectSchema(prefix string, schema map[string]any) []string {
	schemaType, _ := schema["type"].(string)
	if schemaType == "" || schemaType == "object" {
		return nil
	}
	return []string{prefix + ".type must be object when present"}
}

func validSideEffects(value string) bool {
	switch value {
	case "none", "read", "write", "external", "destructive":
		return true
	default:
		return false
	}
}
