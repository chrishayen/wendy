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
  invokes providers, uploads artifacts, and completes or fails jobs through
  public APIs.
- `internal/components/catalog`: service catalog with in-memory or file-backed
  provider registration storage and HTTP handlers.
- `internal/components/gateway`: agent-facing tool discovery, invocation, job,
  log, artifact, and content gateway that composes public component APIs.
- `internal/components/jobs`: async job lifecycle service with in-memory or
  file-backed durable storage and HTTP handlers.
- `internal/components/leases`: resource registry, FIFO lease queue, heartbeat,
  release, expiration, and inspection service with in-memory or file-backed
  durable storage and HTTP handlers.
- `internal/components/artifacts`: upload-session, blob storage, durable
  metadata snapshots, policy context, guarded local registration, and retrieval
  service with a local filesystem root.
- `internal/components/policy`: API key verification, policy decision, secret
  reference, redaction, and durable sensitive state snapshots.
- `internal/components/node`: runtime node agent with local auth, resource
  advertisement, fake, process, and Docker service lifecycle adapters, health,
  and service status APIs.
- `internal/testkit`: contract-simulation fixture loader and fixture-backed
  HTTP fake server.
- `cmd/pacp-contract-smoke`: CLI smoke check for a contract simulation package.
- `cmd/pacp-fixture-server`: serves one fixture owner as an HTTP fake.
- `cmd/pacp-fake-provider`: runnable sample provider using the provider SDK.
- `cmd/pacp-http-provider`: generic provider bridge for HTTP backends that
  accept the PACP provider invocation shape.
- `cmd/pacp-catalog`: runnable catalog server that loads provider manifests.
- `cmd/pacp-gateway`: runnable agent tool gateway.
- `cmd/pacp-jobs`: runnable async job service.
- `cmd/pacp-leases`: runnable resource lease service.
- `cmd/pacp-artifacts`: runnable artifact store.
- `cmd/pacp-policy`: runnable access policy and secrets service.
- `cmd/pacp-node`: runnable runtime node agent for one configured service node.
- `cmd/pacp-runner`: runnable composition runner.
- `cmd/pacp-control`: JSON-first CLI for agent-facing gateway operations.
- `cmd/pacp-dev`: one-command local development stack using the real service
  HTTP boundaries.
- `openapi/public-gateway.v1.yaml`: OpenAPI contract for the agent-facing
  gateway.
- `testdata/contract-sim`: accepted role-play fixtures copied from the vault.
- `testdata/manifests`: sample provider manifests used by tests and examples.

## Local Checks

```sh
go test ./...
go run ./cmd/pacp-contract-smoke
go run ./cmd/pacp-dev
go run ./cmd/pacp-dev -state-dir /tmp/pacp-dev-state
go run ./cmd/pacp-http-provider -addr localhost:18088 -manifest testdata/http-provider/echo-manifest.json -routes testdata/http-provider/echo-routes.json -endpoint http://localhost:18088
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
go run ./cmd/pacp-leases -addr localhost:18083 -state-file /tmp/pacp-leases-state.json
go run ./cmd/pacp-artifacts -addr localhost:18084 -root /tmp/pacp-artifacts -state-file /tmp/pacp-artifacts-state.json
go run ./cmd/pacp-policy -addr localhost:18085 -state-file /tmp/pacp-policy-state.json
go run ./cmd/pacp-gateway -addr localhost:18086 -catalog-url http://localhost:18081 -jobs-url http://localhost:18082 -artifacts-url http://localhost:18084 -policy-url http://localhost:18085 -idempotency-state-file /tmp/pacp-gateway-idempotency-state.json
go run ./cmd/pacp-node -addr localhost:18087 -config testdata/node/linux-gpu-fake.json
go run ./cmd/pacp-runner -once -worker-id runner_local -jobs-url http://localhost:18082 -leases-url http://localhost:18083 -artifacts-url http://localhost:18084
```

The fixture server can also serve individual contract-simulation fixture
owners when a test needs a fixed fake dependency.

The policy state file stores API tokens and secret values. Keep it private and
outside shared artifact directories.

The catalog and gateway can then be queried:

```sh
curl http://localhost:18081/v1/catalog/capabilities
curl http://localhost:18081/v1/catalog/capabilities/cap_image_generate_gpu/route
curl -X POST http://localhost:18085/v1/auth/api-keys -H 'Content-Type: application/json' -d '{"subject_id":"sub_agent_local","scopes":["agent"],"token":"token_agent"}'
go run ./cmd/pacp-control -gateway-url http://localhost:18086 -token token_agent tools
```

This is not the full production control plane yet. It is a usable service stack
with public HTTP boundaries, file-backed local durability, a provider SDK, a
generic HTTP provider bridge, a composition runner, runtime node adapters, and a
gateway control CLI. Production databases, richer provider-specific wrappers,
and hardening remain.
