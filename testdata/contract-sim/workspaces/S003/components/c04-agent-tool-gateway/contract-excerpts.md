# Contract Excerpts: C04 Agent Tool Gateway

## Inbound Public API

- `GET /v1/tools`
- `GET /v1/tools/{capability_id}`
- `POST /v1/tools/{capability_id}/invoke`
- `GET /v1/agent/jobs/{job_id}`
- `POST /v1/agent/jobs/{job_id}/cancel`
- `GET /v1/agent/jobs/{job_id}/logs`
- `GET /v1/agent/jobs/{job_id}/artifacts`
- `GET /v1/artifacts/{artifact_id}/content`

## Outbound Dependency Contracts

C04 depends only on public/component-facing contracts. It must address
dependencies by canonical endpoint, not by fixture label.

| Dependency | Endpoint | Purpose |
| --- | --- | --- |
| C08 | `POST /v1/auth/verify` | Verify public caller credentials. |
| C08 | `POST /v1/policy/check` | Check tool, job, and artifact policy decisions. |
| C03 | `GET /v1/catalog/capabilities` | List component-facing capability records. |
| C03 | `GET /v1/catalog/capabilities/{capability_id}/route` | Resolve worker-visible route metadata for invocation. |
| C05 | `POST /v1/jobs` | Create an async job. |
| C05 | `POST /v1/jobs/{job_id}/cancel` | Cancel a queued async job after public authorization. |
| C05 | `GET /v1/jobs/{job_id}/policy-context` | Fetch minimal job policy context. |
| C05 | `GET /v1/jobs/{job_id}/agent-projection` | Fetch agent-safe job data for C04 to expose publicly. |
| C05 | `GET /v1/jobs/{job_id}/logs` | Fetch log entries after C04 authorizes the public read. |
| C07 | `GET /v1/artifacts/{artifact_id}/policy-context` | Fetch minimal artifact policy context. |
| C07 | `GET /v1/artifacts?producer_ref={job_id}` | List artifacts after C04 authorizes the collection. |
| C07 | `GET /v1/artifacts/{artifact_id}/content` | Fetch artifact bytes after C04 authorizes content read. |

C04 owns the public `/v1/agent/...` routes. C05 may expose component-facing
agent-safe projections, but C05 does not serve public agent routes in S003.

C04 authenticates component-facing calls to C05 with:

```http
Authorization: Bearer token_s003_gateway
```

This credential resolves through C08 to component subject `sub_gateway_s003`.
It is not a public caller credential and must never be returned to agents.

## Authorization

Public protected requests include:

```http
Authorization: Bearer <token>
```

Success envelope rule: `warnings` is optional and omitted when empty. Top-level
`links` are operation-level actions; `data.links` and item links are
resource/entity actions.

Gateway verifies credentials with C08:

```json
{
  "credential": "Bearer <token>",
  "context": {"surface": "public_api"}
}
```

Expected auth response data:

```json
{
  "valid": true,
  "subject_id": "sub_agent_s003",
  "scopes": ["agent"]
}
```

Gateway checks policy with C08:

```json
{
  "subject_id": "sub_agent_s003",
  "action": "tool.discover",
  "resource": "tools",
  "context": {"operation": "listTools"}
}
```

For per-capability visibility, use `action: tool.discover` and `resource: cap_image_generate_gpu`.

For invocation, use `action: tool.invoke` and `resource: cap_image_generate_gpu`.

For public observation and retrieval, C04 checks policy with C08 before calling
C05 or C07 for agent-facing data:

- Job status and logs: `action: job.read`, `resource: job_s003_0001`,
  `context.owner_subject_id: sub_agent_s003`.
- Job artifacts and artifact content: `action: artifact.read`, `resource:
  art_s003_0001`, `context.owner_subject_id: sub_agent_s003`,
  `context.producer_ref: job_s003_0001`.
- Job artifact collection before artifact IDs are known: `action:
  artifact.read`, `resource: job_artifacts:job_s003_0001`,
  `context.owner_subject_id: sub_agent_s003`, `context.producer_ref:
  job_s003_0001`.

C04 must deny when required owner context is missing. C04 obtains owner context
from minimal C05/C07 `policy-context` projections. It must not ask C08 to read
C05 or C07 internals.

Read/content flow:

1. Verify caller credential.
2. Fetch minimal policy context from the owning component:
   - C05 `GET /v1/jobs/{job_id}/policy-context` for job status and logs.
   - C07 `GET /v1/artifacts/{artifact_id}/policy-context` for artifact content.
3. Deny with `404 not_found` if the owning component reports the resource does
   not exist.
4. Return `500 internal_error` if C04 cannot obtain or build required policy
   context for an existing resource because of a C04/component contract bug.
   C08 explicit `missing_context` denials still map to `403 forbidden`.
5. Ask C08 for `job.read` or `artifact.read` using that context.
6. Only after C08 allows, call the agent-safe C05/C07 projection or stream C07
   artifact content.

Policy context projections are public component contracts. They contain only
opaque ids, owner subject, resource kind, and policy-relevant state.

Public error status fixtures:

- Idempotency conflict: HTTP `409`, `code: idempotency_conflict`.
- C08 explicit `missing_context` denial: HTTP `403`, `code: forbidden`.
- Missing job or artifact from owner component: HTTP `404`, `code: not_found`.
- C04 failed to obtain/build required context for an existing resource: HTTP
  `500`, `code: internal_error`.

Artifact-list flow for `GET /v1/agent/jobs/{job_id}/artifacts`:

1. Verify caller credential.
2. Fetch C05 `GET /v1/jobs/{job_id}/policy-context`.
3. Deny if C05 returns no owner context.
4. Ask C08 for `artifact.read` on `job_artifacts:{job_id}` using
   `job_id`, `producer_ref: {job_id}`, and `owner_subject_id`.
5. Only after allow, call C07 `GET /v1/artifacts?producer_ref={job_id}`.
6. Project C07's component-facing artifact list into the agent-safe response.

C04 must not read, cache, synthesize, or own C07 artifact metadata beyond
projecting C07's component-facing response into the agent-safe response shape.

## Discovery Flow

1. Verify caller credential.
2. Check `tool.discover` on `tools`.
3. Ask C03 for catalog capabilities.
4. Filter each candidate with `tool.discover` on the capability ID.
5. Project allowed capabilities into public `Tool` records.

C04 must project C03 `input_schema` and `output_schema` exactly. C04 does not
own or rewrite those schemas. Any C04 local schema example is a replay fixture
copied from the C03 capability record.

Tool list response data:

```json
{
  "items": [
    {
      "id": "cap_image_generate_gpu",
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
      "resource_hints": [{"selector": "gpu", "required": true}],
      "artifact_hints": [{"media_type": "image/png", "count": "one"}],
      "links": {
        "invoke": {
          "method": "POST",
          "href": "/v1/tools/cap_image_generate_gpu/invoke",
          "description": "Invoke tool.",
          "idempotency": "required",
          "side_effects": "external"
        },
        "details": {
          "method": "GET",
          "href": "/v1/tools/cap_image_generate_gpu",
          "description": "Read tool details.",
          "idempotency": "none",
          "side_effects": "read"
        }
      }
    }
  ],
  "next_cursor": null
}
```

Full tool list response envelope:

```json
{
  "ok": true,
  "data": {
    "items": [
      {
        "id": "cap_image_generate_gpu",
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
        "resource_hints": [{"selector": "gpu", "required": true}],
        "artifact_hints": [{"media_type": "image/png", "count": "one"}],
        "links": {
          "invoke": {"method": "POST", "href": "/v1/tools/cap_image_generate_gpu/invoke", "description": "Invoke tool.", "idempotency": "required", "side_effects": "external"},
          "details": {"method": "GET", "href": "/v1/tools/cap_image_generate_gpu", "description": "Read tool details.", "idempotency": "none", "side_effects": "read"}
        }
      }
    ],
    "next_cursor": null
  },
  "links": {},
  "meta": {"request_id": "req_s003_tools_list", "schema_version": "v1"}
}
```

Tool detail response uses the same public `Tool` record for one capability:
C04 serves `GET /v1/tools/{capability_id}` by verifying auth, checking
`tool.discover` for the capability, calling C03
`GET /v1/catalog/capabilities?capability_id={capability_id}`, and projecting
the returned C03 schema exactly.
The S003 C03 dependency fixture for this lookup uses
`meta.request_id: req_s003_catalog_detail_lookup`.

```json
{
  "ok": true,
  "data": {
    "id": "cap_image_generate_gpu",
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
    "resource_hints": [{"selector": "gpu", "required": true}],
    "artifact_hints": [{"media_type": "image/png", "count": "one"}],
    "links": {
      "invoke": {"method": "POST", "href": "/v1/tools/cap_image_generate_gpu/invoke", "description": "Invoke tool.", "idempotency": "required", "side_effects": "external"},
      "details": {"method": "GET", "href": "/v1/tools/cap_image_generate_gpu", "description": "Read tool details.", "idempotency": "none", "side_effects": "read"}
    }
  },
  "links": {},
  "meta": {"request_id": "req_s003_tool_detail", "schema_version": "v1"}
}
```

## Invocation Flow

Required header:

```http
Idempotency-Key: idem_s003_0001
```

Public request body:

```json
{
  "input": {
    "prompt": "a clean product photo of a red ceramic mug",
    "width": 1024,
    "height": 1024
  },
  "dry_run": false,
  "preferred_mode": "async"
}
```

For S003, route is async. Gateway creates a C05 job with:

```http
POST /v1/jobs
Idempotency-Key: idem_s003_c05_create_job
```

The C05 idempotency key is a component-facing guard for the create-job side
effect. It is generated from C04's public invocation handling, but it is not
the public invocation idempotency key and must not be exposed to agents.

```json
{
  "requester_id": "sub_agent_s003",
  "capability_id": "cap_image_generate_gpu",
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
  }
}
```

The gateway must not expose `metadata.execution_plan` in public responses.

Async public response data:

```json
{
  "mode": "async",
  "job_id": "job_s003_0001"
}
```

Public response links must include:

- `status`: `GET /v1/agent/jobs/job_s003_0001`
- `cancel`: `POST /v1/agent/jobs/job_s003_0001/cancel` while the job is
  queued.
- `logs`: `GET /v1/agent/jobs/job_s003_0001/logs`
- `artifacts`: `GET /v1/agent/jobs/job_s003_0001/artifacts`

Full async response envelope:

```json
{
  "ok": true,
  "data": {
    "mode": "async",
    "job_id": "job_s003_0001"
  },
  "links": {
    "status": {
      "method": "GET",
      "href": "/v1/agent/jobs/job_s003_0001",
      "description": "Read job status.",
      "idempotency": "none",
      "side_effects": "read"
    },
    "cancel": {
      "method": "POST",
      "href": "/v1/agent/jobs/job_s003_0001/cancel",
      "description": "Cancel job.",
      "idempotency": "required",
      "side_effects": "write"
    },
    "logs": {
      "method": "GET",
      "href": "/v1/agent/jobs/job_s003_0001/logs",
      "description": "Read logs.",
      "idempotency": "none",
      "side_effects": "read"
    },
    "artifacts": {
      "method": "GET",
      "href": "/v1/agent/jobs/job_s003_0001/artifacts",
      "description": "List artifacts.",
      "idempotency": "none",
      "side_effects": "read"
    }
  },
  "meta": {"request_id": "req_s003_invoke", "schema_version": "v1"}
}
```

If the same idempotency key is replayed with the same request, C04 returns the
same `job_id`. If the same idempotency key is reused with a different request,
C04 returns the standard error envelope with `code: idempotency_conflict`.
The public HTTP status for that conflict is `409`.

C04 owns public invocation idempotency records:

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

C04 idempotency records must not include job lifecycle state, execution plan,
artifact metadata, provider refs, route metadata, node state, lease state, or
storage details.

## Public Job Projection

Agent job responses use `AgentJob`, not worker `Job`.

Allowed public fields:

- `job_id`
- `state`
- `created_at`
- `updated_at`
- `status_message`
- `input_summary`
- `artifact_refs`
- `log_cursor`
- `terminal_error`
- `links`

Forbidden public fields:

- worker claim
- execution plan
- route metadata
- provider endpoint
- node ID
- lease ID unless intentionally exposed as an opaque resource reference
- provider-private context

`log_cursor` is the latest stable cursor for the public log stream at the time
C04 generated the job projection. A log-page `next_cursor` is the opaque cursor
to pass as the next `cursor` query value. C04 forwards public `cursor` and
validated/clamped `limit` query values to C05; C05 owns cursor interpretation
and storage.

Status request target:

```http
GET /v1/agent/jobs/job_s003_0001
```

Non-terminal response example:

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
    "links": {
      "logs": {"method": "GET", "href": "/v1/agent/jobs/job_s003_0001/logs", "description": "Read logs.", "idempotency": "none", "side_effects": "read"},
      "artifacts": {"method": "GET", "href": "/v1/agent/jobs/job_s003_0001/artifacts", "description": "List artifacts.", "idempotency": "none", "side_effects": "read"}
    }
  },
  "links": {},
  "meta": {"request_id": "req_s003_agent_job_running", "schema_version": "v1"}
}
```

## Public Artifact Content

The agent may follow artifact content links returned through the public artifact projection:

```http
GET /v1/artifacts/art_s003_0001/content
```

S003 public behavior: C04 owns the public content route, authorizes through C08,
calls C07 with `Authorization: Bearer token_s003_gateway`, proxies C07 content,
and returns the binary response. It must not expose storage paths, provider
content refs, node IDs, or runtime details. Success returns:

```http
HTTP/1.1 200 OK
Content-Type: image/png
Content-Length: 68
Digest: sha-256=S1xcks7Dsj5qKU/A7qQyNO9RJsWmT0xsUxrIQwqwuEQ=
```

Canonical public byte fixture id: `provider_png_s003_0001`. The fixture id is
only for contract replay; it is not a runtime storage path.

Errors return the standard JSON error envelope.

## Public Cancel

S003 supports public cancellation only while the job is still queued. C04 must
not advertise a cancel link on running or terminal job projections unless a
separate running-cancel contract is defined and fixture-backed.

Request:

```http
POST /v1/agent/jobs/job_s003_0001/cancel
Authorization: Bearer token_s003_agent
Idempotency-Key: idem_s003_cancel_queued
```

C04 verifies the public caller, fetches C05 policy context, checks C08
`job.cancel`, then calls C05:

```http
POST /v1/jobs/job_s003_0001/cancel
Authorization: Bearer token_s003_gateway
Idempotency-Key: idem_s003_c05_cancel_queued
```

Public queued-cancel response:

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
  "meta": {"request_id": "req_s003_agent_cancel_queued", "schema_version": "v1"}
}
```

Running cancellation is deferred in S003. If a caller posts to the cancel route
after the job is running, C04 asks C08 with `job_state: running`; C08 denies
with `policy_denied`, and C04 returns a public HTTP `403 forbidden` envelope.
C04 does not call C05 for running cancel and does not invent
`cancel_requested`.

Running-cancel policy denial response:

```json
{
  "ok": false,
  "error": {
    "code": "forbidden",
    "message": "job cannot be canceled in its current state",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_agent_cancel_running_forbidden", "schema_version": "v1"}
}
```

## Public Logs

Request:

```http
GET /v1/agent/jobs/job_s003_0001/logs?cursor=cursor_s003_logs_0001&limit=50
```

`cursor` is opaque. Omit `cursor` to read from the first available page.
`limit` defaults to 50. C04 validates and clamps public `limit` to C05's
allowed range before forwarding it; C05 may also clamp defensively and remains
the owner of log cursor semantics. S003 public log `limit` range is `1` through
`100`; over-limit values are clamped to `100`.

Response envelope:

```json
{
  "ok": true,
  "data": {
    "items": [
	      {
	        "timestamp": "2026-06-05T20:00:01Z",
	        "level": "info",
	        "message": "job accepted",
	        "fields": {}
	      },
	      {
	        "timestamp": "2026-06-05T20:00:03Z",
	        "level": "info",
	        "message": "job running",
	        "fields": {}
	      }
    ],
    "next_cursor": "cursor_s003_logs_0002"
  },
  "links": {},
  "meta": {"request_id": "req_s003_agent_logs", "schema_version": "v1"}
}
```

C04 maps component-facing log text to public-safe progress text and drops
component log fields from agent-facing responses. S003 maps `claimed job` to
`job accepted` and `running provider invocation` to `job running`.

Final log page:

```json
{
  "ok": true,
  "data": {
    "items": [],
    "next_cursor": null
  },
  "links": {},
  "meta": {"request_id": "req_s003_agent_logs_final", "schema_version": "v1"}
}
```

## Public Artifact List

Request:

```http
GET /v1/agent/jobs/job_s003_0001/artifacts
```

Response envelope:

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
        "links": {
          "content": {
            "method": "GET",
            "href": "/v1/artifacts/art_s003_0001/content",
            "description": "Read artifact content.",
            "idempotency": "none",
            "side_effects": "read"
          }
        }
      }
    ],
    "next_cursor": null
  },
  "links": {},
  "meta": {"request_id": "req_s003_agent_artifacts", "schema_version": "v1"}
}
```

S003 agent-facing artifact projections use `links.content`. They do not include
raw `download_link` strings.

Binary content error example:

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

## S003 Input Summary Mapping

Public invocation input:

```json
{
  "prompt": "a clean product photo of a red ceramic mug",
  "width": 1024,
  "height": 1024
}
```

Job `input_summary`:

```json
{
  "prompt_present": true,
  "width": 1024,
  "height": 1024
}
```

The full input is worker-visible only through `metadata.execution_plan.input`.
