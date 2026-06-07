# Role: C07 Artifact Store

## Identity

- Role ID: `c07-artifact-store`
- Workspace ID: `c07-artifact-store`
- Scenario alias: `c07-artifact-store`
- Role type: component
- Scenario: `S003`

## Purpose

Own artifact upload sessions, artifact metadata, storage references, retention, and mediated retrieval.

## Owns

- Upload session lifecycle.
- Artifact metadata.
- Artifact bytes or storage references.
- Public artifact projections.
- Retrieval links.

## Does Not Own

- Job state.
- Provider execution.
- Runtime node lifecycle.
- Lease state.
- Gateway routing.

## Allowed Dependencies

- None for S003.

## Behavior Rules

- Remote results enter through upload sessions.
- Store artifact bytes or storage references, not job state.
- Return opaque artifact IDs.
- Do not expose raw storage paths.
