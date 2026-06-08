# PACP

Pluggable Agent Control Plane implementation.

The product direction is a generic host for user-approved service providers.
Contract simulation data is kept as test input, not as product behavior.

## What Exists

- `internal/contracts`: shared public API types, envelopes, and validation
  helpers.
- `internal/provider`: provider SDK helpers for manifest, health, invoke,
  simple schema validation, and provider response envelopes.
- `internal/runner`: composition runner that claims jobs, acquires leases,
  starts node-managed providers by node ID, invokes providers, uploads
  artifacts, and completes or fails jobs through public APIs.
- `internal/components/catalog`: service catalog with in-memory or file-backed
  provider registration storage, health, and HTTP handlers.
- `internal/components/gateway`: agent-facing tool discovery, invocation, job,
  log, artifact, content, and health gateway that composes public component
  APIs.
- `internal/components/jobs`: async job lifecycle service with in-memory or
  file-backed durable storage, health, and HTTP handlers.
- `internal/components/leases`: resource registry, FIFO lease queue, heartbeat,
  release, expiration, and inspection service with in-memory or file-backed
  durable storage, startup resource seeding, health, and HTTP handlers.
- `internal/components/artifacts`: upload-session, blob storage, durable
  metadata snapshots, policy context, guarded local registration, and retrieval
  service with health and a local filesystem root.
- `internal/components/policy`: API key verification, policy decision, secret
  reference, redaction, startup policy seeding, health, and durable sensitive
  state snapshots.
- `internal/components/node`: runtime node agent with local auth, resource
  advertisement, fake, process, and Docker service lifecycle adapters, health,
  lease resource export, and service status APIs.
- `internal/testkit`: contract-simulation fixture loader and fixture-backed
  HTTP fake server.
- `cmd/pacp-contract-smoke`: CLI smoke check for a contract simulation package.
- `cmd/pacp-fixture-server`: serves one fixture owner as an HTTP fake.
- `cmd/pacp-fake-provider`: runnable sample provider using the provider SDK.
- `cmd/pacp-http-provider`: generic provider bridge for HTTP backends that
  accept the PACP provider invocation shape.
- `cmd/pacp-command-provider`: generic provider bridge for local commands that
  read a provider invocation JSON object on stdin and write a provider response
  JSON object on stdout.
- `cmd/pacp-browser-search-provider`: constrained browser/search provider with
  file-backed search and guarded page extraction.
- `cmd/pacp-comfyui-provider`: purpose-specific ComfyUI image generation
  provider with workflow templates, LoRA validation, and dry-run mode.
- `cmd/pacp-speech-provider`: purpose-specific text-to-speech and
  speech-to-text provider with voice/format validation and command-backed
  engine adapters.
- `cmd/pacp-ai-toolkit-provider`: purpose-specific dataset registry and LoRA
  training provider with a provider-owned workspace and dry-run mode.
- `cmd/pacp-catalog`: runnable catalog server that loads provider manifests.
- `cmd/pacp-gateway`: runnable agent tool gateway.
- `cmd/pacp-jobs`: runnable async job service.
- `cmd/pacp-leases`: runnable resource lease service.
- `cmd/pacp-artifacts`: runnable artifact store.
- `cmd/pacp-policy`: runnable access policy and secrets service.
- `cmd/pacp-node`: runnable runtime node agent for one configured service node.
- `cmd/pacp-runner`: runnable composition runner.
- `cmd/pacp-primary`: primary-host process for C03, C04, C05, C06, C07, C08,
  and an optional runner using arbitrary manifests, resources, and policy
  seed files.
- `cmd/pacp-bundle`: renders one deployment bundle into catalog manifests,
  node config, lease resource seed, and optional policy seed files.
- `cmd/pacp-admin`: JSON-first operator CLI for component, gateway, node, and
  provider health, inspection, job cancellation through C04, and node lifecycle
  actions.
- `cmd/pacp-control`: JSON-first CLI for gateway health and agent-facing
  gateway operations.
- `cmd/pacp-dev`: one-command local development stack using the real service
  HTTP boundaries.
- `openapi/public-gateway.v1.yaml`: OpenAPI contract for the agent-facing
  gateway.
- `openapi/component-services.v1.yaml`: OpenAPI contract for distributed
  component service APIs.
- `testdata/contract-sim`: accepted role-play fixtures copied from the vault.
- `testdata/manifests`: sample provider manifests used by tests and examples.

## Local Checks

```sh
go test ./...
go run ./cmd/pacp-contract-smoke
go run ./cmd/pacp-contract-smoke -provider-url http://localhost:18088 -capability-id cap_dev_echo -input '{"message":"hello"}'
go run ./cmd/pacp-dev
go run ./cmd/pacp-dev -state-dir /tmp/pacp-dev-state
PACP_HTTP_ECHO_TOKEN='Bearer dev-token' go run ./cmd/pacp-http-provider -addr localhost:18088 -manifest testdata/http-provider/echo-manifest.json -routes testdata/http-provider/echo-routes.json -endpoint http://localhost:18088
go run ./cmd/pacp-command-provider -addr localhost:18088 -manifest provider-manifest.json -routes command-routes.json -endpoint http://localhost:18088
go run ./cmd/pacp-browser-search-provider -addr localhost:18089 -search-index testdata/browser-search/index.json -allowed-hosts localhost,127.0.0.1
go run ./cmd/pacp-comfyui-provider -addr localhost:18090 -dry-run -workflow testdata/comfyui/workflow-template.json -lora-catalog testdata/comfyui/loras.json
go run ./cmd/pacp-speech-provider -addr localhost:18091 -dry-run -voice-catalog testdata/speech/catalog.json
go run ./cmd/pacp-ai-toolkit-provider -addr localhost:18092 -dry-run -workspace testdata/ai-toolkit
go run ./cmd/pacp-admin health
go run ./cmd/pacp-admin -node-urls node_mac=http://mac:18087,node_linux_gpu=http://linux-box:18087 health -providers
go run ./cmd/pacp-admin catalog capabilities
go run ./cmd/pacp-admin catalog import /tmp/pacp-bundle/catalog
go run ./cmd/pacp-admin jobs list
go run ./cmd/pacp-admin diagnose job job_000001
go run ./cmd/pacp-admin -gateway-token token_agent jobs cancel job_000001 -idempotency-key cancel-1 -reason "stop requested"
go run ./cmd/pacp-admin leases resources
go run ./cmd/pacp-admin leases register-resource -resource-id res_gpu_0 -selector gpu -node-id node_linux_gpu -tags gpu,gpu:0
go run ./cmd/pacp-admin leases create-request -requester-id job_manual -selector gpu
go run ./cmd/pacp-admin leases cancel-request lease_req_000001 -reason "operator cleanup"
go run ./cmd/pacp-admin leases release lease_000001 -holder-id job_manual -idempotency-key release-1 -actor-subject-id sub_admin -reason "operator release"
go run ./cmd/pacp-admin artifacts list
go run ./cmd/pacp-admin artifacts create-upload -name output.txt -media-type text/plain -owner-subject-id sub_admin -producer-ref job_manual -idempotency-key upload-create-1
go run ./cmd/pacp-admin artifacts put-content upload_000001 -file /tmp/output.txt -media-type text/plain -idempotency-key upload-content-1
go run ./cmd/pacp-admin artifacts complete-upload upload_000001 -file /tmp/output.txt -idempotency-key upload-complete-1
go run ./cmd/pacp-admin artifacts register-local -path blobs/output.txt -name output.txt -media-type text/plain -owner-subject-id sub_admin
go run ./cmd/pacp-admin policy create-key -subject-id sub_admin -scopes admin,component
go run ./cmd/pacp-admin policy check -subject-id sub_agent -action tool.invoke -resource cap_image_generate
PACP_PROVIDER_TOKEN='secret-value' go run ./cmd/pacp-admin policy create-secret -name provider_token -value-env PACP_PROVIDER_TOKEN
go run ./cmd/pacp-bundle -bundle testdata/deploy/generic-gpu-bundle.json -out-dir /tmp/pacp-bundle
go run ./cmd/pacp-primary -manifest /tmp/pacp-bundle/catalog -resources /tmp/pacp-bundle/leases/resources.json -policy-seed /tmp/pacp-bundle/policy/policy-seed.json -state-dir /tmp/pacp-primary-state -artifact-root /tmp/pacp-primary-artifacts -disable-runner
go run ./cmd/pacp-control -gateway-url http://localhost:18086 health
go run ./cmd/pacp-control -gateway-url http://localhost:18086 -token token_agent tools
go run ./cmd/pacp-control -gateway-url http://localhost:18086 -token token_agent invoke cap_dev_echo -idempotency-key echo-1 -input '{"message":"hello"}'
go run ./cmd/pacp-control -gateway-url http://localhost:18086 -token token_agent invoke cap_dev_artifact -idempotency-key artifact-1 -input '{"prompt":"red mug"}'
```

Use `pacp-dev -state-dir` when local jobs, catalog entries, leases, artifact
metadata, policy credentials, and gateway invocation idempotency should survive
a restart. Artifact bytes are stored under `-artifact-root`.

The services can also be run separately for distributed testing:

```sh
go run ./cmd/pacp-fake-provider -addr localhost:18088
go run ./cmd/pacp-catalog -addr localhost:18081 -manifest testdata/manifests/s003-comfyui-gpu.json -state-file /tmp/pacp-catalog-state.json
go run ./cmd/pacp-jobs -addr localhost:18082 -state-file /tmp/pacp-jobs-state.json
go run ./cmd/pacp-leases -addr localhost:18083 -state-file /tmp/pacp-leases-state.json -resources testdata/leases/linux-gpu-resources.json
go run ./cmd/pacp-artifacts -addr localhost:18084 -root /tmp/pacp-artifacts -state-file /tmp/pacp-artifacts-state.json
go run ./cmd/pacp-policy -addr localhost:18085 -state-file /tmp/pacp-policy-state.json -seed testdata/policy/local-seed.json
go run ./cmd/pacp-gateway -addr localhost:18086 -catalog-url http://localhost:18081 -jobs-url http://localhost:18082 -artifacts-url http://localhost:18084 -policy-url http://localhost:18085 -idempotency-state-file /tmp/pacp-gateway-idempotency-state.json
go run ./cmd/pacp-node -addr localhost:18087 -config testdata/node/linux-gpu-fake.json
go run ./cmd/pacp-runner -once -worker-id runner_local -jobs-url http://localhost:18082 -leases-url http://localhost:18083 -artifacts-url http://localhost:18084 -policy-url http://localhost:18085 -credential token_worker -node-urls node_linux_gpu=http://localhost:18087 -node-start-timeout 30s
```

Deployment bundles are offline packaging inputs for distributed nodes. Render a
bundle once, then pass the generated files to the existing component binaries:

```sh
go run ./cmd/pacp-bundle -bundle testdata/deploy/generic-gpu-bundle.json -out-dir /tmp/pacp-bundle
go run ./cmd/pacp-catalog -manifest /tmp/pacp-bundle/catalog/svc_generic_gpu_image.manifest.json
go run ./cmd/pacp-node -config /tmp/pacp-bundle/node/node.json
go run ./cmd/pacp-leases -resources /tmp/pacp-bundle/leases/resources.json
go run ./cmd/pacp-policy -seed /tmp/pacp-bundle/policy/policy-seed.json
```

For a single primary host, `pacp-primary` hosts the control-plane components in
one process while preserving HTTP component boundaries:

```sh
go run ./cmd/pacp-primary -manifest /tmp/pacp-bundle/catalog -resources /tmp/pacp-bundle/leases/resources.json -policy-seed /tmp/pacp-bundle/policy/policy-seed.json -runner-credential token_runner -state-dir /tmp/pacp-primary-state -artifact-root /tmp/pacp-primary-artifacts -node-urls node_linux_gpu=http://linux-box:18087
```

Node resource declarations can be converted into lease resource seed files:

```sh
go run ./cmd/pacp-node -config testdata/node/linux-gpu-fake.json -export-lease-resources
```

For distributed deployments, set `PACP_COMPONENT_TOKEN` or `-component-token`
on catalog, jobs, leases, artifacts, and policy services. `pacp-gateway` and
`pacp-runner` use `PACP_COMPONENT_TOKEN` for downstream component calls unless
overridden by `PACP_GATEWAY_CREDENTIAL`, `PACP_RUNNER_CREDENTIAL`,
`-gateway-credential`, or `-credential`. Raw tokens and `Bearer ...` values are
both accepted. Leaving the token unset keeps local service endpoints open for
quick isolated testing. The example policy seed creates logical policy
credentials for the gateway, runner, and local agent; component endpoint
authentication is a separate transport guard.
When `pacp-runner` is given `-policy-url`, or when the primary embedded runner
uses the co-hosted policy service, the runner credential should identify a
subject with `worker` scope so `provider.invoke` is allowed intentionally.

Use `pacp-runner -node-urls` or `PACP_NODE_URLS` for distributed nodes. The
format is comma-separated `node_id=URL` entries, for example
`node_linux_gpu=http://linux-box:18087,node_mac_services=http://mac:18087`.

HTTP provider bridge route files can set literal `headers` for non-secret
values and `headers_from_env` for backend credentials that must not be stored in
JSON config.

Command provider bridge route files map each capability id to a command array.
The command receives `ProviderInvokeRequest` JSON on stdin and must write
`ProviderInvokeResponse` JSON on stdout. Route files can set literal
`environment` values and `environment_from_env` for secrets.

The fixture server can also serve individual contract-simulation fixture
owners when a test needs a fixed fake dependency.

The policy seed and state files store API tokens and secret values. Keep them
private and outside shared artifact directories. Reapplying the same policy
seed is idempotent, but startup fails if an existing token or secret name has
drifted from the seed.

The catalog and gateway can then be queried:

```sh
curl http://localhost:18081/v1/catalog/capabilities
curl http://localhost:18081/v1/catalog/capabilities/cap_image_generate_gpu/route
curl http://localhost:18081/v1/catalog/health
curl http://localhost:18082/v1/jobs/health
curl http://localhost:18083/v1/leases/health
curl http://localhost:18084/v1/artifacts/health
curl http://localhost:18085/v1/policy/health
curl http://localhost:18086/v1/gateway/health
go run ./cmd/pacp-admin -node-url http://localhost:18087 -node-token token_agent_smoke health
go run ./cmd/pacp-admin -node-urls node_linux_gpu=http://localhost:18087 -node-token token_agent_smoke health -providers
go run ./cmd/pacp-admin catalog route cap_image_generate_gpu
go run ./cmd/pacp-admin -node-url http://localhost:18087 -node-token token_agent_smoke node services
go run ./cmd/pacp-admin -node-url http://localhost:18087 -node-token token_runner_smoke node start svc_comfyui_gpu -idempotency-key start-comfy-1
go run ./cmd/pacp-admin -node-url http://localhost:18087 -node-token token_runner_smoke node stop svc_comfyui_gpu
go run ./cmd/pacp-control -gateway-url http://localhost:18086 -token token_agent tools
```

This is not the full production control plane yet. It is a usable service stack
with public HTTP boundaries, file-backed local durability, a provider SDK, a
generic HTTP provider bridge, a composition runner, runtime node adapters, and a
gateway and admin control CLI. Production databases, broader workflow
automation, and hardening remain.
