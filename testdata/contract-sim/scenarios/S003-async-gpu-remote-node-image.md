# S003: Async GPU Remote Node Image

## Metadata

- Scenario ID: `S003`
- Name: Async GPU remote node image generation
- Status: draft
- System: Pluggable Agent Control Plane

## Purpose

Validate that an agent can discover a GPU-backed image generation capability, invoke it through the public API, and retrieve the resulting artifact while the work is executed by isolated distributed components.

This scenario should prove the generic host model, not a one-off image service. ComfyUI is only the provider example.

## Source Requirements

- `00-component-inventory.md`
- `01-component-map.md`
- `02-layer-inventory.md`
- `03-use-cases.md`
- `04-public-interface-layer.md`
- `05-composition-orchestration-layer.md`
- `06-deployment-topology-layer.md`
- `07-security-trust-layer.md`
- `08-data-ownership-layer.md`
- `09-operations-layer.md`
- `10-test-compatibility-layer.md`
- `11-build-farming-layer.md`
- `component-03-capability-catalog.md`
- `component-04-public-control-api-gateway.md`
- `component-05-job-state-store.md`
- `component-06-resource-lease-manager.md`
- `component-07-artifact-store.md`
- `component-08-policy-authorizer.md`
- `component-09-runtime-adapter.md`
- `component-10-provider-adapter.md`
- `component-14-composition-runner.md`
- `openapi/public-interface.v1.yaml`

## Actors

- `agent-user`: external agent using only the public API.
- `c04-gateway`: public control API gateway.
- `c03-catalog`: capability catalog and route metadata owner.
- `c08-policy`: policy authorizer.
- `c05-jobs`: job and run state store.
- `c06-leases`: resource lease manager.
- `composition-runner`: generic runner for async jobs.
- `c09-linux-gpu-node`: runtime adapter for a Linux GPU host.
- `c10-comfyui-provider`: provider adapter for an image generation backend.
- `c07-artifact-store`: artifact upload, metadata, and retrieval owner.

## Initial State

- The catalog contains a published capability `cap_image_generate_gpu`.
- The capability is visible to the requesting agent after policy filtering.
- The capability route selects an async job path handled by the composition runner.
- A Linux GPU node is healthy and can satisfy the lease selector.
- The provider adapter can execute one blocking image generation request.
- The artifact store can create one direct upload session.

## Steps

1. `agent-user` requests `GET /v1/tools`.
   - `c04-gateway` authenticates, asks `c08-policy` for discovery visibility, asks `c03-catalog` for matching tools, and returns canonical public tool records.

2. `agent-user` requests `POST /v1/tools/cap_image_generate_gpu/invoke`.
   - The request uses the documented public invocation envelope.
   - `c04-gateway` authorizes invocation, resolves the capability route, creates an async job, and returns the canonical async invocation response.

3. `composition-runner` claims the job from `c05-jobs`.
   - The runner receives enough job input and route metadata to execute without reading public API internals.

4. `composition-runner` requests a GPU lease from `c06-leases`.
   - For v1, `requester_id` is the job ID.
   - The request uses resource selector fields from the route or job execution plan.

5. `composition-runner` starts or checks the runtime through `c09-linux-gpu-node`.
   - The runtime adapter exposes generic health/start behavior without leaking provider-specific implementation to the gateway.

6. `composition-runner` invokes `c10-comfyui-provider`.
   - Provider invocation is blocking from the runner perspective.
   - The runner does not expect a provider-owned public job ID.

7. `composition-runner` creates a direct upload session with `c07-artifact-store` and uploads the generated artifact.
   - Remote nodes do not write directly into local controller storage.

8. `composition-runner` marks the job complete in `c05-jobs`.
   - Job status points to artifact metadata owned by `c07-artifact-store`.

9. `agent-user` polls public job status, reads logs or progress, lists artifacts, and retrieves artifact content.
   - Public responses remain agent-friendly and do not expose internal component-only fields.

## Success Criteria

- The agent completes the flow through public APIs only.
- Each component can perform with its role package and addressed messages.
- No component needs full-system context.
- Async job response shapes match the OpenAPI contract.
- Discovery policy behavior is explicit.
- Remote artifact ingress uses the artifact store upload session contract.
- Provider adapter remains blocking from the runner perspective.
- All findings are classified with target files.

## Known Focus Areas From The Dry Run

- Discovery authorization needs to distinguish list visibility from per-capability invocation.
- Role packages must include public auth requirements so the user actor does not omit authorization.
- Catalog context must include canonical tool envelope, pagination, and route schema.
- Gateway context must preserve canonical link/action response shapes.
- Invocation body shape needs a clear decision: strict `input` envelope or accepted flat tool input.
- Lease requests need a documented `requester_id` convention.
- Heartbeat schemas need clear semantics for state and progress messages.
- Runtime and provider health/start response schemas need enough detail for isolated actors.

## Replay Targets

- Public API contract test for discovery, invocation, status, logs, artifacts, and content.
- Gateway-to-catalog contract test.
- Gateway-to-policy contract test.
- Gateway-to-job-store contract test.
- Runner-to-lease-manager contract test.
- Runner-to-runtime-adapter contract test.
- Runner-to-provider-adapter contract test.
- Runner-to-artifact-store contract test.
- Distributed smoke test across controller host, Mac service host, and Linux GPU host.
