# PACP

Pluggable Agent Control Plane implementation.

The product direction is a generic host for user-approved service providers.
Contract simulation data is kept as test input, not as product behavior.

## What Exists

- `internal/contracts`: shared public API types, envelopes, and validation
  helpers.
- `cmd/pacp-validate`: local contract validation CLI for provider manifests and
  provider/tool invocation payloads.
- `internal/provider`: provider SDK helpers for manifest, health, metrics, invoke,
  simple schema validation, provider response envelopes, and sync,
  async-style, and artifact-producing handler helpers.
- `internal/runner`: composition runner that claims jobs, acquires leases,
  starts node-managed providers by node ID, invokes providers, uploads
  artifacts, and completes or fails jobs through public APIs.
- `internal/components/catalog`: service catalog with in-memory or file-backed
  provider registration storage, export snapshots, health, and HTTP handlers.
- `internal/components/gateway`: agent-facing tool discovery, invocation, job,
  log, artifact, content, and health gateway that composes public component
  APIs.
- `internal/components/jobs`: async job lifecycle service with in-memory or
  file-backed durable storage, health, and HTTP handlers.
- `internal/components/leases`: resource registry, FIFO lease queue, heartbeat,
  release, expiration, and inspection service with in-memory or file-backed
  durable storage, startup resource seeding, health, and HTTP handlers.
- `internal/components/artifacts`: upload-session, blob storage, durable
  metadata snapshots, policy context, guarded local registration, retention
  sweep, and retrieval service with health and a local filesystem root.
- `internal/components/policy`: API key verification, API key lifecycle and
  auth-failure audit, policy decision, secret reference, secret-access audit,
  redaction, startup policy seeding, health, and durable sensitive state
  snapshots.
- `internal/components/node`: runtime node agent with local auth, resource
  advertisement, fake, process, and Docker service lifecycle adapters, idle
  shutdown accounting, lifecycle event log, health, lease resource export,
  optional node registry registration and heartbeat reporting, and service
  status APIs.
- `internal/testkit`: contract-simulation fixture loader, fixture-backed HTTP
  fake server, reusable public component/provider fakes, and fixture replay
  helpers for live handler contract tests. The reusable provider fake includes
  sync, async-acceptance, artifact-producing, validation-failure, and execution
  failure branches; reusable component fakes can run in success, denied, or
  unavailable modes and can expose caller-supplied public list records. The
  reusable policy fake covers auth allow/failure, policy allow/deny, secret
  resolution, and redaction. The reusable node fake covers resources,
  running/stopped/starting/failed services, lifecycle events, lifecycle
  idempotency, and unavailable-node behavior. The reusable jobs fake covers queued, claimed,
  running, succeeded, failed, canceled, and expired jobs plus create, claim,
  heartbeat, completion, cancellation, logs, and unavailable behavior. The
  reusable leases fake covers available/unavailable resources, pending,
  granted, expired, and canceled lease requests, denied resource requests,
  heartbeat, release promotion, and unavailable behavior. The reusable
  artifacts fake covers available, expired, denied, and missing artifacts,
  upload lifecycle, raw content reads, local registration, retention sweep, and
  unavailable behavior. The reusable catalog fake covers valid, invalid, denied, missing,
  and unavailable capability outcomes plus manifest registration.
- `cmd/pacp-contract-smoke`: CLI smoke check for contract simulation packages,
  OpenAPI contracts, live component contracts, distributed component wiring,
  and live provider compliance.
- `cmd/pacp-fixture-server`: serves one fixture owner as an HTTP fake.
- `cmd/pacp-fake-provider`: runnable sample provider using the provider SDK.
- `cmd/pacp-http-provider`: generic provider bridge for HTTP backends that
  accept the PACP provider invocation shape.
- `cmd/pacp-command-provider`: generic provider bridge for local commands that
  read a provider invocation JSON object on stdin and write a provider response
  JSON object on stdout.
- `cmd/pacp-browser-search-provider`: constrained browser/search provider with
  file-backed scoped search and action-aware guarded page extraction.
- `cmd/pacp-comfyui-provider`: purpose-specific ComfyUI image generation
  provider with workflow templates, LoRA validation, and dry-run mode.
- `cmd/pacp-speech-provider`: purpose-specific text-to-speech and
  speech-to-text provider with voice/format/generation-option validation and
  command-backed engine adapters.
- `cmd/pacp-ai-toolkit-provider`: purpose-specific dataset registry, dataset
  upload/update, validated Z-Image-Turbo LoRA training, and LoRA output index
  provider with a provider-owned workspace and dry-run mode.
- `cmd/pacp-catalog`: runnable catalog server that loads provider manifests.
- `cmd/pacp-gateway`: runnable agent tool gateway backed by either the catalog
  or static provider manifests.
- `cmd/pacp-jobs`: runnable async job service.
- `cmd/pacp-leases`: runnable resource lease service.
- `cmd/pacp-artifacts`: runnable artifact store.
- `cmd/pacp-policy`: runnable access policy and secrets service.
- `cmd/pacp-node`: runnable runtime node agent for one configured service node.
- `cmd/pacp-runner`: runnable composition runner with optional health and
  metrics monitor endpoints.
- `cmd/pacp-primary`: primary-host process for the catalog, gateway, jobs,
  leases, artifacts, policy, node registry, and an optional runner using
  arbitrary manifests, resources, and policy seed files.
- `cmd/pacp-bundle`: renders one deployment bundle into catalog manifests,
  node config, lease resource seed, and optional policy seed files.
- `cmd/pacp-admin`: JSON-first operator CLI for component, gateway, node,
  runner, and provider health, inspection, job cancellation through the gateway,
  node registry trust operations, and node lifecycle actions.
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
go run ./cmd/pacp-validate manifest testdata/manifests/comfyui-gpu.json
go run ./cmd/pacp-validate provider-invoke -manifest testdata/manifests/comfyui-gpu.json -capability cap_sample_image_generate_gpu testdata/validate/provider-invoke-image.json
go run ./cmd/pacp-validate tool-invoke -manifest testdata/manifests/comfyui-gpu.json -capability cap_sample_image_generate_gpu testdata/validate/tool-invoke-image.json
go run ./cmd/pacp-contract-smoke
go run ./cmd/pacp-contract-smoke -openapi openapi/public-gateway.v1.yaml,openapi/component-services.v1.yaml
go run ./cmd/pacp-contract-smoke -fake-public-apis
go run ./cmd/pacp-contract-smoke -distributed
go run ./cmd/pacp-contract-smoke -process-distributed -timeout 30s
go run ./cmd/pacp-contract-smoke -component-url http://localhost:18082 -component-kind jobs -component-credential token_component
go run ./cmd/pacp-contract-smoke -provider-url http://localhost:18088 -provider-credential token_worker -capability-id cap_dev_echo -input '{"message":"hello"}'
go run ./cmd/pacp-dev
go run ./cmd/pacp-dev -state-dir /tmp/pacp-dev-state
PACP_HTTP_ECHO_TOKEN='Bearer dev-token' go run ./cmd/pacp-http-provider -addr localhost:18088 -manifest testdata/http-provider/echo-manifest.json -routes testdata/http-provider/echo-routes.json -endpoint http://localhost:18088
go run ./cmd/pacp-command-provider -addr localhost:18088 -manifest provider-manifest.json -routes command-routes.json -endpoint http://localhost:18088
go run ./cmd/pacp-browser-search-provider -addr localhost:18089 -search-index testdata/browser-search/index.json -allowed-hosts localhost,127.0.0.1
go run ./cmd/pacp-comfyui-provider -addr localhost:18090 -dry-run -workflow testdata/comfyui/workflow-template.json -lora-catalog testdata/comfyui/loras.json -runner-tokens token_worker
go run ./cmd/pacp-speech-provider -addr localhost:18091 -dry-run -voice-catalog testdata/speech/catalog.json
go run ./cmd/pacp-ai-toolkit-provider -addr localhost:18092 -dry-run -workspace testdata/ai-toolkit
go run ./cmd/pacp-admin health
go run ./cmd/pacp-admin -node-urls node_mac=http://mac:18087,node_linux_gpu=http://linux-box:18087 health -providers
go run ./cmd/pacp-admin metrics -providers
go run ./cmd/pacp-admin node-registry list
go run ./cmd/pacp-admin node-registry register node_linux_gpu -url http://linux-box:18087 -trust-state trusted -status reachable -tags gpu,linux
go run ./cmd/pacp-admin node-registry trust node_linux_gpu -trust-state disabled -reason "maintenance"
go run ./cmd/pacp-admin node-registry heartbeat node_linux_gpu -status reachable
go run ./cmd/pacp-admin catalog capabilities
go run ./cmd/pacp-admin catalog import /tmp/pacp-bundle/catalog
go run ./cmd/pacp-admin jobs list
go run ./cmd/pacp-admin diagnose job job_000001
go run ./cmd/pacp-admin diagnose resource res_gpu_0
go run ./cmd/pacp-admin -gateway-token token_agent jobs cancel job_000001 -idempotency-key cancel-1 -reason "stop requested"
go run ./cmd/pacp-admin -node-url http://localhost:18087 -node-token token_worker node events
go run ./cmd/pacp-admin leases resources
go run ./cmd/pacp-admin leases register-resource -resource-id res_gpu_0 -selector gpu -node-id node_linux_gpu -tags gpu,gpu:0
go run ./cmd/pacp-admin leases create-request -requester-id job_manual -selector gpu
go run ./cmd/pacp-admin leases requests -requester-id job_manual
go run ./cmd/pacp-admin leases cancel-request lease_req_000001 -reason "operator cleanup"
go run ./cmd/pacp-admin leases release lease_000001 -holder-id job_manual -idempotency-key release-1 -actor-subject-id sub_admin -reason "operator release"
go run ./cmd/pacp-admin artifacts list
go run ./cmd/pacp-admin artifacts retention-sweep
go run ./cmd/pacp-admin artifacts create-upload -name output.txt -media-type text/plain -owner-subject-id sub_admin -producer-ref job_manual -idempotency-key upload-create-1
go run ./cmd/pacp-admin artifacts put-content upload_000001 -file /tmp/output.txt -media-type text/plain -idempotency-key upload-content-1
go run ./cmd/pacp-admin artifacts complete-upload upload_000001 -file /tmp/output.txt -idempotency-key upload-complete-1
go run ./cmd/pacp-admin artifacts register-local -path blobs/output.txt -name output.txt -media-type text/plain -owner-subject-id sub_admin
go run ./cmd/pacp-admin policy create-key -subject-id sub_admin -scopes admin,component
go run ./cmd/pacp-admin policy rotate-key key_000001
go run ./cmd/pacp-admin policy check -subject-id sub_agent -action tool.invoke -resource cap_image_generate
PACP_PROVIDER_TOKEN='secret-value' go run ./cmd/pacp-admin policy create-secret -name provider_token -value-env PACP_PROVIDER_TOKEN
go run ./cmd/pacp-bundle -bundle testdata/deploy/generic-gpu-bundle.json -out-dir /tmp/pacp-bundle
go run ./cmd/pacp-primary -manifest /tmp/pacp-bundle/catalog -resources /tmp/pacp-bundle/leases/resources.json -policy-seed /tmp/pacp-bundle/policy/policy-seed.json -state-dir /tmp/pacp-primary-state -artifact-root /tmp/pacp-primary-artifacts -disable-runner
go run ./cmd/pacp-node -addr localhost:18087 -config testdata/node/linux-gpu-fake.json -node-registry-url http://localhost:18080 -node-registry-credential token_component -node-public-url http://linux-box:18087 -node-registry-register -node-registry-heartbeat 30s
go run ./cmd/pacp-control -gateway-url http://localhost:18086 health
go run ./cmd/pacp-control -gateway-url http://localhost:18086 -token token_agent tools
go run ./cmd/pacp-control -gateway-url http://localhost:18086 -token token_agent invoke cap_dev_echo -idempotency-key echo-1 -input '{"message":"hello"}'
go run ./cmd/pacp-control -gateway-url http://localhost:18086 -token token_agent invoke cap_dev_artifact -idempotency-key artifact-1 -input '{"prompt":"red mug"}' -wait
go run ./cmd/pacp-control -gateway-url http://localhost:18086 -token token_agent wait job_000001
go run ./cmd/pacp-control -gateway-url http://localhost:18086 -token token_agent queue gpu
go run ./cmd/pacp-control -gateway-url http://localhost:18086 -token token_agent artifacts job_000001 -out-dir /tmp/pacp-job-output
go run ./cmd/pacp-control -gateway-url http://localhost:18086 -token token_agent artifact-content art_000001 -out /tmp/pacp-output.png
```

The distributed smoke command starts an in-memory primary-plus-node topology and
checks route auth separation, live component health/metrics, stable read-only
component list surfaces, gateway invocation, runner execution, artifact
retrieval, node service lifecycle, lease release audit, and provider invocation.
The process-distributed smoke command starts real `pacp-primary`, `pacp-node`,
`pacp-fake-provider`, and `pacp-runner` child processes on temporary local ports
and verifies node self-registration, registry trust promotion, and an async
gateway-to-runner-to-node-to-provider artifact flow.
The single-component smoke mode checks health and metrics for every component
kind, and also checks read-only list surfaces for artifacts, catalog, jobs,
leases, and node components.

Use `pacp-dev -state-dir` when local jobs, catalog entries, leases, artifact
metadata, policy credentials, and gateway invocation idempotency should survive
a restart. Artifact bytes are stored under `-artifact-root`.

The services can also be run separately for distributed testing:

```sh
go run ./cmd/pacp-fake-provider -addr localhost:18088
go run ./cmd/pacp-catalog -addr localhost:18081 -manifest testdata/manifests/comfyui-gpu.json -state-file /tmp/pacp-catalog-state.json
go run ./cmd/pacp-jobs -addr localhost:18082 -state-file /tmp/pacp-jobs-state.json
go run ./cmd/pacp-leases -addr localhost:18083 -state-file /tmp/pacp-leases-state.json -resources testdata/leases/linux-gpu-resources.json
go run ./cmd/pacp-artifacts -addr localhost:18084 -root /tmp/pacp-artifacts -state-file /tmp/pacp-artifacts-state.json
go run ./cmd/pacp-policy -addr localhost:18085 -state-file /tmp/pacp-policy-state.json -seed testdata/policy/local-seed.json
go run ./cmd/pacp-gateway -addr localhost:18086 -catalog-url http://localhost:18081 -jobs-url http://localhost:18082 -leases-url http://localhost:18083 -artifacts-url http://localhost:18084 -policy-url http://localhost:18085 -idempotency-state-file /tmp/pacp-gateway-idempotency-state.json
go run ./cmd/pacp-node -addr localhost:18087 -config testdata/node/linux-gpu-fake.json
go run ./cmd/pacp-runner -once -worker-id runner_local -actor-subject-id sub_runner_local -catalog-url http://localhost:18081 -jobs-url http://localhost:18082 -leases-url http://localhost:18083 -artifacts-url http://localhost:18084 -policy-url http://localhost:18085 -credential token_worker -node-urls node_linux_gpu=http://localhost:18087 -node-start-timeout 30s -lease-poll 1s -addr localhost:18089
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
go run ./cmd/pacp-primary -manifest /tmp/pacp-bundle/catalog -resources /tmp/pacp-bundle/leases/resources.json -policy-seed /tmp/pacp-bundle/policy/policy-seed.json -runner-credential token_runner -runner-actor-subject-id sub_runner_local -state-dir /tmp/pacp-primary-state -artifact-root /tmp/pacp-primary-artifacts -node-urls node_linux_gpu=http://linux-box:18087
```

Add `-route-aware-component-auth` to `pacp-primary` when the embedded catalog,
jobs, leases, and artifact services should enforce the same policy-backed route
auth as their standalone binaries. In that mode `-component-token` still
protects the embedded policy service and is used by co-hosted components when
calling the policy `/v1/auth/verify` endpoint.

Node resource declarations can be converted into lease resource seed files:

```sh
go run ./cmd/pacp-node -config testdata/node/linux-gpu-fake.json -export-lease-resources
```

`pacp-node` can also report itself to a node registry through the public
registry API. Use `-node-registry-register` with `-node-public-url` to create or
refresh a registry record at startup, and `-node-registry-heartbeat 30s` to keep
the record reachable while the node process is running. Self-registration
defaults to the registry's trust rules, so an operator can still promote or
disable trust explicitly with `pacp-admin node-registry trust`.

Direct job creation/cancellation, node service start/touch/stop, and lease release
are side-effecting operations and require an `Idempotency-Key`. The gateway sets
the job creation and cancellation headers for public invocations, `pacp-admin jobs
cancel`, `pacp-admin node start`, and `pacp-admin node stop` expose an
`-idempotency-key` flag, and runner start operations set the node lifecycle
header for you. Node service touch records active use for idle shutdown and does
not require an idempotency key. `pacp-admin leases release` and runner release
operations set the lease release header.

For distributed deployments, set `PACP_COMPONENT_TOKEN` or `-component-token`
on catalog, jobs, leases, artifacts, and policy services. `pacp-gateway` uses
`PACP_COMPONENT_TOKEN` for downstream component calls unless overridden by
`PACP_GATEWAY_CREDENTIAL` or `-gateway-credential`. `pacp-runner` uses
`PACP_RUNNER_CREDENTIAL` or `-credential` for worker routes such as job claims,
leases, node starts, and artifact uploads. When the policy service is
transport-protected by a component token, set `PACP_RUNNER_POLICY_CREDENTIAL`
or `-policy-credential` so runner policy calls use the component credential while
worker routes still use the worker credential. When the node registry is
transport-protected by a component token, set
`PACP_RUNNER_NODE_REGISTRY_CREDENTIAL` or `-node-registry-credential` so node
trust lookups use the component credential while node service calls still use
the worker credential. `pacp-node` uses `PACP_NODE_REGISTRY_CREDENTIAL` or
`-node-registry-credential` when it self-registers or heartbeats to the node
registry. In `pacp-primary`, `-component-token` also becomes the default
embedded gateway credential, embedded runner credential, embedded runner policy
credential, and embedded runner node-registry credential unless the more
specific gateway or runner credential flags are set. Raw tokens and
`Bearer ...` values are both accepted. Leaving the token unset keeps local
service endpoints open for quick isolated testing. The example policy seed
creates logical policy credentials for the gateway, runner, and local agent;
component endpoint authentication is a separate transport guard.
Provider endpoints may also enforce runner/component bearer tokens. The
ComfyUI provider accepts `-runner-tokens`, `-component-tokens`, and
`-agent-tokens`; the first two are allowed to invoke and fetch provider-local
content, while configured agent tokens are authenticated but forbidden. The
flags default from `PACP_PROVIDER_RUNNER_TOKENS`, `PACP_RUNNER_CREDENTIAL`,
`PACP_PROVIDER_COMPONENT_TOKENS`, and `PACP_COMPONENT_TOKEN`.
Generic SDK-backed provider binaries accept `-provider-credential`, defaulting
from `PACP_PROVIDER_CREDENTIAL` or `PACP_PROVIDER_TOKEN`, to require a bearer
credential for invoke and provider-local content routes. Manifest, health, and
metrics routes remain readable for discovery and monitoring.
Provider binaries accept `-endpoint` or `PACP_PROVIDER_ENDPOINT` for the
provider URL advertised in manifests. Set it to the URL reachable by the
runner, especially when the listen address is bound to `:port` or a node-local
interface.
When `pacp-runner` is given `-policy-url`, or when the primary embedded runner
uses the co-hosted policy service, the runner credential should identify a
subject with `worker` scope so `provider.invoke` is allowed intentionally.
Set `-actor-subject-id` on `pacp-runner` or `-runner-actor-subject-id` on
`pacp-primary` when lease release audit records should use a stable policy
subject. If omitted, the runner falls back to the configured runner subject id,
then omits the audit header and lets the lease service use its local default.
Set `-addr` to expose `/v1/runner/health` and `/v1/runner/metrics`; set
`-monitor-token` or `PACP_RUNNER_MONITOR_TOKEN` when those endpoints should
require a bearer token.
Set `-catalog-url` or `PACP_CATALOG_URL` when queued jobs contain lean execution
plans that identify the capability and input but rely on the catalog for the current
provider route and resource hints. `pacp-primary` and `pacp-dev` pass their
co-hosted catalog URL to the embedded runner automatically.

Use `pacp-runner -node-urls` or `PACP_NODE_URLS` for distributed nodes. The
format is comma-separated `node_id=URL` entries, for example
`node_linux_gpu=http://linux-box:18087,node_mac_services=http://mac:18087`.
`pacp-primary` seeds those entries into its node registry as trusted registered
nodes and passes the registry URL to its embedded runner. Standalone runners can
use `-node-registry-url` or `PACP_NODE_REGISTRY_URL`; when set, explicit
`node_id` routes are resolved through `/v1/node-registry/nodes/{node_id}` and
disabled, untrusted, unreachable, or stale nodes are not used.

HTTP provider bridge route files can set literal `headers` for non-secret
values, `headers_from_env` for node-local backend credentials, and
`headers_from_secret` for policy secret refs that should be resolved through the
policy service at startup. Use `-policy-url`, `-policy-credential`, and
`-secret-subject-id` when a bridge route uses secret refs. The bridge forwards
the provider invocation request id to backend HTTP services as `X-Request-ID`
when the runner supplies one.

ComfyUI image generation returns provider-local `content_refs` to the runner.
The runner dereferences `/v1/provider/artifacts/{content_ref}/content`, verifies
the size/checksum/digest, uploads the bytes to the artifact store, and completes the job with
durable artifact ids. Provider-local content refs are runner-facing only and
must not be returned to agents.

`pacp-jobs` can use policy-backed route-aware auth instead of the coarse component
transport token by setting `-policy-url`. In that mode callers present their
own policy bearer credentials to the jobs service, and the jobs process verifies them through
the policy `/v1/auth/verify` endpoint: gateway/component credentials can create, cancel, and
read component projections; worker credentials can claim, heartbeat, log,
complete, fail, and read worker job state. Use `-policy-credential` when the
policy service itself is protected by a component transport token.

`pacp-artifacts` supports the same policy-backed route-aware auth mode. Worker
credentials can create upload sessions, upload content, complete uploads, and
register local artifacts; gateway/component credentials can list, read metadata,
read policy context, and retrieve artifact content. Use
`-policy-credential` when the policy service itself is protected by a component
transport token.
Set `-artifact-ttl` or `PACP_ARTIFACT_TTL` when completed artifacts should
expire by retention policy. Expired artifacts return `artifact_expired` for
metadata and content reads, are omitted from artifact lists, and report
`policy_state: expired` from the policy-context endpoint.

`pacp-leases` also supports policy-backed route-aware auth. Worker credentials can
request leases, heartbeat active leases, and release leases; component
credentials can register resources and inspect resource or lease state.

`pacp-catalog` supports policy-backed route-aware auth for component credentials
that register manifests, list catalog records, and read provider routes.
`pacp-dev` uses these route-aware component boundaries by default with its
seeded local component and worker credentials.

Command provider bridge route files map each capability id to a command array.
The command receives `ProviderInvokeRequest` JSON on stdin and must write
`ProviderInvokeResponse` JSON on stdout. Route files can set literal
`environment` values, `environment_from_env` for node-local secrets, and
`environment_from_secret` for policy secret refs resolved through the policy
service at startup. Use `-policy-url`, `-policy-credential`, and
`-secret-subject-id` when a bridge route uses secret refs. The bridge also adds
non-empty invocation context values as `PACP_REQUEST_ID`,
`PACP_SUBJECT_ID`, `PACP_JOB_ID`, `PACP_RESOURCE_LEASE_ID`, and
`PACP_ARTIFACT_BASE_URL`.

Provider-local async engines should stay hidden behind the provider adapter.
When a provider needs to acknowledge accepted backend work as its own output,
use `provider.AsyncHandler` to convert an `AcceptedHandle` into the normal
`ProviderInvokeResponse.output` object and declare `handle_id` / `status` in
the capability output schema. This remains a blocking provider invoke from the
runner's perspective and does not create PACP jobs or provider status/cancel
routes.

Provider authors can use `provider.NewManifestBuilder` to declare a service,
provider endpoint, capabilities, and handlers together. The builder defaults
the provider health path to `/v1/provider/health`, validates the manifest with
provider-contract rules, verifies that every capability has a handler, and can return either
the manifest plus handler map or a ready `provider.Server`.

Use `provider.ArtifactHandler` when a capability returns ordinary output plus
provider-local artifacts or content refs. The handler still emits a normal
provider response; durable artifact upload remains the runner and artifact store
responsibility.

The fixture server can also serve individual contract-simulation fixture
owners when a test needs a fixed fake dependency. It matches method, path,
declared query values, declared headers, and declared request bodies. If the
same exact request appears more than once in a fixture package, repeated calls
advance through those fixtures in file order so replay cases can be tested.
Fixture replay helpers can also send accepted fixture requests to live handlers
and compare status plus the expected response envelope as a JSON subset.

The policy seed and state files store API tokens and secret values. Keep them
private and outside shared artifact directories. Reapplying the same policy
seed is idempotent, but startup fails if an existing token or secret name has
drifted from the seed.
Use `pacp-admin policy rotate-key` when a credential should keep the same key id
and subject/scopes but receive a replacement bearer token. The old token stops
verifying immediately after rotation.

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
curl http://localhost:18089/v1/runner/health
curl http://localhost:18082/v1/jobs/metrics
curl http://localhost:18083/v1/leases/metrics
curl http://localhost:18086/v1/gateway/metrics
curl http://localhost:18088/v1/provider/metrics
curl http://localhost:18089/v1/runner/metrics
go run ./cmd/pacp-admin -node-url http://localhost:18087 -node-token token_agent_smoke health
go run ./cmd/pacp-admin -node-urls node_linux_gpu=http://localhost:18087 -node-token token_agent_smoke health -providers
go run ./cmd/pacp-admin -runner-url http://localhost:18089 health
go run ./cmd/pacp-admin -node-urls node_linux_gpu=http://localhost:18087 -node-token token_agent_smoke -runner-url http://localhost:18089 metrics -providers
go run ./cmd/pacp-admin -node-urls node_linux_gpu=http://localhost:18087 -node-token token_agent_smoke -runner-url http://localhost:18089 alerts -providers -node-registry -queue-depth-threshold 1 -runner-heartbeat-stale-after 5m
go run ./cmd/pacp-admin catalog route cap_image_generate_gpu
go run ./cmd/pacp-admin -node-url http://localhost:18087 -node-token token_agent_smoke node services
go run ./cmd/pacp-admin -node-url http://localhost:18087 -node-token token_runner_smoke node start svc_comfyui_gpu -idempotency-key start-comfy-1
go run ./cmd/pacp-admin -node-url http://localhost:18087 -node-token token_runner_smoke node touch svc_comfyui_gpu
go run ./cmd/pacp-admin -node-url http://localhost:18087 -node-token token_runner_smoke node stop svc_comfyui_gpu -idempotency-key stop-comfy-1
go run ./cmd/pacp-control -gateway-url http://localhost:18086 -token token_agent tools
go run ./cmd/pacp-control -gateway-url http://localhost:18086 -token token_agent queue gpu
```

Component metrics include component-specific state samples plus HTTP request
count, error count, and average latency by method and normalized route group.
Job metrics include active and expired claim counts so missed worker heartbeats
can be surfaced without direct store access.
Artifact metrics include registration counts, content retrieval counts, upload
states, and expiration counts.
Provider metrics additionally expose invocation count, error count, and average
duration by service id, capability id, status, and error code. Admin provider
metrics and alerts discover provider metric endpoints from catalog routes when
run with `-providers`.
Runner metrics expose active job count, run loop results, successful heartbeat
timestamps, and dependency reachability for configured primary APIs and nodes.
Runner lease-expiration and artifact-materialization errors are surfaced
through `pacp-admin alerts` so an operator can tell when jobs failed after
losing a resource lease or after provider output could not be stored.
Gateway health includes per-downstream dependency status for catalog, policy,
jobs, leases, and artifacts. Gateway metrics expose configured and reachable
samples for those downstreams, and `pacp-admin alerts` reports configured
gateway dependencies that are missing or not healthy.
Policy metrics expose policy denies and secret resolution outcomes.
`pacp-admin alerts` reports denied secret resolutions and escalates missing
secret refs as failed secret resolution alerts.
`pacp-admin alerts -node-registry` reads the public node registry API and
reports disabled, untrusted, unreachable, and stale nodes.
Runner operational logs are JSON records with request id, job id when present,
and configured credentials redacted.
Gateway and runner requests propagate `X-Request-ID` to downstream component,
node, artifact, and provider calls so logs and response metadata can be
correlated across the distributed flow. `pacp-admin` also propagates an
operator request id to every public API call. `pacp-control` does the same for
agent-facing gateway calls. Set `-request-id` or `PACP_REQUEST_ID` to reuse a
known id, or let the CLI generate one.

This is not the full production control plane yet. It is a usable service stack
with public HTTP boundaries, file-backed local durability, a provider SDK, a
generic HTTP provider bridge, a composition runner, runtime node adapters, and a
gateway and admin control CLI. Production databases, broader workflow
automation, and hardening remain.
