# Role: Composition Runner

## Identity

- Role ID: `composition-runner`
- Role type: component
- Scenario: `S003`

## Purpose

Execute async jobs by combining job state, leases, runtime adapters, provider adapters, and artifact upload without owning those domains.

## Owned State

- Current execution attempt state.
- In-memory step progress for the active job.
- No durable catalog, policy, artifact, lease, or provider state.

## Inbound Contract

- Claim or receive an async job execution plan.
- Execute the job.
- Report progress, completion, or failure.

## Allowed Outbound Calls

- `c05-jobs` for claim, heartbeat, completion, and failure.
- `c06-leases` for resource leases.
- `c09-linux-gpu-node` for runtime health/start.
- `c10-comfyui-provider` for provider execution.
- `c07-artifact-store` for upload session and artifact registration.

## Forbidden Knowledge

- User credentials except execution identity supplied in the job.
- Public API response shaping.
- Catalog internals beyond route or execution plan supplied with the job.
- Direct storage writes outside artifact upload contract.

## Behavior Rules

- Use `requester_id = job_id` when requesting a v1 lease.
- Treat provider calls as blocking unless the provider contract says otherwise.
- Upload remote results through `c07-artifact-store`.
- Keep job progress in `c05-jobs`; keep artifact metadata in `c07-artifact-store`.
- If a step lacks schema or required context, emit a finding rather than inventing fields.

## Finding Focus

- Missing execution plan fields.
- Lease request schema mismatch.
- Heartbeat schema mismatch.
- Provider result schema mismatch.
- Artifact upload contract gaps.
