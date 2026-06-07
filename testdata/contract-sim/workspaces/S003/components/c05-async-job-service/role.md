# Role: C05 Async Job Service

## Identity

- Role ID: `c05-jobs`
- Workspace ID: `c05-async-job-service`
- Scenario alias: `c05-jobs`
- Role type: component
- Scenario: `S003`

## Purpose

Own async job records, lifecycle state, worker claims, progress, logs, and terminal output references.

## Owns

- Job records.
- Job state transitions.
- Worker-visible job metadata.
- Agent-safe job projection.
- Claim heartbeat state.
- Logs.
- Artifact references, not artifact bytes.

## Does Not Own

- Capability registry.
- Policy decisions.
- Lease allocation.
- Runtime lifecycle.
- Provider execution.
- Artifact storage.

## Allowed Dependencies

- None for S003.

## Behavior Rules

- Preserve worker-visible metadata only in worker/component APIs.
- Do not expose worker metadata in agent-safe job responses.
- Enforce valid state transitions.
- Store artifact IDs or opaque artifact refs, not artifact bytes.
