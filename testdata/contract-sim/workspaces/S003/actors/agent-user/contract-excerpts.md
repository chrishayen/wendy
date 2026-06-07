# Contract Excerpts: Agent User

## Public Authorization

Protected public endpoints use:

```http
Authorization: Bearer <token>
```

Endpoints are protected unless the endpoint explicitly says otherwise.

## Common Response Envelope

Success responses:

```json
{
  "ok": true,
  "data": {},
  "links": {},
  "warnings": [],
  "meta": {
    "request_id": "req_...",
    "schema_version": "v1"
  }
}
```

`warnings` is optional. Omit it when there are no warnings; include it as an
array only when the response has warning entries.

Errors:

```json
{
  "ok": false,
  "error": {
    "code": "forbidden",
    "message": "string",
    "retryable": false
  },
  "links": {},
  "meta": {
    "request_id": "req_...",
    "schema_version": "v1"
  }
}
```

## Action Link Shape

Links are objects, not raw strings:

```json
{
  "method": "GET",
  "href": "/v1/agent/jobs/job_0001",
  "description": "Read job status.",
  "idempotency": "none",
  "side_effects": "read"
}
```

Navigation rule: top-level `links` describe operation-level or primary
response actions. `data.links` and item-level `links` describe actions for the
resource or item inside `data`. Agents should follow documented relation names
from either location and must not infer hidden actions from URL structure.

## Discover Tools

Request:

```http
GET /v1/tools
Authorization: Bearer <token>
```

Response data:

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

Full S003 discovery response envelope:

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

## Tool Details

Request:

```http
GET /v1/tools/cap_image_generate_gpu
Authorization: Bearer <token>
```

Response is the same public `Tool` record shown in `GET /v1/tools`, wrapped in
the standard success envelope with `meta.request_id: req_s003_tool_detail`.

## Invoke Tool

Request:

```http
POST /v1/tools/cap_image_generate_gpu/invoke
Authorization: Bearer <token>
Idempotency-Key: idem_s003_0001
Content-Type: application/json
```

Body:

```json
{
  "input": {
    "prompt": "a clean product photo of a red ceramic mug",
    "width": 1024,
    "height": 1024
  },
  "preferred_mode": "async",
  "dry_run": false
}
```

Async response data:

```json
{
  "mode": "async",
  "job_id": "job_s003_0001"
}
```

The response links must include public next actions for status, cancel, logs, and artifacts.
The `cancel` link is valid because the job has just been accepted and is still
cancelable while queued. Later job projections must advertise only actions
valid for that job state.

Async response envelope:

```json
{
  "ok": true,
  "data": {
    "mode": "async",
    "job_id": "job_s003_0001"
  },
  "links": {
    "status": {"method": "GET", "href": "/v1/agent/jobs/job_s003_0001", "description": "Read job status.", "idempotency": "none", "side_effects": "read"},
    "cancel": {"method": "POST", "href": "/v1/agent/jobs/job_s003_0001/cancel", "description": "Cancel job.", "idempotency": "required", "side_effects": "write"},
    "logs": {"method": "GET", "href": "/v1/agent/jobs/job_s003_0001/logs", "description": "Read logs.", "idempotency": "none", "side_effects": "read"},
    "artifacts": {"method": "GET", "href": "/v1/agent/jobs/job_s003_0001/artifacts", "description": "List artifacts.", "idempotency": "none", "side_effects": "read"}
  },
  "meta": {"request_id": "req_s003_invoke", "schema_version": "v1"}
}
```

## Cancel Queued Job

Request:

```http
POST /v1/agent/jobs/job_s003_0001/cancel
Authorization: Bearer <token>
Idempotency-Key: idem_s003_cancel_queued
```

S003 defines queued cancellation only. Running job projections do not advertise
cancel unless a separate running-cancel contract is added.

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
  "meta": {"request_id": "req_s003_agent_cancel_queued", "schema_version": "v1"}
}
```

## Observe Job

Use links from the invocation response when present:

- `GET /v1/agent/jobs/{job_id}`
- `POST /v1/agent/jobs/{job_id}/cancel`
- `GET /v1/agent/jobs/{job_id}/logs`
- `GET /v1/agent/jobs/{job_id}/artifacts`

Agent-safe job responses must not expose worker claim, route metadata, provider endpoints, leases, node IDs, or private provider context.
`log_cursor` on a job projection is the latest stable cursor for the public log
stream at the time the projection was generated. A log-page `next_cursor` is
the opaque cursor to pass when requesting the next page. Agents must not parse
cursor values.

Running job response:

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

Succeeded job response:

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
  "meta": {"request_id": "req_s003_agent_job", "schema_version": "v1"}
}
```

## Read Logs

Request:

```http
GET /v1/agent/jobs/job_s003_0001/logs?cursor=cursor_s003_logs_0001&limit=50
Authorization: Bearer <token>
```

`cursor` is opaque. Omit it to start at the first available page.

Response:

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

Final log page for the succeeded checkpoint:

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

## List Artifacts

Request:

```http
GET /v1/agent/jobs/job_s003_0001/artifacts
Authorization: Bearer <token>
```

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
        "links": {
          "content": {"method": "GET", "href": "/v1/artifacts/art_s003_0001/content", "description": "Read artifact content.", "idempotency": "none", "side_effects": "read"}
        }
      }
    ],
    "next_cursor": null
  },
  "links": {},
  "meta": {"request_id": "req_s003_agent_artifacts", "schema_version": "v1"}
}
```

Agent-facing artifact projections use `links.content` and do not include raw
`download_link` strings in S003.

The content link is a public artifact content route. It returns the canonical
S003 public byte fixture `artifact_png_s003_0001`, size `68`, checksum
`sha256:4b5c5c92cec3b23e6a294fc0eea43234ef5126c5a64f4c6c531ac8430ab0b844`,
and Digest `sha-256=S1xcks7Dsj5qKU/A7qQyNO9RJsWmT0xsUxrIQwqwuEQ=`.
This fixture id is for contract replay and is not a runtime storage path.

## Read Artifact Content

The content link returns binary bytes on success:

```http
HTTP/1.1 200 OK
Content-Type: image/png
Content-Length: 68
Digest: sha-256=S1xcks7Dsj5qKU/A7qQyNO9RJsWmT0xsUxrIQwqwuEQ=
```

Errors return the JSON error envelope:

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
