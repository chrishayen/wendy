package node

import "wendy/internal/contracts"

func LeaseResourceRegistrations(cfg contracts.NodeConfig) []contracts.RegisterResourceRequest {
	resources := make([]contracts.RegisterResourceRequest, 0, len(cfg.Resources))
	for _, resource := range cfg.Resources {
		metadata := cloneMetadata(resource.Metadata)
		resources = append(resources, contracts.RegisterResourceRequest{
			ResourceID:  resource.ResourceID,
			Selector:    resourceSelector(resource),
			DisplayName: resourceDisplayName(cfg, resource),
			Status:      contracts.ResourceAvailable,
			NodeID:      cfg.NodeID,
			Tags:        append([]string(nil), resource.Tags...),
			Metadata:    metadata,
		})
	}
	return resources
}

func resourceSelector(resource contracts.NodeResource) string {
	if selector, ok := resource.Metadata["selector"].(string); ok && selector != "" {
		return selector
	}
	for _, tag := range resource.Tags {
		if tag != "" {
			return tag
		}
	}
	return resource.ResourceID
}

func resourceDisplayName(cfg contracts.NodeConfig, resource contracts.NodeResource) string {
	if cfg.DisplayName == "" {
		return resource.ResourceID
	}
	if len(cfg.Resources) == 1 {
		return cfg.DisplayName
	}
	return cfg.DisplayName + " " + resource.ResourceID
}

func cloneMetadata(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
