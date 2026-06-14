package provider

import (
	"fmt"
	"strings"

	"wendy/internal/contracts"
)

type ManifestBuilder struct {
	manifest contracts.ProviderManifest
	handlers map[string]CapabilityHandler
}

func NewManifestBuilder(service contracts.Service, providerInfo contracts.Provider) *ManifestBuilder {
	if providerInfo.HealthPath == "" {
		providerInfo.HealthPath = "/v1/provider/health"
	}
	return &ManifestBuilder{
		manifest: contracts.ProviderManifest{
			SchemaVersion: "v1",
			Service:       service,
			Provider:      providerInfo,
			Capabilities:  []contracts.Capability{},
		},
		handlers: map[string]CapabilityHandler{},
	}
}

func (b *ManifestBuilder) AddCapability(capability contracts.Capability, handler CapabilityHandler) *ManifestBuilder {
	if b == nil {
		return b
	}
	b.manifest.Capabilities = append(b.manifest.Capabilities, capability)
	b.handlers[capability.ID] = handler
	return b
}

func (b *ManifestBuilder) Build() (contracts.ProviderManifest, map[string]CapabilityHandler, error) {
	if b == nil {
		return contracts.ProviderManifest{}, nil, fmt.Errorf("%w: manifest builder is nil", ErrValidation)
	}
	manifest := b.manifest
	manifest.Capabilities = append([]contracts.Capability(nil), b.manifest.Capabilities...)
	handlers := make(map[string]CapabilityHandler, len(b.handlers))
	for id, handler := range b.handlers {
		handlers[id] = handler
	}

	errs := contracts.ValidateProviderManifest(manifest)
	for _, capability := range manifest.Capabilities {
		if handlers[capability.ID] == nil {
			errs = append(errs, "handlers."+capability.ID+" is required")
		}
	}
	if len(errs) > 0 {
		return contracts.ProviderManifest{}, nil, fmt.Errorf("%w: %s", ErrValidation, strings.Join(errs, "; "))
	}
	return manifest, handlers, nil
}

func (b *ManifestBuilder) Server() (*Server, error) {
	manifest, handlers, err := b.Build()
	if err != nil {
		return nil, err
	}
	return NewServer(manifest, handlers)
}
