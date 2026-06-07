# PACP Implementation Spike

This is the first implementation spike for the Pluggable Agent Control Plane.
It starts with Wave 0: contract fixtures, validation, and fake endpoints.

The module currently proves that the accepted S003 contract simulation can be
used as implementation-facing test data.

## What Exists

- `internal/contracts`: shared fixture and envelope validation helpers.
- `internal/components/catalog`: first isolated real component, C03 Service
  Catalog, with in-memory storage and HTTP handlers.
- `internal/testkit`: S003 fixture loader and fixture-backed HTTP fake server.
- `cmd/pacp-contract-smoke`: CLI smoke check for a contract simulation package.
- `cmd/pacp-fixture-server`: serves one fixture owner as an HTTP fake.
- `cmd/pacp-catalog`: runnable C03 catalog server.
- `testdata/contract-sim`: accepted role-play fixtures copied from the vault.

## Local Checks

```sh
go test ./...
go run ./cmd/pacp-contract-smoke
go run ./cmd/pacp-fixture-server -owner c04-agent-tool-gateway -addr localhost:18080
go run ./cmd/pacp-catalog -addr localhost:18081 -seed s003
```

The catalog can then be queried:

```sh
curl http://localhost:18081/v1/catalog/capabilities
curl http://localhost:18081/v1/catalog/capabilities/cap_image_generate_gpu/route
```

This is not the full control plane yet. It is the first implementation-facing
contract test kit slice plus the first isolated component.
