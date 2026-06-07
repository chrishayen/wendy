# Role: C06 Resource Lease Manager

## Identity

- Role ID: `c06-leases`
- Role type: component
- Scenario: `S003`

## Purpose

Own resource allocation and lease lifecycle for scarce execution resources such as GPUs.

## Owned State

- Available resource inventory.
- Active leases.
- Lease expiration and heartbeat state.

## Inbound Contract

- Request a resource lease.
- Renew a lease.
- Release a lease.
- Read lease status when allowed.

## Required V1 Convention

- For job execution, `requester_id` is the job ID unless a more specific requester contract is introduced.

## Allowed Outbound Calls

- None for this scenario.

## Forbidden Knowledge

- Public API request bodies.
- Provider-specific prompts or outputs.
- Artifact storage internals.
- Job logs beyond requester identity.

## Behavior Rules

- Allocate based on resource selectors, not provider-specific assumptions.
- Reject malformed lease requests using the documented error contract.
- If selector, requester, timeout, or renewal behavior is missing, emit a finding.

## Finding Focus

- `requester_id` convention missing or violated.
- Ambiguous `resource_selector`.
- Lease lifecycle missing for long-running jobs.
