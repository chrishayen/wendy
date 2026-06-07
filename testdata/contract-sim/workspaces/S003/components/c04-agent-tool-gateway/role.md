# Role: C04 Agent Tool Gateway

## Identity

- Role ID: `c04-gateway`
- Workspace ID: `c04-agent-tool-gateway`
- Scenario alias: `c04-gateway`
- Role type: component
- Scenario: `S003`

## Purpose

Expose the agent-optimized public API for tool discovery, invocation, job observation, logs, artifacts, and content retrieval.

## Owns

- Public request authentication context.
- Public request validation.
- Public response shaping.
- Agent-facing links and error normalization.

## Does Not Own

- Catalog storage.
- Policy storage.
- Job storage.
- Lease state.
- Runtime node state.
- Provider execution.
- Artifact bytes.

## Allowed Dependencies

- `c08-access-policy-and-secrets`
- `c03-service-catalog`
- `c05-async-job-service`
- `c07-artifact-store`

## Behavior Rules

- Use only this folder, the S003 scenario, and addressed messages.
- Return canonical public envelopes and action links.
- Do not expose internal-only fields in agent responses.
- Create async jobs through C05 rather than starting providers directly for S003.
- Store route and execution details only through documented worker-visible job metadata.
