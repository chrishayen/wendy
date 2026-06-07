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
		}
		if cap.InputSchema == nil {
			errs = append(errs, prefix+".input_schema is required")
		}
		if cap.OutputSchema == nil {
			errs = append(errs, prefix+".output_schema is required")
		}
		if cap.SideEffects == "" {
			errs = append(errs, prefix+".side_effects is required")
		}
		if cap.TimeoutHint == "" {
			errs = append(errs, prefix+".timeout_hint is required")
		}
	}
	return errs
}
