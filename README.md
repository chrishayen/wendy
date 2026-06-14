# Wendy

Wendy is a pluggable agent control plane. It gives agents one gateway for
discovering tools, invoking provider capabilities, running asynchronous jobs,
leasing host resources, and collecting artifacts.

It can run as a one-command local stack or as separate roles across multiple
hosts:

```text
client -> gateway/control plane -> runner -> runtime node -> provider
```

The fastest way to use it is the `wendy` CLI. The older component CLIs still
exist for debugging and custom deployments, but they are not required for the
normal path.

## Requirements

- Go 1.25 or newer.
- Open ports for the services you run. The default local ports are `18080`
  through `18088`.
- Network reachability between the primary host, runtime nodes, and providers
  for multi-host setups.

## Quick Start

Start a complete local stack:

```sh
go run ./cmd/wendy up
```

The first run creates:

- `wendy.yaml`: local configuration with generated credentials.
- `.wendy/`: state files and artifacts.

Both are ignored by git.

In another terminal, call the gateway:

```sh
go run ./cmd/wendy status
go run ./cmd/wendy tools
go run ./cmd/wendy invoke cap_dev_echo --input '{"message":"hello"}'
```

Run an async tool and wait for completion:

```sh
go run ./cmd/wendy invoke cap_dev_artifact \
  --input '{"prompt":"red mug"}' \
  --wait
```

Download artifacts from a completed job:

```sh
go run ./cmd/wendy artifacts job_000001 --out-dir ./wendy-output
```

If you prefer a binary:

```sh
mkdir -p bin
go build -o bin/wendy ./cmd/wendy
./bin/wendy up
```

## Configuration

`wendy.yaml` is the main interface. A generated local config looks like this in
shape:

```yaml
schema_version: v1
primary:
  host: localhost
  # Optional. Bind services to a different interface than the host advertised
  # to clients and other hosts.
  # bind_host: 0.0.0.0
  ports:
    node_registry: 18080
    catalog: 18081
    jobs: 18082
    leases: 18083
    artifacts: 18084
    policy: 18085
    gateway: 18086
    provider: 18088
  state_dir: .wendy/state
  artifact_root: .wendy/artifacts
  embedded_runner: true

credentials:
  agent: ${WENDY_AGENT_TOKEN}
  component: ${WENDY_COMPONENT_TOKEN}
  runner: ${WENDY_RUNNER_TOKEN}
  node_admin: ${WENDY_NODE_ADMIN_TOKEN}

providers:
  - service_id: svc_dev_provider
    service_name: Development Provider
    node_id: node_local
    kind: dev
    addr: localhost:18088
    endpoint: http://localhost:18088

nodes:
  - node_id: node_local
    display_name: Local Node
    addr: localhost:18087
    public_url: http://localhost:18087
    resources:
      - resource_id: res_dev_gpu
        selector: gpu
        display_name: Local development GPU
        tags: [gpu, dev]
```

Generated configs contain real token values. Keep `wendy.yaml` private, or use
environment references like `${WENDY_AGENT_TOKEN}` before sharing the file.

Useful config commands:

```sh
go run ./cmd/wendy init
go run ./cmd/wendy init --profile distributed
go run ./cmd/wendy admin credentials
go run ./cmd/wendy admin credentials --show
```

Use `-c` to point at a different config:

```sh
go run ./cmd/wendy -c ./deploy/wendy.yaml tools
```

## Multi-Host Setup

Use this when the control plane runs on one host and providers run on other
machines.

On the primary host:

```sh
go run ./cmd/wendy init --profile distributed
```

Edit `wendy.yaml`:

- Set `primary.host` to an address other hosts can reach.
- Set `primary.bind_host` to `0.0.0.0` if the primary should listen on all
  interfaces.
- Set each provider `endpoint` to the provider host URL.
- Set each node `public_url` to the runtime node host URL.
- Set each node `addr` to the local listener address for that node process,
  for example `0.0.0.0:18087`.
- Use environment variables for credentials if the same config is copied to
  multiple hosts.

Start the primary role:

```sh
go run ./cmd/wendy primary up
```

On a runtime node host:

```sh
go run ./cmd/wendy -c wendy.yaml node up --node node_local
```

On a provider host:

```sh
go run ./cmd/wendy -c wendy.yaml provider up --service svc_generic_gpu_image
```

From a client machine that can reach the primary gateway:

```sh
go run ./cmd/wendy -c wendy.yaml tools
go run ./cmd/wendy -c wendy.yaml invoke cap_image_generate \
  --input '{"prompt":"red mug","width":512,"height":512}' \
  --wait
```

### ComfyUI Provider

The distributed profile creates a dry-run ComfyUI-style provider. For a real
ComfyUI backend, set the provider to something like:

```yaml
providers:
  - service_id: svc_generic_gpu_image
    service_name: GPU Image Provider
    node_id: node_gpu
    kind: comfyui
    addr: 0.0.0.0:18088
    endpoint: http://gpu-host:18088
    capability_id: cap_image_generate
    dry_run: false
    comfyui_url: http://127.0.0.1:8188
    workflow: testdata/comfyui/workflow-template.json
    lora_catalog: testdata/comfyui/loras.json

nodes:
  - node_id: node_gpu
    display_name: GPU Node
    addr: 0.0.0.0:18087
    public_url: http://gpu-host:18087
    resources:
      - resource_id: res_gpu_0
        selector: gpu
        display_name: GPU
        tags: [gpu]
```

`addr` is where the Wendy provider listens. `endpoint` is the URL the runner uses
to reach it.

## Command Reference

```text
wendy init [--profile local|distributed] [--force]
wendy up [--no-runner]
wendy primary up
wendy node up --node <node-id>
wendy provider up --service <service-id>
wendy status
wendy tools
wendy invoke <capability-id> [--input JSON] [--mode sync|async] [--wait]
wendy artifacts <job-id> [--out-dir dir]
wendy admin credentials [--show]
wendy admin health
```

Role summary:

- `wendy up`: local all-in-one stack with control plane, provider, and runner.
- `wendy primary up`: control plane and runner, without starting a provider.
- `wendy node up`: runtime node API for a configured node.
- `wendy provider up`: provider API for a configured service.

## Advanced Tools

The lower-level binaries are still available when you need direct control:

- `wendy-primary`: combined control-plane process with many explicit flags.
- `wendy-node`: runtime node agent.
- `wendy-runner`: standalone runner.
- `wendy-control`: gateway client.
- `wendy-admin`: operator client.
- `wendy-bundle`: renders deployment bundle files.
- `wendy-catalog`, `wendy-jobs`, `wendy-leases`, `wendy-artifacts`,
  `wendy-policy`, `wendy-gateway`: individual control-plane components.

Use these for contract work, compatibility checks, or custom deployments. New
users should start with `wendy`.

## Provider Development

Provider manifests describe service endpoints, capabilities, resource hints,
artifact hints, input schemas, and output schemas.

Validate a manifest:

```sh
go run ./cmd/wendy-validate manifest testdata/manifests/comfyui-gpu.json
```

Validate invocation payloads:

```sh
go run ./cmd/wendy-validate provider-invoke \
  -manifest testdata/manifests/comfyui-gpu.json \
  -capability cap_sample_image_generate_gpu \
  testdata/validate/provider-invoke-image.json

go run ./cmd/wendy-validate tool-invoke \
  -manifest testdata/manifests/comfyui-gpu.json \
  -capability cap_sample_image_generate_gpu \
  testdata/validate/tool-invoke-image.json
```

Provider options in this repo include native Go providers, HTTP bridges,
command bridges, ComfyUI, speech, and AI Toolkit providers.

## Security Notes

- `wendy.yaml` contains credentials when generated. Do not commit it.
- `.wendy/` contains state and artifacts. Do not commit it.
- `wendy admin credentials --show` prints secret values.
- For shared configs, prefer `${ENV_VAR}` references for credentials.
- Use private interfaces, firewall rules, or a reverse proxy when exposing
  component ports outside a trusted network.

## Tests And Contract Checks

Run the Go tests:

```sh
go test ./...
```

If your Go cache is read-only:

```sh
GOCACHE=/tmp/go-build-cache go test ./...
```

Useful smoke checks:

```sh
go run ./cmd/wendy-contract-smoke
go run ./cmd/wendy-contract-smoke -distributed
go run ./cmd/wendy-contract-smoke -process-distributed -timeout 30s
```

Important fixtures:

- `openapi/public-gateway.v1.yaml`: agent-facing gateway API.
- `openapi/component-services.v1.yaml`: component service APIs.
- `testdata/deploy`: sample deployment bundles.
- `testdata/manifests`: provider manifests.
- `testdata/node`: runtime node configs.
