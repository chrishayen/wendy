package leases

import (
	"encoding/json"
	"fmt"
	"os"

	"wendy/internal/contracts"
)

type resourceRegistrationFile struct {
	Resources []contracts.RegisterResourceRequest `json:"resources"`
}

func LoadResourceRegistrations(path string) ([]contracts.RegisterResourceRequest, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: resources path is required", ErrValidation)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var wrapped resourceRegistrationFile
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Resources != nil {
		return validateResourceRegistrations(wrapped.Resources)
	}
	var resources []contracts.RegisterResourceRequest
	if err := json.Unmarshal(raw, &resources); err != nil {
		return nil, err
	}
	return validateResourceRegistrations(resources)
}

func validateResourceRegistrations(resources []contracts.RegisterResourceRequest) ([]contracts.RegisterResourceRequest, error) {
	if len(resources) == 0 {
		return nil, fmt.Errorf("%w: resources must contain at least one resource", ErrValidation)
	}
	for i, resource := range resources {
		if resource.Selector == "" {
			return nil, fmt.Errorf("%w: resources[%d].selector is required", ErrValidation, i)
		}
		if resource.Status != "" && resource.Status != contracts.ResourceAvailable && resource.Status != contracts.ResourceUnavailable {
			return nil, fmt.Errorf("%w: resources[%d].status must be available or unavailable", ErrValidation, i)
		}
	}
	return resources, nil
}
