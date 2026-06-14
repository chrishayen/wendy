package provider

import (
	"context"

	"wendy/internal/contracts"
)

type ArtifactCapabilityHandler func(context.Context, contracts.ProviderInvokeRequest) (ArtifactResult, error)

type ArtifactResult struct {
	Output      map[string]any
	Artifacts   []contracts.ProviderArtifact
	ContentRefs []contracts.ProviderContentRef
}

func ArtifactHandler(handler ArtifactCapabilityHandler) CapabilityHandler {
	return func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
		result, err := handler(ctx, req)
		if err != nil {
			return contracts.ProviderInvokeResponse{}, err
		}
		return contracts.ProviderInvokeResponse{
			Output:      result.Output,
			Artifacts:   result.Artifacts,
			ContentRefs: result.ContentRefs,
		}, nil
	}
}
