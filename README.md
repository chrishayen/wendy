# PACP

PACP is a pluggable agent control plane. It exposes provider capabilities through
an agent-facing gateway, queues asynchronous work, leases resources, starts
services on remote runtime nodes, runs providers, stores artifacts, and keeps the
public HTTP boundaries between those pieces visible.

The project is useful today as a local or distributed service stack with
file-backed state, a provider SDK, runtime node adapters, a runner, and CLI
tools. It is not a finished production platform: production databases, broader
workflow automation, and deployment hardening are still future work.

## Architecture

The default distributed shape is:

```text
agent/client
  -> pacp-gateway
  -> pacp-jobs / pacp-leases / pacp-artifacts / pacp-policy / pacp-catalog
  -> pacp-runner
  -> pacp-node on one or more remote hosts
  -> provider process, HTTP bridge, command bridge, ComfyUI provider, etc.
```

The main binaries are:

- `pacp-dev`: one-command local stack for development.
- `pacp-primary`: single process for catalog, gateway, jobs, leases, artifacts,
  policy, node registry, and optionally a runner.
- `pacp-node`: runtime node agent for a host that owns services and resources.
- `pacp-runner`: claims jobs, leases resources, starts node-managed services,
  invokes providers, uploads artifacts, and completes jobs.
- `pacp-control`: agent-facing CLI for gateway operations.
- `pacp-admin`: operator CLI for health, metrics, node registry, jobs, leases,
  artifacts, policy, and node service actions.
- `pacp-bundle`: renders a deployment bundle into catalog, node, lease, and
  policy files.

## Requirements

- Go 1.25 or newer.
- Network access between the primary host, runtime nodes, and provider
  endpoints for distributed runs.
- Open firewall rules for whichever HTTP ports you bind. The examples use
  `18080` through `18088`.

Run the test suite:

```sh
go test ./...
```

If your environment has a read-only Go cache, use a writable cache:

```sh
GOCACHE=/tmp/go-build-cache go test ./...
```

## Quick Start: Local Stack

Start everything on one machine:

```sh
go run ./cmd/pacp-dev
```

The local stack starts these endpoints:

| Service | URL |
| --- | --- |
| Gateway | `http://localhost:18086` |
| Catalog | `http://localhost:18081` |
| Jobs | `http://localhost:18082` |
| Leases | `http://localhost:18083` |
| Artifacts | `http://localhost:18084` |
| Policy | `http://localhost:18085` |
| Provider | `http://localhost:18088` |

Try it from another terminal:

```sh
curl http://localhost:18086/v1/gateway/health

go run ./cmd/pacp-control \
  -gateway-url http://localhost:18086 \
  -token token_agent \
  tools

go run ./cmd/pacp-control \
  -gateway-url http://localhost:18086 \
  -token token_agent \
  invoke cap_dev_echo \
  -idempotency-key echo-1 \
  -input '{"message":"hello"}'
```

Run the local stack with durable state:

```sh
go run ./cmd/pacp-dev \
  -state-dir /tmp/pacp-dev-state \
  -artifact-root /tmp/pacp-dev-artifacts
```

## Quick Start: Distributed Smoke Test

This starts real child processes for the primary, node, fake provider, and
runner on temporary local ports. It verifies node self-registration, node trust,
gateway invocation, runner execution, and artifact retrieval.

```sh
go run ./cmd/pacp-contract-smoke -process-distributed -timeout 30s
```

A passing run ends with:

```text
process-distributed-smoke=pass
```

Use this before changing distributed wiring.

## Distributed Setup

Use this flow when one machine hosts the control plane and other machines host
runtime nodes/providers.

### 1. Build Or Run The Binaries

During development, `go run` is fine. For real hosts, build binaries and copy
them to the machines that need them:

```sh
mkdir -p bin
go build -o bin/pacp-primary ./cmd/pacp-primary
go build -o bin/pacp-node ./cmd/pacp-node
go build -o bin/pacp-runner ./cmd/pacp-runner
go build -o bin/pacp-control ./cmd/pacp-control
go build -o bin/pacp-admin ./cmd/pacp-admin
```

Provider hosts also need whichever provider binary they run, for example:

```sh
go build -o bin/pacp-fake-provider ./cmd/pacp-fake-provider
go build -o bin/pacp-comfyui-provider ./cmd/pacp-comfyui-provider
go build -o bin/pacp-http-provider ./cmd/pacp-http-provider
go build -o bin/pacp-command-provider ./cmd/pacp-command-provider
```

### 2. Render A Deployment Bundle

The sample bundle defines one GPU node, one GPU lease resource, a provider
manifest, and policy tokens:

```sh
go run ./cmd/pacp-bundle \
  -bundle testdata/deploy/generic-gpu-bundle.json \
  -out-dir /tmp/pacp-bundle
```

It writes:

```text
/tmp/pacp-bundle/catalog/svc_generic_gpu_image.manifest.json
/tmp/pacp-bundle/leases/resources.json
/tmp/pacp-bundle/node/node.json
/tmp/pacp-bundle/policy/policy-seed.json
```

Edit the rendered files before deploying if your real hostnames, provider
ports, tokens, node IDs, or resource names differ. In particular, provider
manifest endpoints must be reachable by the runner.

For the commands below, make these two edits after rendering the sample bundle:

- Change provider endpoints in the catalog manifest and node config from
  `http://linux-gpu-node:18088` to `http://gpu-host:18088`.
- Change the node auth token for the worker subject from `token_node_runner` to
  `token_runner`, or choose your own shared worker token and update the policy
  seed, node config, primary runner flags, and provider token list together.

### 3. Start The Primary Host

Replace `0.0.0.0` with a private interface if you do not want these services on
all interfaces.

```sh
go run ./cmd/pacp-primary \
  -catalog-addr 0.0.0.0:18081 \
  -jobs-addr 0.0.0.0:18082 \
  -leases-addr 0.0.0.0:18083 \
  -artifacts-addr 0.0.0.0:18084 \
  -policy-addr 0.0.0.0:18085 \
  -gateway-addr 0.0.0.0:18086 \
  -node-registry-addr 0.0.0.0:18080 \
  -manifest /tmp/pacp-bundle/catalog \
  -resources /tmp/pacp-bundle/leases/resources.json \
  -policy-seed /tmp/pacp-bundle/policy/policy-seed.json \
  -state-dir /tmp/pacp-primary-state \
  -artifact-root /tmp/pacp-primary-artifacts \
  -component-token token_component \
  -gateway-credential token_component \
  -runner-credential token_runner \
  -runner-policy-credential token_component \
  -runner-node-registry-credential token_component \
  -runner-subject-id sub_runner \
  -runner-actor-subject-id sub_runner \
  -route-aware-component-auth
```

This command keeps the runner embedded in `pacp-primary`. Use `-disable-runner`
when you want to run `pacp-runner` separately.

### 4. Start A Runtime Node

On the GPU host, start the node agent. Replace `primary-host` and `gpu-host`
with names or IPs that the machines can actually resolve.

```sh
go run ./cmd/pacp-node \
  -addr 0.0.0.0:18087 \
  -config /tmp/pacp-bundle/node/node.json \
  -node-registry-url http://primary-host:18080 \
  -node-registry-credential token_component \
  -node-public-url http://gpu-host:18087 \
  -node-registry-register \
  -node-registry-heartbeat 30s
```

The node registers as `untrusted` by default. Trust it from the primary host:

```sh
go run ./cmd/pacp-admin \
  -node-registry-url http://primary-host:18080 \
  -component-token token_component \
  node-registry trust node_linux_gpu \
  -trust-state trusted \
  -reason "approved gpu node"
```

You can also skip self-registration and pass node routes directly to the
primary or runner:

```sh
-node-urls node_linux_gpu=http://gpu-host:18087,node_mac_services=http://mac-host:18087
```

### 5. Start The Provider On The Node Host

The sample bundle uses a fake runtime adapter for node lifecycle, so the
provider process still has to be reachable separately. This dry-run ComfyUI
provider matches the sample bundle's service and capability IDs:

```sh
go run ./cmd/pacp-comfyui-provider \
  -addr 0.0.0.0:18088 \
  -endpoint http://gpu-host:18088 \
  -service-id svc_generic_gpu_image \
  -capability-id cap_image_generate \
  -dry-run \
  -workflow testdata/comfyui/workflow-template.json \
  -lora-catalog testdata/comfyui/loras.json \
  -runner-tokens token_runner
```

For a real ComfyUI backend, drop `-dry-run` and set `-comfyui-url`. For a real
HTTP backend, use `pacp-http-provider` with a manifest and route file. For local
commands, use `pacp-command-provider`.

### 6. Invoke Through The Gateway

From a client machine that can reach the primary gateway:

```sh
go run ./cmd/pacp-control \
  -gateway-url http://primary-host:18086 \
  -token token_agent \
  tools

go run ./cmd/pacp-control \
  -gateway-url http://primary-host:18086 \
  -token token_agent \
  invoke cap_image_generate \
  -idempotency-key image-1 \
  -input '{"prompt":"red mug","width":512,"height":512}' \
  -wait
```

Fetch artifacts from a completed job:

```sh
go run ./cmd/pacp-control \
  -gateway-url http://primary-host:18086 \
  -token token_agent \
  artifacts job_000001 \
  -out-dir /tmp/pacp-job-output
```

## Running The Components Separately

`pacp-primary` is convenient, but each component can run as its own process:

```sh
go run ./cmd/pacp-catalog \
  -addr localhost:18081 \
  -manifest testdata/manifests/comfyui-gpu.json \
  -state-file /tmp/pacp-catalog-state.json

go run ./cmd/pacp-jobs \
  -addr localhost:18082 \
  -state-file /tmp/pacp-jobs-state.json

go run ./cmd/pacp-leases \
  -addr localhost:18083 \
  -resources testdata/leases/linux-gpu-resources.json \
  -state-file /tmp/pacp-leases-state.json

go run ./cmd/pacp-artifacts \
  -addr localhost:18084 \
  -root /tmp/pacp-artifacts \
  -state-file /tmp/pacp-artifacts-state.json

go run ./cmd/pacp-policy \
  -addr localhost:18085 \
  -seed testdata/policy/local-seed.json \
  -state-file /tmp/pacp-policy-state.json

go run ./cmd/pacp-gateway \
  -addr localhost:18086 \
  -catalog-url http://localhost:18081 \
  -jobs-url http://localhost:18082 \
  -leases-url http://localhost:18083 \
  -artifacts-url http://localhost:18084 \
  -policy-url http://localhost:18085 \
  -idempotency-state-file /tmp/pacp-gateway-idempotency-state.json
```

Run a standalone runner against those services:

```sh
go run ./cmd/pacp-runner \
  -worker-id runner_local \
  -worker-subject-id sub_runner_local \
  -actor-subject-id sub_runner_local \
  -catalog-url http://localhost:18081 \
  -jobs-url http://localhost:18082 \
  -leases-url http://localhost:18083 \
  -artifacts-url http://localhost:18084 \
  -policy-url http://localhost:18085 \
  -credential token_worker \
  -node-urls node_linux_gpu=http://localhost:18087 \
  -addr localhost:18089
```

Use `-once` when you want the runner to process at most one job and exit.

## Provider Development

Provider manifests describe the service endpoint, capabilities, resource hints,
artifact hints, input schemas, and output schemas. Validate a manifest:

```sh
go run ./cmd/pacp-validate manifest testdata/manifests/comfyui-gpu.json
```

Validate provider and gateway invocation payloads:

```sh
go run ./cmd/pacp-validate provider-invoke \
  -manifest testdata/manifests/comfyui-gpu.json \
  -capability cap_sample_image_generate_gpu \
  testdata/validate/provider-invoke-image.json

go run ./cmd/pacp-validate tool-invoke \
  -manifest testdata/manifests/comfyui-gpu.json \
  -capability cap_sample_image_generate_gpu \
  testdata/validate/tool-invoke-image.json
```

Provider options:

- Use `internal/provider` to build a native Go provider with health, metrics,
  auth, schema validation, sync handlers, async-style handlers, and artifact
  helpers.
- Use `pacp-http-provider` to bridge to an HTTP backend that accepts PACP
  provider invocation JSON.
- Use `pacp-command-provider` to run local commands that read
  `ProviderInvokeRequest` JSON on stdin and write `ProviderInvokeResponse` JSON
  on stdout.
- Use `pacp-comfyui-provider`, `pacp-speech-provider`, or
  `pacp-ai-toolkit-provider` for the included purpose-built providers.

Provider endpoints can require a bearer token. Generic SDK-backed providers use
`-provider-credential`, `PACP_PROVIDER_CREDENTIAL`, or `PACP_PROVIDER_TOKEN`.
ComfyUI accepts runner/component token lists with `-runner-tokens` and
`-component-tokens`.

Set `-endpoint` or `PACP_PROVIDER_ENDPOINT` to the URL the runner should use for
the provider. This matters when a provider listens on `0.0.0.0` or a node-local
interface but should advertise a different routable URL.

## Runtime Nodes

A node config declares:

- `node_id`: stable ID used by manifests and lease resources.
- `resources`: resources available on the host, such as `gpu`.
- `auth`: local node API credentials and allowed node actions.
- `services`: provider services the node can start, touch, stop, and monitor.

Service runtime adapters:

- `fake`: records lifecycle state but does not launch a process.
- `process`: starts a configured local command.
- `docker`: controls an existing Docker container.

Convert a node config into lease resources:

```sh
go run ./cmd/pacp-node \
  -config testdata/node/linux-gpu-fake.json \
  -export-lease-resources
```

## Auth Model

There are two layers:

- Transport tokens protect component-to-component HTTP calls.
- Policy credentials identify subjects and scopes such as `agent`, `worker`,
  `component`, and `admin`.

For distributed deployments:

- Set `-component-token` on control-plane services.
- Set `-gateway-credential` so the gateway can call component APIs.
- Set `-runner-credential` for worker routes such as job claims, leases, node
  starts, and artifact uploads.
- Set `-runner-policy-credential` when the policy service itself is protected by
  the component token.
- Set `-runner-node-registry-credential` when node registry lookups are
  protected by the component token.
- Set `-node-registry-credential` on nodes that self-register or heartbeat.

Raw token values and `Bearer ...` values are both accepted.

Policy seed and state files contain tokens and secret values. Keep them private
and outside shared artifact directories.

## Operations

Health checks:

```sh
curl http://localhost:18086/v1/gateway/health
curl -H 'Authorization: Bearer token_component' http://localhost:18080/v1/node-registry/health
go run ./cmd/pacp-admin -component-token token_component health
```

Inspect providers, nodes, metrics, and alerts:

```sh
go run ./cmd/pacp-admin -component-token token_component catalog capabilities
go run ./cmd/pacp-admin -component-token token_component node-registry list
go run ./cmd/pacp-admin -node-url http://gpu-host:18087 -node-token token_node_admin node services
go run ./cmd/pacp-admin -component-token token_component metrics -providers
go run ./cmd/pacp-admin -component-token token_component alerts -providers -node-registry
```

Operate jobs and resources:

```sh
go run ./cmd/pacp-admin -component-token token_component jobs list
go run ./cmd/pacp-admin -component-token token_component diagnose job job_000001
go run ./cmd/pacp-admin -component-token token_component diagnose resource res_gpu_0
go run ./cmd/pacp-admin -component-token token_component leases resources
go run ./cmd/pacp-admin -component-token token_component artifacts list
```

Rotate a policy key:

```sh
go run ./cmd/pacp-admin -component-token token_component policy rotate-key key_000001
```

Gateway and runner requests propagate `X-Request-ID` to downstream services.
Use `-request-id` or `PACP_REQUEST_ID` with the CLIs when you want a stable ID
for log correlation.

## Test Data And Contracts

- `openapi/public-gateway.v1.yaml`: agent-facing gateway API.
- `openapi/component-services.v1.yaml`: component service APIs.
- `testdata/deploy`: sample deployment bundles.
- `testdata/manifests`: sample provider manifests.
- `testdata/node`: sample node configs.
- `testdata/contract-sim`: accepted contract simulation fixtures used as test
  input, not product behavior.

Useful checks:

```sh
go run ./cmd/pacp-contract-smoke
go run ./cmd/pacp-contract-smoke -openapi openapi/public-gateway.v1.yaml,openapi/component-services.v1.yaml
go run ./cmd/pacp-contract-smoke -fake-public-apis
go run ./cmd/pacp-contract-smoke -distributed
go run ./cmd/pacp-contract-smoke -process-distributed -timeout 30s
```
