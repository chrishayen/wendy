# Contract Excerpts: C10 ComfyUI Provider

## Health

Endpoint: `GET /v1/provider/health`.

Response envelope:

```json
{
  "ok": true,
  "data": {
    "status": "healthy",
    "version": "v1",
    "checked_at": "2026-06-05T20:00:06Z",
    "details": {"backend": "comfyui"}
  },
  "links": {},
  "meta": {"request_id": "req_s003_provider_health", "schema_version": "v1"}
}
```

## Invoke Capability

Endpoint: `POST /v1/provider/capabilities/cap_image_generate_gpu/invoke`.

The provider endpoint supplied by C09/C03 is the origin/base URL, for example
`http://node_linux_gpu:8188`. The endpoint path above is an absolute API path.

Required caller context:

```http
Authorization: Bearer token_s003_runner
```

The bearer credential must resolve to a worker or component subject authorized
for `provider.invoke`. Unknown or malformed credentials receive
`unauthorized`. Authenticated agent-scoped credentials receive `forbidden`.

Required request context fields:

- `subject_id`: original agent or user subject for ownership and audit.
- `request_id`: caller request id for trace correlation.
- `job_id`: opaque job id for correlation.
- `resource_lease_id`: required when `dry_run` is `false`; may be `null` only
  when `dry_run` is `true`.
- `dry_run`: boolean.

Missing any required context field returns the standard error envelope with
`code: validation_failed`. When `dry_run` is `false`,
`resource_lease_id` is required and must be a non-empty opaque lease id.

`dry_run: true` validates authorization, input, and route compatibility but
must not enqueue provider work, call the backend generation engine, or produce
content refs. It returns `content_refs: []`.

Input validation:

- `prompt` is required and must be a non-empty string.
- `width` and `height` are required integers from `64` through `2048`.
- `width` and `height` must be multiples of `8`.
- Invalid input returns the standard error envelope with `code: validation_failed`.

Request:

```json
{
  "input": {
    "prompt": "a clean product photo of a red ceramic mug",
    "width": 1024,
    "height": 1024
  },
  "context": {
    "subject_id": "sub_agent_s003",
    "request_id": "req_s003_provider_0001",
    "job_id": "job_s003_0001",
    "resource_lease_id": "lease_s003_0001",
    "dry_run": false
  }
}
```

Response is blocking from the runner perspective.

Response status: `200`.

Response envelope:

```json
{
  "ok": true,
  "data": {
    "output": {
      "result": "image_generated",
      "media_type": "image/png",
      "filename": "job_s003_0001.png"
    },
    "content_refs": [
      {
        "content_ref": "pcr_s003_0001",
        "name": "job_s003_0001.png",
        "media_type": "image/png",
        "size": 68,
        "checksum": "sha256:4b5c5c92cec3b23e6a294fc0eea43234ef5126c5a64f4c6c531ac8430ab0b844",
        "expires_at": "2026-06-05T20:15:00Z"
      }
    ]
  },
  "links": {},
  "meta": {"request_id": "req_s003_provider_invoke", "schema_version": "v1"}
}
```

Dry-run response envelope:

Response status: `200`.

```json
{
  "ok": true,
  "data": {
    "output": {
      "result": "dry_run_valid",
      "media_type": "image/png",
      "filename": null
    },
    "content_refs": []
  },
  "links": {},
  "meta": {"request_id": "req_s003_provider_dry_run", "schema_version": "v1"}
}
```

Timeout and cancellation rules:

- The runner enforces `timeout_seconds` from the execution plan.
- If C10 detects its own backend timeout before the runner disconnects, it
  returns `provider_timeout` with `retryable: true`.
- The `provider_backend_unavailable` fixture is triggered by scenario
  checkpoint `provider_backend_down_before_invoke`, where the provider adapter
  is reachable but its ComfyUI backend health check fails before generation.
- The `provider_timeout` fixture is triggered by scenario checkpoint
  `provider_generation_exceeds_timeout`, where the backend accepts work but C10
  detects that provider execution exceeded the invocation timeout.
- If the runner cancels by closing the request or transport context, C10 may
  stop local work and record the invocation as canceled. S003 does not define a
  provider-run status or cancel endpoint.
- Provider-local cancellation state must not be exposed to agents.

## Boundary Rule

The provider response above is normalized provider output for the runner. The runner still uploads content through C07 and receives the final C07 artifact ID. Provider-local content refs and paths must not be exposed to the agent.
C10 exposes no provider-run status or cancel state. Accidental requests to
provider-run status/cancel routes are handled by the API router as undefined
routes, returning the standard `404 not_found` envelope; they do not create
C10-owned run state.

Undefined provider-run route example:

Example request paths:

```http
GET /v1/provider/runs/provider_run_s003_0001
```

```http
POST /v1/provider/runs/provider_run_s003_0001/cancel
```

HTTP status: `404`.

```json
{
  "ok": false,
  "error": {
    "code": "not_found",
    "message": "route not found",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_provider_run_route_not_found", "schema_version": "v1"}
}
```

## Provider Content Handoff

Endpoint: `GET /v1/provider/artifacts/pcr_s003_0001/content`.

This endpoint is runner-facing only. It returns provider result bytes so the runner can upload them to C07. It is not an agent endpoint and does not replace C07 artifact retrieval.

Required caller context:

```http
Authorization: Bearer token_s003_runner
```

The bearer credential must resolve to a worker or component subject authorized
for `provider.invoke`. Missing, malformed, or unknown credentials receive
`unauthorized`. Authenticated agent-scoped credentials receive `forbidden`.

Success response:

```http
HTTP/1.1 200 OK
Content-Type: image/png
Content-Length: 68
Digest: sha-256=S1xcks7Dsj5qKU/A7qQyNO9RJsWmT0xsUxrIQwqwuEQ=
```

Local role-play byte fixture:

- Fixture id: `provider_png_s003_0001`.
- Fixture body: 68-byte PNG.
- SHA-256: `4b5c5c92cec3b23e6a294fc0eea43234ef5126c5a64f4c6c531ac8430ab0b844`.

Failure responses:

- Unknown or expired `content_ref`: `not_found`.
- Missing, malformed, or unknown credentials: `unauthorized`.
- Caller is not runner/component scoped for the invocation: `forbidden`.
- Provider-local content failed validation: `provider_unavailable`.

Unknown or expired ref response:

```json
{
  "ok": false,
  "error": {
    "code": "not_found",
    "message": "provider content reference not found",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_provider_content_not_found", "schema_version": "v1"}
}
```

Forbidden response:

```json
{
  "ok": false,
  "error": {
    "code": "forbidden",
    "message": "provider content reference is runner-only",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_provider_content_forbidden", "schema_version": "v1"}
}
```

Unauthorized content response:

```json
{
  "ok": false,
  "error": {
    "code": "unauthorized",
    "message": "provider content retrieval requires a valid runner or component credential",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_provider_content_unauthorized", "schema_version": "v1"}
}
```

Provider unavailable response:

```json
{
  "ok": false,
  "error": {
    "code": "provider_unavailable",
    "message": "provider content could not be read",
    "retryable": true
  },
  "links": {},
  "meta": {"request_id": "req_s003_provider_content_unavailable", "schema_version": "v1"}
}
```

Rules:

- `content_ref` is opaque and starts with `pcr_`.
- `content_ref` is not a filesystem path, ComfyUI node ID, queue ID, or durable artifact ID.
- `content_ref` is scoped to the provider invocation result.
- The runner may dereference it only to upload bytes into C07.
- Agent-facing responses must contain final C07 artifact IDs, not provider content refs.
- A `content_ref` expires at the instant in `expires_at`. Requests at or after
  that timestamp return `not_found`. Providers may delete expired local bytes
  at any time after expiry.

Invalid input example:

HTTP status: `400`.

```json
{
  "ok": false,
  "error": {
    "code": "validation_failed",
    "message": "width and height must be multiples of 8 between 64 and 2048",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_provider_invalid", "schema_version": "v1"}
}
```

Missing context example:

HTTP status: `400`.

```json
{
  "ok": false,
  "error": {
    "code": "validation_failed",
    "message": "context.subject_id, context.job_id, context.resource_lease_id, and context.dry_run are required",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_provider_missing_context", "schema_version": "v1"}
}
```

Backend unavailable response:

HTTP status: `503`.

```json
{
  "ok": false,
  "error": {
    "code": "provider_unavailable",
    "message": "ComfyUI backend is unavailable",
    "retryable": true
  },
  "links": {},
  "meta": {"request_id": "req_s003_provider_backend_unavailable", "schema_version": "v1"}
}
```

Provider timeout response:

HTTP status: `504`.

```json
{
  "ok": false,
  "error": {
    "code": "provider_timeout",
    "message": "provider invocation timed out",
    "retryable": true
  },
  "links": {},
  "meta": {"request_id": "req_s003_provider_timeout", "schema_version": "v1"}
}
```

Unauthorized invoke example:

HTTP status: `401`.

```json
{
  "ok": false,
  "error": {
    "code": "unauthorized",
    "message": "provider invocation requires a valid runner or component credential",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_provider_invoke_unauthorized", "schema_version": "v1"}
}
```

Forbidden invoke example:

HTTP status: `403`.

```json
{
  "ok": false,
  "error": {
    "code": "forbidden",
    "message": "provider invocation requires a runner or component credential",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_provider_invoke_forbidden", "schema_version": "v1"}
}
```
