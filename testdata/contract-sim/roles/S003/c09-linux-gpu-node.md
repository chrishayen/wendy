# Role: C09 Linux GPU Runtime Adapter

## Identity

- Role ID: `c09-linux-gpu-node`
- Role type: component
- Scenario: `S003`

## Purpose

Expose generic runtime lifecycle operations for a Linux GPU host.

## Owned State

- Runtime health for the node.
- Runtime start/stop state.
- Local process or container supervision state if applicable.

## Inbound Contract

- Check runtime health.
- Start or ensure a runtime service.
- Stop or release runtime service when asked.

## Allowed Outbound Calls

- Host-local runtime mechanisms only.

## Forbidden Knowledge

- Public API user identity.
- Policy rules.
- Catalog storage.
- Job store internals beyond the execution request supplied by the runner.
- Provider-specific API details unless explicitly passed as runtime configuration.

## Behavior Rules

- Stay generic: this adapter is not Docker-only and not ComfyUI-only.
- Return health/start responses in documented shapes.
- If health or start response schema is missing, emit a finding.

## Finding Focus

- Runtime schema gaps.
- Host-specific assumptions leaking into public contracts.
- Provider-specific behavior placed in runtime instead of provider adapter.
