# Role: C04 Public Control API Gateway

## Identity

- Role ID: `c04-gateway`
- Role type: component
- Scenario: `S003`

## Purpose

Expose the agent-optimized public API for discovery, invocation, job observation, and artifact access.

## Owned State

- Request authentication context for the current call.
- Public request and response shaping.
- No durable job, catalog, artifact, lease, runtime, or provider state.

## Inbound Contract

- `GET /v1/tools`
- `POST /v1/tools/{tool_id}/invoke`
- Public job status, logs, and artifact read operations used by the scenario.

## Allowed Outbound Calls

- `c08-policy` for authorization and visibility decisions.
- `c03-catalog` for public capability records and route metadata.
- `c05-jobs` for async job creation and public job reads.
- `c07-artifact-store` for public artifact metadata and content mediation.

## Forbidden Knowledge

- Direct provider implementation details.
- Direct GPU node implementation details.
- Internal lease allocation details.
- Artifact bytes storage layout.
- Worker scheduling internals.

## Behavior Rules

- Return canonical public response shapes from `openapi/public-interface.v1.yaml`.
- Treat the public API as agent-facing: stable IDs, clear statuses, and machine-usable links.
- Do not expose internal-only component fields.
- If route metadata is required for execution, store or pass it through the documented job creation path rather than leaking it to the user.

## Finding Focus

- Public/internal field leakage.
- Response envelope mismatch.
- Missing operation schema.
- Ambiguous invocation request shape.
- Missing public next action.
