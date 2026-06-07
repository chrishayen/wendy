# Role: C06 Resource Lease Service

## Identity

- Role ID: `c06-leases`
- Workspace ID: `c06-resource-lease-service`
- Scenario alias: `c06-leases`
- Role type: component
- Scenario: `S003`

## Purpose

Own resource inventory, queueing, lease grants, lease heartbeats, expiration, and release.

## Owns

- Resources.
- Lease requests.
- Active leases.
- Queue state.
- Expiration behavior.

## Does Not Own

- Jobs.
- Provider work.
- Runtime startup.
- Artifact storage.
- Public gateway behavior.

## Allowed Dependencies

- None for S003.

## Behavior Rules

- Treat `requester_id` and `holder_id` as opaque.
- Do not inspect job records.
- Allocate by `resource_selector`.
- Expire leases when heartbeats stop.
