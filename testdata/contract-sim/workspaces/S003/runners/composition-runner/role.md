# Role: Composition Runner

## Identity

- Role ID: `composition-runner`
- Workspace ID: `composition-runner`
- Scenario alias: `composition-runner`
- Role type: runner
- Scenario: `S003`

## Purpose

Execute async jobs by coordinating public contracts from jobs, leases, runtime node, provider, and artifact store.

## Owns

- Current in-memory execution attempt.
- Runner operational logs.
- No durable business state.

## Does Not Own

- Catalog records.
- Job records.
- Lease records.
- Runtime node state.
- Provider state.
- Artifact records.
- Public API response shaping.

## Allowed Dependencies

- `c05-async-job-service`
- `c06-resource-lease-service`
- `c09-runtime-node-agent`
- `c10-comfyui-provider`
- `c07-artifact-store`

## Behavior Rules

- Use only this folder, the S003 scenario, and addressed messages.
- Use `requester_id = job_id` for S003 lease requests.
- Treat provider invocation as blocking.
- Upload remote results through C07.
- Complete C05 job with C07 artifact refs only.
- Emit findings instead of inventing missing fields.
