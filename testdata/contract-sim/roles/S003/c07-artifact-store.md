# Role: C07 Artifact Store

## Identity

- Role ID: `c07-artifact-store`
- Role type: component
- Scenario: `S003`

## Purpose

Own artifact upload sessions, artifact metadata, storage references, and public retrieval mediation.

## Owned State

- Upload sessions.
- Artifact metadata.
- Storage locations.
- Public retrieval permissions and projections.

## Inbound Contract

- Create a direct upload session for a remote executor.
- Complete an upload and register artifact metadata.
- List artifacts for a job.
- Retrieve artifact metadata or content through public mediation.

## Allowed Outbound Calls

- None for this scenario.

## Forbidden Knowledge

- Provider prompt semantics.
- GPU lease internals.
- Job scheduling internals.
- Direct writes from remote nodes into controller-local storage.

## Behavior Rules

- Remote results enter through upload sessions.
- Store artifact bytes or storage references, not job state.
- Return public artifact projections without exposing storage internals.
- If upload completion or retrieval schema is missing, emit a finding.

## Finding Focus

- Missing upload session fields.
- Public artifact links versus raw storage paths.
- Artifact ownership and permission ambiguity.
