# PACP

Pluggable Agent Control Plane implementation.

The product direction is a generic host for user-approved service providers.
Contract simulation data is kept as test input, not as product behavior.

## What Exists

- `internal/contracts`: shared fixture and envelope validation helpers.
- `internal/components/catalog`: first isolated real component, C03 Service
  Catalog, with in-memory storage and HTTP handlers.
- `internal/components/jobs`: async job lifecycle service with in-memory
  storage and HTTP handlers.
- `internal/testkit`: S003 fixture loader and fixture-backed HTTP fake server.
- `cmd/pacp-contract-smoke`: CLI smoke check for a contract simulation package.
- `cmd/pacp-fixture-server`: serves one fixture owner as an HTTP fake.
- `cmd/pacp-catalog`: runnable catalog server that loads provider manifests.
- `cmd/pacp-jobs`: runnable async job service.
- `testdata/contract-sim`: accepted role-play fixtures copied from the vault.
- `testdata/manifests`: sample provider manifests used by tests and examples.

## Local Checks

```sh
go test ./...
go run ./cmd/pacp-contract-smoke
go run ./cmd/pacp-fixture-server -owner c04-agent-tool-gateway -addr localhost:18080
go run ./cmd/pacp-catalog -addr localhost:18081 -manifest testdata/manifests/s003-comfyui-gpu.json
go run ./cmd/pacp-jobs -addr localhost:18082
```

The catalog can then be queried:

```sh
curl http://localhost:18081/v1/catalog/capabilities
curl http://localhost:18081/v1/catalog/capabilities/cap_image_generate_gpu/route
```

This is not the full control plane yet. It is the first implementation-facing
contract test kit slice plus the first isolated component.
