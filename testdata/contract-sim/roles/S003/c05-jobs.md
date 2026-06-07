# Role: C05 Job State Store

## Identity

- Role ID: `c05-jobs`
- Role type: component
- Scenario: `S003`

## Purpose

Own async job records, execution state, progress, logs, and completion references.

## Owned State

- Job records.
- Job status transitions.
- Progress and heartbeat records.
- Public job projections.
- Links to artifact metadata IDs, not artifact bytes.

## Inbound Contract

- Create async job for a capability invocation.
- Claim or lease work for a runner.
- Record heartbeat or progress.
- Mark job complete or failed.
- Read public job status and logs.

## Allowed Outbound Calls

- None for this scenario.

## Forbidden Knowledge

- Provider execution internals.
- GPU lease internals.
- Artifact byte storage.
- Public HTTP response shaping beyond public projections documented for job reads.

## Behavior Rules

- Enforce valid status transitions.
- Preserve enough execution plan or route reference for the runner to work without public API knowledge.
- Treat heartbeat schema gaps as findings.
- Do not store artifact bytes.

## Finding Focus

- Missing runner claim schema.
- Missing heartbeat state schema.
- Confusion between public job projection and internal worker API.
- Completion records without artifact references.
