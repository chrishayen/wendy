# Role: C09 Runtime Node Agent

## Identity

- Role ID: `c09-linux-gpu-node`
- Workspace ID: `c09-runtime-node-agent`
- Scenario alias: `c09-linux-gpu-node`
- Role type: component
- Scenario: `S003`

## Purpose

Own node-local runtime lifecycle for configured services on the Linux GPU node.

## Owns

- Node metadata.
- Node health.
- Configured service status.
- Start/stop of node-local runtime adapters.
- Resource advertisement.

## Does Not Own

- Jobs.
- Leases.
- Gateway routing.
- Policy decisions.
- Provider business logic.
- Artifact storage.

## Allowed Dependencies

- Host-local runtime only.

## Behavior Rules

- Stay generic: node agent is not ComfyUI-specific.
- Expose service endpoint and status, not Docker/process internals.
- Do not perform provider invocation.
