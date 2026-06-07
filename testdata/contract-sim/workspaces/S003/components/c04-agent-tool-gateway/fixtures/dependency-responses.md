# C04 Dependency Response Fixtures

These are addressed public-contract fakes for C04. They are not sibling-folder reads.

## C08 Auth Success

Request target: C08 `POST /v1/auth/verify`.

Response:

```json
{
  "ok": true,
  "data": {
    "valid": true,
    "subject_id": "sub_agent_s003",
    "scopes": ["agent"]
  },
  "links": {},
  "meta": {"request_id": "req_s003_auth", "schema_version": "v1"}
}
```

## C08 Policy Allows Discovery

Request target: C08 `POST /v1/policy/check`.

Response:

```json
{
  "ok": true,
  "data": {"allowed": true, "reason": "allowed_by_s003_fixture"},
  "links": {},
  "meta": {"request_id": "req_s003_policy_discover", "schema_version": "v1"}
}
```

## C08 Policy Allows Invoke

Request target: C08 `POST /v1/policy/check`.

Response:

```json
{
  "ok": true,
  "data": {
    "allowed": true,
    "reason": "allowed_by_s003_fixture"
  },
  "links": {},
  "meta": {"request_id": "req_s003_policy_invoke", "schema_version": "v1"}
}
```

## C08 Policy Allows Job Read

Request target: C08 `POST /v1/policy/check`.

Request context includes `owner_subject_id: sub_agent_s003`.

Response:

```json
{
  "ok": true,
  "data": {
    "allowed": true,
    "reason": "allowed_by_s003_fixture"
  },
  "links": {},
  "meta": {"request_id": "req_s003_policy_job_read", "schema_version": "v1"}
}
```

## C08 Policy Allows Job Cancel

Request target: C08 `POST /v1/policy/check`.

Request context includes `owner_subject_id: sub_agent_s003` and
`job_state: queued`.

Response:

```json
{
  "ok": true,
  "data": {
    "allowed": true,
    "reason": "allowed_by_s003_fixture"
  },
  "links": {},
  "meta": {"request_id": "req_s003_policy_job_cancel_queued", "schema_version": "v1"}
}
```

## C08 Policy Denies Running Job Cancel

Request target: C08 `POST /v1/policy/check`.

Request context includes `owner_subject_id: sub_agent_s003` and
`job_state: running`.

Response:

```json
{
  "ok": true,
  "data": {
    "allowed": false,
    "reason": "policy_denied"
  },
  "links": {},
  "meta": {"request_id": "req_s003_policy_job_cancel_running_denied", "schema_version": "v1"}
}
```

## C05 Job Policy Context

Request target: C05 `GET /v1/jobs/job_s003_0001/policy-context`.

Request includes `Authorization: Bearer token_s003_gateway`.

Response:

```json
{
  "ok": true,
  "data": {
    "resource_kind": "job",
    "job_id": "job_s003_0001",
    "owner_subject_id": "sub_agent_s003",
    "requester_id": "sub_agent_s003",
    "job_state": "queued",
    "policy_state": "active"
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_policy_context", "schema_version": "v1"}
}
```

## C05 Running Job Policy Context

Request target: C05 `GET /v1/jobs/job_s003_0001/policy-context`.

Request includes `Authorization: Bearer token_s003_gateway`.

Response:

```json
{
  "ok": true,
  "data": {
    "resource_kind": "job",
    "job_id": "job_s003_0001",
    "owner_subject_id": "sub_agent_s003",
    "requester_id": "sub_agent_s003",
    "job_state": "running",
    "policy_state": "active"
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_policy_context_running", "schema_version": "v1"}
}
```

## C05 Succeeded Job Policy Context

Request target: C05 `GET /v1/jobs/job_s003_0001/policy-context`.

Request includes `Authorization: Bearer token_s003_gateway`.

Response:

```json
{
  "ok": true,
  "data": {
    "resource_kind": "job",
    "job_id": "job_s003_0001",
    "owner_subject_id": "sub_agent_s003",
    "requester_id": "sub_agent_s003",
    "job_state": "succeeded",
    "policy_state": "terminal"
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_policy_context_succeeded", "schema_version": "v1"}
}
```

## C05 Job Policy Context Missing

Request target: C05 `GET /v1/jobs/job_s003_0001/policy-context`.

Request includes `Authorization: Bearer token_s003_gateway`.

Response:

```json
{
  "ok": false,
  "error": {
    "code": "not_found",
    "message": "job not found",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_policy_context_missing", "schema_version": "v1"}
}
```

## C08 Policy Allows Artifact Read

Request target: C08 `POST /v1/policy/check`.

Request context includes `owner_subject_id: sub_agent_s003`,
`producer_ref: job_s003_0001`, and `artifact_id: art_s003_0001`.

Response:

```json
{
  "ok": true,
  "data": {
    "allowed": true,
    "reason": "allowed_by_s003_fixture"
  },
  "links": {},
  "meta": {"request_id": "req_s003_policy_artifact_read", "schema_version": "v1"}
}
```

## C08 Policy Allows Job Artifact Collection Read

Request target: C08 `POST /v1/policy/check`.

Request resource: `job_artifacts:job_s003_0001`.

Request context includes `job_id: job_s003_0001`, `producer_ref:
job_s003_0001`, and `owner_subject_id: sub_agent_s003`.

Response:

```json
{
  "ok": true,
  "data": {
    "allowed": true,
    "reason": "allowed_by_s003_fixture"
  },
  "links": {},
  "meta": {"request_id": "req_s003_policy_job_artifacts_read", "schema_version": "v1"}
}
```

## C08 Policy Denied

Request target: C08 `POST /v1/policy/check`.

Response:

```json
{
  "ok": true,
  "data": {
    "allowed": false,
    "reason": "policy_denied"
  },
  "links": {},
  "meta": {"request_id": "req_s003_policy_denied", "schema_version": "v1"}
}
```

## C07 Artifact Policy Context

Response:

```json
{
  "ok": true,
  "data": {
    "resource_kind": "artifact",
    "artifact_id": "art_s003_0001",
    "owner_subject_id": "sub_agent_s003",
    "producer_ref": "job_s003_0001",
    "policy_state": "available"
  },
  "links": {},
  "meta": {"request_id": "req_s003_artifact_policy_context", "schema_version": "v1"}
}
```

## C07 Artifact Policy Context Missing

Response:

```json
{
  "ok": false,
  "error": {
    "code": "not_found",
    "message": "artifact not found",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_artifact_policy_context_missing", "schema_version": "v1"}
}
```

## C03 Capability List

Request target: C03 `GET /v1/catalog/capabilities?limit=50`.

Response:

```json
{
  "ok": true,
  "data": {
    "items": [
      {
        "capability": {
          "id": "cap_image_generate_gpu",
          "service_id": "svc_comfyui_gpu",
          "name": "GPU image generation",
          "description": "Generate an image using a GPU-backed provider.",
          "execution_mode": "async",
          "input_schema": {
            "type": "object",
            "required": ["prompt"],
            "properties": {
              "prompt": {"type": "string"},
              "width": {"type": "integer"},
              "height": {"type": "integer"},
              "seed": {"type": "integer"}
            }
          },
          "output_schema": {
            "type": "object",
            "properties": {
              "artifact_refs": {"type": "array", "items": {"type": "string"}}
            }
          },
          "examples": [],
          "side_effects": "external",
          "resource_hints": [{"selector": "gpu", "required": true, "quantity": 1}],
          "artifact_hints": [{"media_type": "image/png", "count": "one"}],
          "timeout_hint": "15m"
        },
        "route": {
          "capability_id": "cap_image_generate_gpu",
          "service_id": "svc_comfyui_gpu",
          "provider_endpoint": "http://node_linux_gpu:8188",
          "provider_health_path": "/v1/provider/health",
          "provider_invoke_path": "/v1/provider/capabilities/cap_image_generate_gpu/invoke",
          "node_id": "node_linux_gpu",
          "node_managed": true,
          "service_start_mode": "on_demand",
          "resource_hints": [{"selector": "gpu", "required": true, "quantity": 1}],
          "artifact_hints": [{"media_type": "image/png", "count": "one"}]
        },
        "service": {
          "id": "svc_comfyui_gpu",
          "name": "ComfyUI GPU Provider",
          "description": "Node-managed image generation provider.",
          "version": "v1",
          "provider_kind": "comfyui",
          "tags": ["image", "gpu"]
        }
      }
    ],
    "next_cursor": null
  },
  "links": {},
  "meta": {"request_id": "req_s003_catalog_list", "schema_version": "v1"}
}
```

## C03 Capability Detail Lookup

Request target:
`GET /v1/catalog/capabilities?capability_id=cap_image_generate_gpu`.

Response uses the same component-facing C03 capability, route, and service
record as the list fixture.

```json
{
  "ok": true,
  "data": {
    "items": [
      {
        "capability": {
          "id": "cap_image_generate_gpu",
          "service_id": "svc_comfyui_gpu",
          "name": "GPU image generation",
          "description": "Generate an image using a GPU-backed provider.",
          "execution_mode": "async",
          "input_schema": {
            "type": "object",
            "required": ["prompt"],
            "properties": {
              "prompt": {"type": "string"},
              "width": {"type": "integer"},
              "height": {"type": "integer"},
              "seed": {"type": "integer"}
            }
          },
          "output_schema": {
            "type": "object",
            "properties": {
              "artifact_refs": {"type": "array", "items": {"type": "string"}}
            }
          },
          "examples": [],
          "side_effects": "external",
          "resource_hints": [{"selector": "gpu", "required": true, "quantity": 1}],
          "artifact_hints": [{"media_type": "image/png", "count": "one"}],
          "timeout_hint": "15m"
        },
        "route": {
          "capability_id": "cap_image_generate_gpu",
          "service_id": "svc_comfyui_gpu",
          "provider_endpoint": "http://node_linux_gpu:8188",
          "provider_health_path": "/v1/provider/health",
          "provider_invoke_path": "/v1/provider/capabilities/cap_image_generate_gpu/invoke",
          "node_id": "node_linux_gpu",
          "node_managed": true,
          "service_start_mode": "on_demand",
          "resource_hints": [{"selector": "gpu", "required": true, "quantity": 1}],
          "artifact_hints": [{"media_type": "image/png", "count": "one"}]
        },
        "service": {
          "id": "svc_comfyui_gpu",
          "name": "ComfyUI GPU Provider",
          "description": "Node-managed image generation provider.",
          "version": "v1",
          "provider_kind": "comfyui",
          "tags": ["image", "gpu"]
        }
      }
    ],
    "next_cursor": null
  },
  "links": {},
  "meta": {"request_id": "req_s003_catalog_detail_lookup", "schema_version": "v1"}
}
```

## C03 Route Lookup

Request target: C03 `GET /v1/catalog/capabilities/cap_image_generate_gpu/route`.

Response data is the `route` object from the C03 capability list fixture.

## C05 Create Job

Request target: C05 `POST /v1/jobs`.

Request includes `Authorization: Bearer token_s003_gateway` and
`Idempotency-Key: idem_s003_c05_create_job`.

Response:

```json
{
  "ok": true,
  "data": {
    "job_id": "job_s003_0001",
    "state": "queued",
    "created_at": "2026-06-05T20:00:00Z",
    "updated_at": "2026-06-05T20:00:00Z",
    "input_summary": {"prompt_present": true, "width": 1024, "height": 1024},
    "metadata": {
      "execution_plan": {
        "capability_id": "cap_image_generate_gpu",
        "subject_id": "sub_agent_s003",
        "input": {
          "prompt": "a clean product photo of a red ceramic mug",
          "width": 1024,
          "height": 1024
        },
        "route": {
          "capability_id": "cap_image_generate_gpu",
          "service_id": "svc_comfyui_gpu",
          "provider_endpoint": "http://node_linux_gpu:8188",
          "provider_health_path": "/v1/provider/health",
          "provider_invoke_path": "/v1/provider/capabilities/cap_image_generate_gpu/invoke",
          "node_id": "node_linux_gpu",
          "node_managed": true,
          "service_start_mode": "on_demand",
          "resource_hints": [{"selector": "gpu", "required": true, "quantity": 1}],
          "artifact_hints": [{"media_type": "image/png", "count": "one"}]
        },
        "resource_selector": "gpu",
        "timeout_seconds": 900,
        "artifact_hints": [{"media_type": "image/png", "count": "one"}],
        "provider_context": {}
      }
    },
    "artifact_refs": [],
    "log_cursor": null,
    "terminal_error": null,
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_create", "schema_version": "v1"}
}
```

## C05 Agent-Safe Job Projection

Request target: C05 `GET /v1/jobs/job_s003_0001/agent-projection`.

Request includes `Authorization: Bearer token_s003_gateway`.

Response:

```json
{
  "ok": true,
  "data": {
    "job_id": "job_s003_0001",
    "state": "succeeded",
    "created_at": "2026-06-05T20:00:00Z",
    "updated_at": "2026-06-05T20:00:46Z",
    "status_message": "completed",
    "input_summary": {"prompt_present": true, "width": 1024, "height": 1024},
    "artifact_refs": ["art_s003_0001"],
    "log_cursor": "cursor_s003_logs_0002",
    "terminal_error": null,
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_agent_job", "schema_version": "v1"}
}
```

## C05 Agent-Safe Job Projection Running

Request target: C05 `GET /v1/jobs/job_s003_0001/agent-projection`.

Request includes `Authorization: Bearer token_s003_gateway`.

Response:

```json
{
  "ok": true,
  "data": {
    "job_id": "job_s003_0001",
    "state": "running",
    "created_at": "2026-06-05T20:00:00Z",
    "updated_at": "2026-06-05T20:00:03Z",
    "status_message": "running",
    "input_summary": {"prompt_present": true, "width": 1024, "height": 1024},
    "artifact_refs": [],
    "log_cursor": "cursor_s003_logs_0001",
    "terminal_error": null,
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_agent_job_running", "schema_version": "v1"}
}
```

## Public Succeeded Agent Job Projection

C04 projects the C05 succeeded projection into the public response below. It
does not add a cancel link for terminal jobs.

```json
{
  "ok": true,
  "data": {
    "job_id": "job_s003_0001",
    "state": "succeeded",
    "created_at": "2026-06-05T20:00:00Z",
    "updated_at": "2026-06-05T20:00:46Z",
    "status_message": "completed",
    "input_summary": {"prompt_present": true, "width": 1024, "height": 1024},
    "artifact_refs": ["art_s003_0001"],
    "log_cursor": "cursor_s003_logs_0002",
    "terminal_error": null,
    "links": {
      "logs": {"method": "GET", "href": "/v1/agent/jobs/job_s003_0001/logs", "description": "Read logs.", "idempotency": "none", "side_effects": "read"},
      "artifacts": {"method": "GET", "href": "/v1/agent/jobs/job_s003_0001/artifacts", "description": "List artifacts.", "idempotency": "none", "side_effects": "read"}
    }
  },
  "links": {},
  "meta": {"request_id": "req_s003_agent_job_succeeded_public", "schema_version": "v1"}
}
```

## Invoke Idempotency Conflict

Response:

```json
{
  "ok": false,
  "error": {
    "code": "idempotency_conflict",
    "message": "idempotency key was reused with different request content",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_invoke_idempotency_conflict", "schema_version": "v1"}
}
```

HTTP status: `409`.

## C04 Idempotency Replay Record

Local C04-owned state after first successful invoke:

```json
{
  "idempotency_key": "idem_s003_0001",
  "subject_id": "sub_agent_s003",
  "request_fingerprint": "sha256:s003_request_fingerprint",
  "job_id": "job_s003_0001",
  "expires_at": "2026-06-06T20:00:00Z",
  "replay_status": "created"
}
```

Same key and same fingerprint returns the original async response. Same key and
different fingerprint returns the idempotency conflict response above.

## C05 Logs Projection

Request target: C05 `GET /v1/jobs/job_s003_0001/logs`.

Request includes `Authorization: Bearer token_s003_gateway`. C04 forwards
validated/clamped public `cursor` and `limit` query parameters to C05. For the
S003 first page, C04 calls
`GET /v1/jobs/job_s003_0001/logs?cursor=cursor_s003_logs_0001&limit=50`.
If the public request uses `limit=500`, C04 clamps to `limit=100` before
calling C05:
`GET /v1/jobs/job_s003_0001/logs?cursor=cursor_s003_logs_0001&limit=100`.

Response:

```json
{
  "ok": true,
  "data": {
    "items": [
      {
        "timestamp": "2026-06-05T20:00:01Z",
        "level": "info",
        "message": "claimed job",
        "fields": {}
      },
      {
        "timestamp": "2026-06-05T20:00:03Z",
        "level": "info",
        "message": "running provider invocation",
        "fields": {}
      }
    ],
    "next_cursor": "cursor_s003_logs_0002"
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_logs_read", "schema_version": "v1"}
}
```

C04 maps this component-facing response before returning public logs. It drops
component `fields` and maps `claimed job` to `job accepted` and
`running provider invocation` to `job running` in the public response.

## C05 Logs Projection Final Page

Request target:
`GET /v1/jobs/job_s003_0001/logs?cursor=cursor_s003_logs_0002&limit=50`.

Request includes `Authorization: Bearer token_s003_gateway`.

Response:

```json
{
  "ok": true,
  "data": {
    "items": [],
    "next_cursor": null
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_logs_final", "schema_version": "v1"}
}
```

## C08 Missing Context Denial

Response from C08:

```json
{
  "ok": true,
  "data": {
    "allowed": false,
    "reason": "missing_context"
  },
  "links": {},
  "meta": {"request_id": "req_s003_policy_missing_context", "schema_version": "v1"}
}
```

C04 public mapping:

```json
{
  "ok": false,
  "error": {
    "code": "forbidden",
    "message": "required policy context is missing",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_agent_policy_missing_context", "schema_version": "v1"}
}
```

HTTP status: `403`.

## C04 Context Build Failure

If C04 fails to build required policy context for an existing resource, that is
a C04/component contract bug.

```json
{
  "ok": false,
  "error": {
    "code": "internal_error",
    "message": "gateway could not build required policy context",
    "retryable": true
  },
  "links": {},
  "meta": {"request_id": "req_s003_agent_context_internal_error", "schema_version": "v1"}
}
```

HTTP status: `500`.

## C05 Queued Cancel

Request target: C05 `POST /v1/jobs/job_s003_0001/cancel`.

Request includes `Authorization: Bearer token_s003_gateway` and
`Idempotency-Key: idem_s003_c05_cancel_queued`.

Response:

```json
{
  "ok": true,
  "data": {
    "job_id": "job_s003_0001",
    "state": "canceled",
    "created_at": "2026-06-05T20:00:00Z",
    "updated_at": "2026-06-05T20:00:01Z",
    "status_message": "canceled by requester",
    "input_summary": {"prompt_present": true, "width": 1024, "height": 1024},
    "artifact_refs": [],
    "log_cursor": null,
    "terminal_error": {
      "code": "canceled",
      "message": "canceled by requester",
      "retryable": false
    },
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_cancel_queued", "schema_version": "v1"}
}
```

## C07 Artifact Projection

Response:

```json
{
  "ok": true,
  "data": {
    "items": [
      {
        "artifact_id": "art_s003_0001",
        "name": "job_s003_0001.png",
        "media_type": "image/png",
        "size": 68,
        "checksum": "sha256:4b5c5c92cec3b23e6a294fc0eea43234ef5126c5a64f4c6c531ac8430ab0b844",
        "created_at": "2026-06-05T20:00:45Z",
        "producer_ref": "job_s003_0001",
        "owner_subject_id": "sub_agent_s003",
        "links": {
          "content": {"method": "GET", "href": "/v1/artifacts/art_s003_0001/content", "description": "Read artifact content.", "idempotency": "none", "side_effects": "read"}
        }
      }
    ],
    "next_cursor": null
  },
  "links": {},
  "meta": {"request_id": "req_s003_artifact_list", "schema_version": "v1"}
}
```

S003 agent-facing artifact projections use `links.content` only and omit raw
`download_link` strings.

## C07 Artifact Content Forbidden

```json
{
  "ok": false,
  "error": {
    "code": "forbidden",
    "message": "artifact access denied",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_artifact_content_forbidden", "schema_version": "v1"}
}
```

## C07 Artifact Content Read

S003 C04 public behavior is to authorize the public caller, call C07 with
`Authorization: Bearer token_s003_gateway`, and proxy the C07 binary response.
The canonical public byte fixture id is `provider_png_s003_0001`; this is not a
runtime storage path.

```http
HTTP/1.1 200 OK
Content-Type: image/png
Content-Length: 68
Digest: sha-256=S1xcks7Dsj5qKU/A7qQyNO9RJsWmT0xsUxrIQwqwuEQ=
```
