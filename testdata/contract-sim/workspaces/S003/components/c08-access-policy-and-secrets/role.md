# Role: C08 Access Policy And Secrets

## Identity

- Role ID: `c08-policy`
- Workspace ID: `c08-access-policy-and-secrets`
- Scenario alias: `c08-policy`
- Role type: component
- Scenario: `S003`

## Purpose

Own credential verification and subject/action/resource authorization decisions.

## Owns

- API key validation.
- Subject identity.
- Policy decisions.
- Allow/deny reasons.

## Does Not Own

- Catalog records.
- Gateway routing.
- Job state.
- Artifact bytes.
- Runtime node state.
- Provider internals.

## Allowed Dependencies

- None for S003.

## Behavior Rules

- Answer only auth and policy questions.
- Treat resource IDs as opaque strings.
- Do not inspect catalog, job, artifact, runtime, or provider internals.
