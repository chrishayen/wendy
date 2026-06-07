# Contract Excerpts: C09 Runtime Node Agent

## S003 Initial State

At the start of S003, node `node_linux_gpu` is reachable and healthy. Service
`svc_comfyui_gpu` may be `stopped` before the runner starts the job. The runner
must call `GET /v1/node/services/svc_comfyui_gpu` and call `POST /start` when
the service is not `running`.

## S003 Bounded Auth Facts

For fixture replay, C09 has local endpoint-auth facts only:

- `token_s003_runner` resolves to `sub_runner_s003` and is allowed to call
  `node.read`, `node.service.start`, and `node.service.stop` for
  `node_linux_gpu` / `svc_comfyui_gpu`.
- `token_s003_agent` resolves to `sub_agent_s003` but is not allowed to call
  node lifecycle actions.
- Missing, malformed, or unknown bearer tokens are unauthorized.

These facts make C09's local endpoint enforcement replayable in S003 without
making C09 the owner of global C08 policy rules.

## Node Health

Endpoint: `GET /v1/node/health`.

Required caller context:

```http
Authorization: Bearer token_s003_runner
```

The credential must resolve to a worker or component subject authorized for
`node.read`. C09 enforces node endpoint auth/action requirements using supplied
credentials or documented C08 checks, but C09 does not own global policy rule
definitions.

Response envelope:

```json
{
  "ok": true,
  "data": {
    "status": "healthy",
    "version": "v1",
    "checked_at": "2026-06-05T20:00:04Z",
    "details": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_node_health", "schema_version": "v1"}
}
```

## Service Status

Endpoint: `GET /v1/node/services/svc_comfyui_gpu`.

Required caller context:

```http
Authorization: Bearer token_s003_runner
```

The credential must resolve to a worker or component subject authorized for
`node.read` on `svc_comfyui_gpu`.

`provider_endpoint` is the provider origin/base URL for the node-managed
service. It must not include the provider API path prefix.

Response status: `200`.

Response envelope:

```json
{
  "ok": true,
  "data": {
    "service_id": "svc_comfyui_gpu",
    "status": "stopped",
    "runtime_adapter": "docker",
    "provider_endpoint": "http://node_linux_gpu:8188",
    "manifest": null,
    "links": {
      "start": {"method": "POST", "href": "/v1/node/services/svc_comfyui_gpu/start", "description": "Start service."},
      "stop": {"method": "POST", "href": "/v1/node/services/svc_comfyui_gpu/stop", "description": "Stop service."}
    }
  },
  "links": {},
  "meta": {"request_id": "req_s003_node_service_stopped", "schema_version": "v1"}
}
```

## Start Service

Endpoint: `POST /v1/node/services/svc_comfyui_gpu/start`.

Required caller context:

```http
Authorization: Bearer token_s003_runner
```

The credential must resolve to a worker or component subject authorized for
`node.service.start` on `svc_comfyui_gpu`.

Optional idempotency header:

```http
Idempotency-Key: idem_s003_node_start
```

Start is idempotent by service state even when the header is absent. When the
header is present, the same key and same service start request returns the
current service state for that start operation. Reusing the same key for a
different service or different start request returns `idempotency_conflict`.

Start lifecycle:

- If stopped, `POST /start` returns HTTP `202` with `status: starting`.
- If already running, `POST /start` returns HTTP `200` with current running
  service data.
- Replaying `Idempotency-Key: idem_s003_node_start` after the service is
  running returns HTTP `200` with current running service data and
  `meta.request_id: req_s003_node_start_replay`.
- Calling `POST /start` with no `Idempotency-Key` while the service is already
  running returns HTTP `200` with current running service data and
  `meta.request_id: req_s003_node_start_running_no_key`.
- The caller may poll `GET /v1/node/services/{service_id}` until `status: running` or `status: failed`.
- When `status: starting`, callers should poll no faster than every 2 seconds.
  S003 considers the service ready when a subsequent status response returns
  `status: running`.

S003 start response example:

```json
{
  "ok": true,
  "data": {
    "service_id": "svc_comfyui_gpu",
    "status": "starting",
    "runtime_adapter": "docker",
    "provider_endpoint": "http://node_linux_gpu:8188",
    "manifest": null,
    "links": {
      "status": {"method": "GET", "href": "/v1/node/services/svc_comfyui_gpu", "description": "Poll service status."}
    }
  },
  "links": {},
  "meta": {"request_id": "req_s003_node_start", "schema_version": "v1"}
}
```

S003 start replay response after service is running:

```json
{
  "ok": true,
  "data": {
    "service_id": "svc_comfyui_gpu",
    "status": "running",
    "runtime_adapter": "docker",
    "provider_endpoint": "http://node_linux_gpu:8188",
    "manifest": null,
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_node_start_replay", "schema_version": "v1"}
}
```

S003 ready response after polling:

```json
{
  "ok": true,
  "data": {
    "service_id": "svc_comfyui_gpu",
    "status": "running",
    "runtime_adapter": "docker",
    "provider_endpoint": "http://node_linux_gpu:8188",
    "manifest": null,
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_node_service_ready", "schema_version": "v1"}
}
```

## Stop Service

Endpoint: `POST /v1/node/services/svc_comfyui_gpu/stop`.

Required caller context:

```http
Authorization: Bearer token_s003_runner
```

The credential must resolve to a worker or component subject authorized for
the lifecycle operation.

Response status: `202`.

Response data:

```json
{
  "service_id": "svc_comfyui_gpu",
  "status": "stopped",
  "runtime_adapter": "docker",
  "provider_endpoint": "http://node_linux_gpu:8188",
  "manifest": null,
  "links": {
    "start": {"method": "POST", "href": "/v1/node/services/svc_comfyui_gpu/start", "description": "Start service."}
  }
}
```

Response envelope:

```json
{
  "ok": true,
  "data": {
    "service_id": "svc_comfyui_gpu",
    "status": "stopped",
    "runtime_adapter": "docker",
    "provider_endpoint": "http://node_linux_gpu:8188",
    "manifest": null,
    "links": {
      "start": {"method": "POST", "href": "/v1/node/services/svc_comfyui_gpu/start", "description": "Start service."}
    }
  },
  "links": {},
  "meta": {"request_id": "req_s003_node_stop", "schema_version": "v1"}
}
```

Stop is idempotent when the service is already stopped.

## Runtime Errors

- Unknown service: HTTP `404`, `code: not_found`.
- Runtime adapter unavailable: HTTP `503`, `code: provider_unavailable`.
- Start failure: HTTP `503`, `code: provider_unavailable`.
- Invalid lifecycle request: HTTP `400`, `code: validation_failed`.
- Idempotency key reused for different service/start content: HTTP `409`,
  `code: idempotency_conflict`.
- Missing, malformed, or unknown credentials: HTTP `401`, `code:
  unauthorized`.
- Authenticated caller without required node action: HTTP `403`, `code:
  forbidden`.

Canonical error envelope:

```json
{
  "ok": false,
  "error": {
    "code": "provider_unavailable",
    "message": "runtime adapter docker is unavailable",
    "retryable": true
  },
  "links": {},
  "meta": {"request_id": "req_s003_node_error", "schema_version": "v1"}
}
```

Unauthorized response:

```json
{
  "ok": false,
  "error": {
    "code": "unauthorized",
    "message": "credential is missing, malformed, or unknown",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_node_unauthorized", "schema_version": "v1"}
}
```

Forbidden response:

```json
{
  "ok": false,
  "error": {
    "code": "forbidden",
    "message": "caller is not allowed to perform the node action",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_node_forbidden", "schema_version": "v1"}
}
```

## Runtime/Provider Boundary

C09 may expose `provider_endpoint` for the configured service. C09 must not expose:

- Docker container ID unless explicitly added to node diagnostics.
- Host filesystem paths.
- ComfyUI workflow node IDs.
- Provider queue IDs.
- Generated artifact paths.
