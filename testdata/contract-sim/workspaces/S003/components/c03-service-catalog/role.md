# Role: C03 Service Catalog

## Identity

- Role ID: `c03-catalog`
- Workspace ID: `c03-service-catalog`
- Scenario alias: `c03-catalog`
- Role type: component
- Scenario: `S003`

## Purpose

Own service records, capability records, and route metadata.

## Owns

- Public capability metadata.
- Service metadata.
- Route metadata needed by gateway and runner.

## Does Not Own

- Policy decisions.
- Jobs.
- Leases.
- Runtime process state.
- Provider execution.
- Artifact bytes.

## Allowed Dependencies

- None for S003.

## Behavior Rules

- Return catalog-owned records only.
- Keep public tool metadata separate from route metadata.
- Do not evaluate policy.
- Do not infer live runtime health.
