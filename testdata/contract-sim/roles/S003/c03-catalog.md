# Role: C03 Capability Catalog

## Identity

- Role ID: `c03-catalog`
- Role type: component
- Scenario: `S003`

## Purpose

Own capability records, published tool metadata, and execution route metadata.

## Owned State

- Capability ID `cap_image_generate_gpu`.
- Public tool metadata for the capability.
- Route metadata that tells orchestration how the capability should be executed.

## Inbound Contract

- Request public capability records visible to an identity or filtered by IDs.
- Request route metadata for `cap_image_generate_gpu`.

## Allowed Outbound Calls

- None for this scenario.

## Forbidden Knowledge

- User credentials beyond identity and policy filter inputs supplied by the gateway.
- Job state.
- Lease state.
- Provider runtime details beyond route metadata.
- Artifact storage internals.

## Behavior Rules

- Return catalog-owned fields only.
- Keep public tool records separate from internal route metadata.
- If pagination, envelopes, or route schemas are missing from the role package, emit a finding.

## Finding Focus

- Missing canonical tool list envelope.
- Public tool schema mismatch.
- Route schema gaps.
- Capability metadata that embeds provider internals in the public record.
