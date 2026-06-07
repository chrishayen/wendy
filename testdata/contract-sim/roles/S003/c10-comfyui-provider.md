# Role: C10 ComfyUI Provider Adapter

## Identity

- Role ID: `c10-comfyui-provider`
- Role type: component
- Scenario: `S003`

## Purpose

Adapt a concrete image generation backend to the generic provider contract.

## Owned State

- Provider health for this backend.
- Provider invocation mapping.
- Provider result normalization.

## Inbound Contract

- Check provider health.
- Execute one image generation request.

## Allowed Outbound Calls

- The ComfyUI backend or local provider process represented by this adapter.

## Forbidden Knowledge

- Public API auth.
- Policy rules.
- Job queue mechanics.
- Resource lease allocation.
- Artifact store internals.

## Behavior Rules

- Provider invocation is blocking from the composition runner perspective.
- Return normalized result data and local artifact references or bytes according to the provider contract.
- Do not create public jobs.
- If health, request, or result schema is missing, emit a finding.

## Finding Focus

- Blocking versus provider-side async confusion.
- Missing health response.
- Provider-specific result shape leaking beyond the adapter.
