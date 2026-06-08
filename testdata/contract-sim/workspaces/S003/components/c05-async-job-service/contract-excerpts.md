# Contract Excerpts: C05 Async Job Service

## Job States

Allowed states:

- `queued`
- `claimed`
- `running`
- `succeeded`
- `failed`
- `canceled`
- `expired`

Expected S003 happy path:

```text
queued -> claimed -> running -> succeeded
```

## Create Job

Endpoint: `POST /v1/jobs`.

Required header:

```http
Authorization: Bearer token_s003_gateway
Idempotency-Key: idem_s003_c05_create_job
```

The credential must resolve to gateway component subject `sub_gateway_s003`
authorized for `job.create`. It is a component credential, not a public agent
credential.

C05 create-job idempotency protects the component-facing create side effect.
It is separate from C04 public invocation idempotency. C04 still owns the
public invocation idempotency record and C05 still owns job lifecycle state.

Idempotency rules:

- Same idempotency key, same requester, and same request fingerprint returns
  HTTP `200` with the original job data and the current response
  `meta.request_id`.
- Same idempotency key with different request content returns
  HTTP `409` with `idempotency_conflict`.
- C05 idempotency records must not include C04 public request bodies, gateway
  replay status, artifact metadata, provider refs, route-private storage, or
  lease state.

Request:

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

Response status: `201`.

Response data: worker-visible `Job`.

Concrete response envelope:

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
    "resource_refs": [],
    "artifact_refs": [],
    "log_cursor": null,
    "terminal_error": null,
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_create", "schema_version": "v1"}
}
```

Create replay response envelope:

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
    "resource_refs": [],
    "artifact_refs": [],
    "log_cursor": null,
    "terminal_error": null,
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_create_replay", "schema_version": "v1"}
}
```

## Worker-Visible Job

Worker/component APIs may include:

- `job_id`
- `state`
- `created_at`
- `updated_at`
- `status_message`
- `input_summary`
- `metadata.execution_plan`
- `claim`
- `resource_refs`
- `artifact_refs`
- `log_cursor`
- `terminal_error`
- `links`

`metadata.execution_plan` is a documented projection. It is not a direct catalog, runtime, provider, or artifact-store private record.

## Agent-Safe Job

Agent-safe endpoints must omit:

- `metadata`
- `claim`
- route details
- provider endpoint
- node internals
- lease internals

## Job Policy Context

Endpoint: `GET /v1/jobs/job_s003_0001/policy-context`.

Required caller context:

```http
Authorization: Bearer token_s003_gateway
```

The credential must resolve to gateway component subject `sub_gateway_s003`.

This component-facing projection exists only so C04 can ask C08 for
owner-scoped decisions without C08 reading C05 internals. It returns policy
context only and must not include execution plans, logs, provider refs, runtime
details, worker claims, or artifact metadata.

Response data:

```json
{
  "resource_kind": "job",
  "job_id": "job_s003_0001",
  "owner_subject_id": "sub_agent_s003",
  "requester_id": "sub_agent_s003",
  "job_state": "queued",
  "policy_state": "active"
}
```

Response envelope:

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

Running response envelope:

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

Succeeded response envelope:

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

If the job does not exist, C05 returns the standard `not_found` error envelope.

## Claim Job

Endpoint: `POST /v1/jobs/job_s003_0001/claim`.

Required caller context:

```http
Authorization: Bearer token_s003_runner
```

The credential must resolve to a worker or component subject authorized for
`job.execute`. `worker_id` identifies the runner instance making the claim; it
is not a substitute for authentication. If the authenticated worker subject is
not allowed to use the supplied `worker_id`, C05 returns `forbidden`.

Request:

```json
{
  "worker_id": "runner_s003_0001",
  "lease_seconds": 60
}
```

Claim rules:

- `queued -> claimed` succeeds for the first valid worker claim.
- Reclaim by the same `worker_id` before `claim.expires_at` returns the
  existing claim.
- Claim by a different `worker_id` before `claim.expires_at` returns
  `worker_conflict`.
- If `claim.expires_at` has passed and the job is non-terminal, a new worker
  may claim the job and C05 records the previous claim as stale.
- Terminal jobs cannot be claimed and return `job_terminal`.

Response status: `200`.

Response data includes worker-visible `Job` with:

```json
{
  "job_id": "job_s003_0001",
  "state": "claimed",
  "claim": {
    "worker_id": "runner_s003_0001",
    "claimed_at": "2026-06-05T20:00:01Z",
    "expires_at": "2026-06-05T20:01:01Z"
  },
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

Full response envelope:

```json
{
  "ok": true,
  "data": "<worker-visible Job shown above>",
  "links": {},
  "meta": {"request_id": "req_s003_job_claim", "schema_version": "v1"}
}
```

## Heartbeat

Endpoint: `POST /v1/jobs/job_s003_0001/heartbeat`.

Required caller context:

```http
Authorization: Bearer token_s003_runner
```

Request:

```json
{
  "worker_id": "runner_s003_0001",
  "status_message": "waiting for gpu lease"
}
```

Heartbeat refreshes the worker claim and may update `status_message`.
If `worker_id` does not match the active claim, C05 returns `forbidden`. If the
claim has expired, C05 returns `claim_expired` and does not change job state.

C05 claim renewal is independent from C06 lease renewal. A C05 heartbeat does
not imply that any C06 resource lease was refreshed, and C06 lease expiration
does not automatically expire or revoke the C05 job claim. The runner must use
the public C05 and C06 contracts separately.

Non-transition heartbeat response data:

```json
{
  "job_id": "job_s003_0001",
  "state": "claimed",
  "updated_at": "2026-06-05T20:00:02Z",
  "status_message": "waiting for gpu lease",
  "claim": {
    "worker_id": "runner_s003_0001",
    "claimed_at": "2026-06-05T20:00:01Z",
    "expires_at": "2026-06-05T20:01:02Z"
  },
  "artifact_refs": [],
  "log_cursor": null,
  "terminal_error": null,
  "links": {}
}
```

Non-transition heartbeat response envelope:

```json
{
  "ok": true,
  "data": {
    "job_id": "job_s003_0001",
    "state": "claimed",
    "updated_at": "2026-06-05T20:00:02Z",
    "status_message": "waiting for gpu lease",
    "claim": {
      "worker_id": "runner_s003_0001",
      "claimed_at": "2026-06-05T20:00:01Z",
      "expires_at": "2026-06-05T20:01:02Z"
    },
    "artifact_refs": [],
    "log_cursor": null,
    "terminal_error": null,
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_heartbeat_waiting", "schema_version": "v1"}
}
```

## Mark Running

Endpoint: `POST /v1/jobs/job_s003_0001/heartbeat`.

There is no separate mark-running endpoint in S003. A heartbeat may request
`claimed -> running` only through `transition_to: running`. Free-form
`status_message` text never drives state changes.

Request:

```json
{
  "worker_id": "runner_s003_0001",
  "transition_to": "running",
  "status_message": "running provider invocation"
}
```

Heartbeat response data:

```json
{
  "job_id": "job_s003_0001",
  "state": "running",
  "updated_at": "2026-06-05T20:00:03Z",
  "status_message": "running provider invocation",
  "claim": {
    "worker_id": "runner_s003_0001",
    "claimed_at": "2026-06-05T20:00:01Z",
    "expires_at": "2026-06-05T20:01:03Z"
  },
  "artifact_refs": [],
  "log_cursor": "cursor_s003_logs_0001",
  "terminal_error": null,
  "links": {}
}
```

Heartbeat running response envelope:

```json
{
  "ok": true,
  "data": {
    "job_id": "job_s003_0001",
    "state": "running",
    "updated_at": "2026-06-05T20:00:03Z",
    "status_message": "running provider invocation",
    "claim": {
      "worker_id": "runner_s003_0001",
      "claimed_at": "2026-06-05T20:00:01Z",
      "expires_at": "2026-06-05T20:01:03Z"
    },
    "artifact_refs": [],
    "log_cursor": "cursor_s003_logs_0001",
    "terminal_error": null,
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_heartbeat_running", "schema_version": "v1"}
}
```

## Running Claim Heartbeat

The lease-expiration failure branch uses a C05 non-transition heartbeat to keep
the active job claim writable while intentionally not refreshing the C06
resource lease.

Request:

```json
{
  "worker_id": "runner_s003_0001",
  "status_message": "monitoring provider invocation"
}
```

Running non-transition heartbeat response envelope:

```json
{
  "ok": true,
  "data": {
    "job_id": "job_s003_0001",
    "state": "running",
    "updated_at": "2026-06-05T20:00:32Z",
    "status_message": "monitoring provider invocation",
    "claim": {
      "worker_id": "runner_s003_0001",
      "claimed_at": "2026-06-05T20:00:01Z",
      "expires_at": "2026-06-05T20:01:32Z"
    },
    "artifact_refs": [],
    "log_cursor": "cursor_s003_logs_0002",
    "terminal_error": null,
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_heartbeat_claim_renewed", "schema_version": "v1"}
}
```

## Logs

Append endpoint: `POST /v1/jobs/job_s003_0001/logs`.

Read endpoint: `GET /v1/jobs/job_s003_0001/logs`.

C05 owns log storage, cursors, and worker append validation. C04 owns public
auth and projection for `/v1/agent/jobs/{job_id}/logs`.

Read requests are component-facing and require:

```http
Authorization: Bearer token_s003_gateway
```

The credential must resolve to gateway component subject `sub_gateway_s003`.
C05 returns component-facing log entries only after C04 has handled public
caller auth and policy checks. C05 still owns log cursors and storage.

Append requests are worker-authored and require:

```http
Authorization: Bearer token_s003_runner
```

The request body must include `worker_id`. C05 validates that `worker_id`
matches the active job claim before appending worker-authored logs. Worker
mismatch returns `forbidden`; expired claim returns `claim_expired`.

Append request:

```json
{
  "worker_id": "runner_s003_0001",
  "entries": [
    {
      "timestamp": "2026-06-05T20:00:01Z",
      "level": "info",
      "message": "claimed job",
      "fields": {}
    }
  ]
}
```

Append response data:

```json
{
  "items": [
    {
      "timestamp": "2026-06-05T20:00:01Z",
      "level": "info",
      "message": "claimed job",
      "fields": {}
    }
  ],
  "next_cursor": "cursor_s003_logs_0001"
}
```

Append response envelope:

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
      }
    ],
    "next_cursor": "cursor_s003_logs_0001"
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_log_claimed", "schema_version": "v1"}
}
```

Second append request:

```json
{
  "worker_id": "runner_s003_0001",
  "entries": [
    {
      "timestamp": "2026-06-05T20:00:03Z",
      "level": "info",
      "message": "running provider invocation",
      "fields": {"provider": "svc_comfyui_gpu"}
    }
  ]
}
```

Second append response envelope:

```json
{
  "ok": true,
  "data": {
    "items": [
      {
        "timestamp": "2026-06-05T20:00:03Z",
        "level": "info",
        "message": "running provider invocation",
        "fields": {"provider": "svc_comfyui_gpu"}
      }
    ],
    "next_cursor": "cursor_s003_logs_0002"
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_log_running", "schema_version": "v1"}
}
```

Read response data:

```json
{
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
      "fields": {"provider": "svc_comfyui_gpu"}
    }
  ],
  "next_cursor": "cursor_s003_logs_0002"
}
```

Read response envelope:

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
        "fields": {"provider": "svc_comfyui_gpu"}
      }
    ],
    "next_cursor": "cursor_s003_logs_0002"
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_logs_read", "schema_version": "v1"}
}
```

Final read request:

```http
GET /v1/jobs/job_s003_0001/logs?cursor=cursor_s003_logs_0002&limit=50
Authorization: Bearer token_s003_gateway
```

Precondition: the caller previously received `next_cursor:
cursor_s003_logs_0002` from `req_s003_job_logs_read`, and no later C05 log
entry exists on the happy-path branch.

Final read response envelope:

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

## Complete Job

Endpoint: `POST /v1/jobs/job_s003_0001/complete`.

Required caller context:

```http
Authorization: Bearer token_s003_runner
```

Request:

```json
{
  "worker_id": "runner_s003_0001",
  "artifact_refs": ["art_s003_0001"],
  "output": {"artifact_refs": ["art_s003_0001"]}
}
```

Completion stores artifact refs only. Artifact metadata and bytes remain owned by C07.

Complete response data:

```json
{
  "job_id": "job_s003_0001",
  "state": "succeeded",
  "updated_at": "2026-06-05T20:00:46Z",
  "status_message": "completed",
  "artifact_refs": ["art_s003_0001"],
  "terminal_error": null,
  "links": {}
}
```

Complete response envelope:

```json
{
  "ok": true,
  "data": {
    "job_id": "job_s003_0001",
    "state": "succeeded",
    "updated_at": "2026-06-05T20:00:46Z",
    "status_message": "completed",
    "artifact_refs": ["art_s003_0001"],
    "terminal_error": null,
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_complete", "schema_version": "v1"}
}
```

Complete/fail rules:

- Only the active `claim.worker_id` may complete or fail a non-terminal job.
- Worker mismatch returns `forbidden`.
- Expired claim returns `claim_expired`.
- Terminal jobs return `job_terminal`.
- Completion stores final C07 artifact refs only.

## Cancel Job

Endpoint: `POST /v1/jobs/job_s003_0001/cancel`.

Required caller context:

```http
Authorization: Bearer token_s003_gateway
Idempotency-Key: idem_s003_c05_cancel_queued
```

S003 supports component-facing cancellation only while the job is queued. C04
owns the public cancel route and public policy checks; C05 owns the job state
transition.

Queued cancel request:

```json
{
  "requester_id": "sub_agent_s003",
  "reason": "canceled by requester"
}
```

Queued cancel response:

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

Running cancellation is deferred in S003. If C04 calls this endpoint after the
job is already running, C05 returns a standard `validation_failed` error
envelope and does not create a hidden cancel-requested state.

## Fail Job

Endpoint: `POST /v1/jobs/job_s003_0001/fail`.

Required caller context:

```http
Authorization: Bearer token_s003_runner
```

Request:

```json
{
  "worker_id": "runner_s003_0001",
  "error": {
    "code": "provider_unavailable",
    "message": "ComfyUI backend is unavailable",
    "retryable": true
  }
}
```

Fail response data:

```json
{
  "job_id": "job_s003_0001",
  "state": "failed",
  "updated_at": "2026-06-05T20:00:08Z",
  "status_message": "ComfyUI backend is unavailable",
  "artifact_refs": [],
  "terminal_error": {
    "code": "provider_unavailable",
    "message": "ComfyUI backend is unavailable",
    "retryable": true
  },
  "links": {}
}
```

Provider failure fail response envelope:

```json
{
  "ok": true,
  "data": {
    "job_id": "job_s003_0001",
    "state": "failed",
    "updated_at": "2026-06-05T20:00:08Z",
    "status_message": "ComfyUI backend is unavailable",
    "artifact_refs": [],
    "terminal_error": {
      "code": "provider_unavailable",
      "message": "ComfyUI backend is unavailable",
      "retryable": true
    },
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_fail_provider", "schema_version": "v1"}
}
```

Provider failure append-log request:

```json
{
  "worker_id": "runner_s003_0001",
  "entries": [
    {
      "timestamp": "2026-06-05T20:00:08Z",
      "level": "error",
      "message": "provider invocation failed",
      "fields": {"code": "provider_unavailable"}
    }
  ]
}
```

Provider failure append-log response data:

```json
{
  "items": [
    {
      "timestamp": "2026-06-05T20:00:08Z",
      "level": "error",
      "message": "provider invocation failed",
      "fields": {"code": "provider_unavailable"}
    }
  ],
  "next_cursor": "cursor_s003_logs_provider_failure"
}
```

Provider failure append-log response envelope:

```json
{
  "ok": true,
  "data": {
    "items": [
      {
        "timestamp": "2026-06-05T20:00:08Z",
        "level": "error",
        "message": "provider invocation failed",
        "fields": {"code": "provider_unavailable"}
      }
    ],
    "next_cursor": "cursor_s003_logs_provider_failure"
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_log_provider_failure", "schema_version": "v1"}
}
```

Provider timeout append-log request:

```json
{
  "worker_id": "runner_s003_0001",
  "entries": [
    {
      "timestamp": "2026-06-05T20:15:08Z",
      "level": "error",
      "message": "provider invocation timed out",
      "fields": {"code": "provider_timeout"}
    }
  ]
}
```

Provider timeout append-log response envelope:

```json
{
  "ok": true,
  "data": {
    "items": [
      {
        "timestamp": "2026-06-05T20:15:08Z",
        "level": "error",
        "message": "provider invocation timed out",
        "fields": {"code": "provider_timeout"}
      }
    ],
    "next_cursor": "cursor_s003_logs_provider_timeout"
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_log_provider_timeout", "schema_version": "v1"}
}
```

Provider timeout failure request:

```json
{
  "worker_id": "runner_s003_0001",
  "error": {
    "code": "provider_timeout",
    "message": "provider invocation timed out",
    "retryable": true
  }
}
```

Provider timeout fail response envelope:

```json
{
  "ok": true,
  "data": {
    "job_id": "job_s003_0001",
    "state": "failed",
    "updated_at": "2026-06-05T20:15:08Z",
    "status_message": "provider invocation timed out",
    "artifact_refs": [],
    "terminal_error": {
      "code": "provider_timeout",
      "message": "provider invocation timed out",
      "retryable": true
    },
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_fail_provider_timeout", "schema_version": "v1"}
}
```

Lease expiration append-log request:

```json
{
  "worker_id": "runner_s003_0001",
  "entries": [
    {
      "timestamp": "2026-06-05T20:01:04Z",
      "level": "error",
      "message": "resource lease expired",
      "fields": {"lease_id": "lease_s003_0001"}
    }
  ]
}
```

Lease expiration append-log response envelope:

```json
{
  "ok": true,
  "data": {
    "items": [
      {
        "timestamp": "2026-06-05T20:01:04Z",
        "level": "error",
        "message": "resource lease expired",
        "fields": {"lease_id": "lease_s003_0001"}
      }
    ],
    "next_cursor": "cursor_s003_logs_lease_expired"
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_log_lease_expired", "schema_version": "v1"}
}
```

Lease expiration failure request:

```json
{
  "worker_id": "runner_s003_0001",
  "error": {
    "code": "lease_expired",
    "message": "resource lease expired before completion",
    "retryable": true
  }
}
```

Lease expiration failure response data:

```json
{
  "job_id": "job_s003_0001",
  "state": "failed",
  "updated_at": "2026-06-05T20:01:04Z",
  "status_message": "resource lease expired before completion",
  "artifact_refs": [],
  "terminal_error": {
    "code": "lease_expired",
    "message": "resource lease expired before completion",
    "retryable": true
  },
  "links": {}
}
```

Lease expiration fail response envelope:

```json
{
  "ok": true,
  "data": {
    "job_id": "job_s003_0001",
    "state": "failed",
    "updated_at": "2026-06-05T20:01:04Z",
    "status_message": "resource lease expired before completion",
    "artifact_refs": [],
    "terminal_error": {
      "code": "lease_expired",
      "message": "resource lease expired before completion",
      "retryable": true
    },
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_fail_lease_expired", "schema_version": "v1"}
}
```

## Agent-Safe Projection Fixture

Endpoint: `GET /v1/jobs/job_s003_0001/agent-projection`.

This is a component-facing projection for C04. C04 owns the public
`/v1/agent/jobs/{job_id}` route and adds public links after authorization.

Required caller context:

```http
Authorization: Bearer token_s003_gateway
```

Queued agent-safe projection:

```json
{
  "ok": true,
  "data": {
    "job_id": "job_s003_0001",
    "state": "queued",
    "created_at": "2026-06-05T20:00:00Z",
    "updated_at": "2026-06-05T20:00:00Z",
    "status_message": null,
    "input_summary": {"prompt_present": true, "width": 1024, "height": 1024},
    "artifact_refs": [],
    "log_cursor": null,
    "terminal_error": null,
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_agent_job_queued", "schema_version": "v1"}
}
```

Running agent-safe projection:

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

Canceled agent-safe projection:

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
  "meta": {"request_id": "req_s003_agent_job_canceled", "schema_version": "v1"}
}
```

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

Provider-failure agent-safe projection:

```json
{
  "ok": true,
  "data": {
    "job_id": "job_s003_0001",
    "state": "failed",
    "created_at": "2026-06-05T20:00:00Z",
    "updated_at": "2026-06-05T20:00:08Z",
    "status_message": "ComfyUI backend is unavailable",
    "input_summary": {"prompt_present": true, "width": 1024, "height": 1024},
    "artifact_refs": [],
    "log_cursor": "cursor_s003_logs_provider_failure",
    "terminal_error": {
      "code": "provider_unavailable",
      "message": "ComfyUI backend is unavailable",
      "retryable": true
    },
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_agent_job_provider_failed", "schema_version": "v1"}
}
```

Provider-timeout agent-safe projection:

```json
{
  "ok": true,
  "data": {
    "job_id": "job_s003_0001",
    "state": "failed",
    "created_at": "2026-06-05T20:00:00Z",
    "updated_at": "2026-06-05T20:15:08Z",
    "status_message": "provider invocation timed out",
    "input_summary": {"prompt_present": true, "width": 1024, "height": 1024},
    "artifact_refs": [],
    "log_cursor": "cursor_s003_logs_provider_timeout",
    "terminal_error": {
      "code": "provider_timeout",
      "message": "provider invocation timed out",
      "retryable": true
    },
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_agent_job_provider_timeout", "schema_version": "v1"}
}
```

Lease-expiration agent-safe projection:

```json
{
  "ok": true,
  "data": {
    "job_id": "job_s003_0001",
    "state": "failed",
    "created_at": "2026-06-05T20:00:00Z",
    "updated_at": "2026-06-05T20:01:04Z",
    "status_message": "resource lease expired before completion",
    "input_summary": {"prompt_present": true, "width": 1024, "height": 1024},
    "artifact_refs": [],
    "log_cursor": "cursor_s003_logs_lease_expired",
    "terminal_error": {
      "code": "lease_expired",
      "message": "resource lease expired before completion",
      "retryable": true
    },
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_agent_job_lease_expired", "schema_version": "v1"}
}
```

## S003 HTTP Status Map

Unless an endpoint above states a more specific status inline, these named S003
C05 fixtures use the following HTTP statuses:

| Fixture request id | HTTP status |
| --- | --- |
| `req_s003_job_create` | `201` |
| `req_s003_job_create_replay` | `200` |
| `req_s003_job_policy_context` | `200` |
| `req_s003_job_policy_context_running` | `200` |
| `req_s003_job_policy_context_succeeded` | `200` |
| `req_s003_job_claim` | `200` |
| `req_s003_job_claim_same_worker_replay` | `200` |
| `req_s003_job_claim_expired_reclaim` | `200` |
| `req_s003_job_heartbeat_waiting` | `200` |
| `req_s003_job_heartbeat_running` | `200` |
| `req_s003_job_heartbeat_claim_renewed` | `200` |
| `req_s003_job_log_claimed` | `200` |
| `req_s003_job_log_running` | `200` |
| `req_s003_job_logs_read` | `200` |
| `req_s003_job_logs_final` | `200` |
| `req_s003_job_complete` | `200` |
| `req_s003_job_cancel_queued` | `200` |
| `req_s003_job_cancel_queued_replay` | `200` |
| `req_s003_job_fail_provider` | `200` |
| `req_s003_job_log_provider_failure` | `200` |
| `req_s003_job_log_provider_timeout` | `200` |
| `req_s003_job_fail_provider_timeout` | `200` |
| `req_s003_job_log_lease_expired` | `200` |
| `req_s003_job_fail_lease_expired` | `200` |
| `req_s003_agent_job_queued` | `200` |
| `req_s003_agent_job_running` | `200` |
| `req_s003_agent_job_canceled` | `200` |
| `req_s003_agent_job` | `200` |
| `req_s003_agent_job_provider_failed` | `200` |
| `req_s003_agent_job_provider_timeout` | `200` |
| `req_s003_agent_job_lease_expired` | `200` |
| `req_s003_job_invalid_transition` | `400` |
| `req_s003_job_worker_unauthorized` | `401` |
| `req_s003_job_gateway_unauthorized` | `401` |
| `req_s003_job_forbidden` | `403` |
| `req_s003_job_log_worker_mismatch` | `403` |
| `req_s003_job_complete_worker_mismatch` | `403` |
| `req_s003_job_fail_worker_mismatch` | `403` |
| `req_s003_job_not_found` | `404` |
| `req_s003_job_worker_conflict` | `409` |
| `req_s003_job_idempotency_conflict` | `409` |
| `req_s003_job_cancel_idempotency_conflict` | `409` |
| `req_s003_job_log_claim_expired` | `409` |
| `req_s003_job_complete_claim_expired` | `409` |
| `req_s003_job_fail_claim_expired` | `409` |
| `req_s003_job_cancel_running_conflict` | `400` |
| `req_s003_job_claim_terminal` | `409` |
| `req_s003_job_complete_terminal` | `409` |
| `req_s003_job_fail_terminal` | `409` |

## Error Shape

Invalid state transitions return the standard error envelope with `code:
job_terminal` or `code: validation_failed`.

Missing jobs or resources return HTTP `404` with `code: not_found`.

Worker mismatch returns HTTP `403` with `code: forbidden`. Invalid worker or
gateway credentials return HTTP `401` with `code: unauthorized`.

Contention and state conflicts return HTTP `409` for `worker_conflict`,
`idempotency_conflict`, `job_terminal`, and `claim_expired`.

Running-cancel conflict is defensive component-facing behavior only. In S003,
C04 checks C08 with `job_state: running` and returns a public `403 forbidden`
policy denial before calling C05.

Invalid transition example:

HTTP status: `400`.

```json
{
  "ok": false,
  "error": {
    "code": "validation_failed",
    "message": "transition_to must be running when heartbeat changes state",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_invalid_transition", "schema_version": "v1"}
}
```

Worker conflict example:

HTTP status: `409`.

```json
{
  "ok": false,
  "error": {
    "code": "worker_conflict",
    "message": "job is already claimed by another worker",
    "retryable": true
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_worker_conflict", "schema_version": "v1"}
}
```

Idempotency conflict example:

HTTP status: `409`.

```json
{
  "ok": false,
  "error": {
    "code": "idempotency_conflict",
    "message": "idempotency key was reused with different job content",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_idempotency_conflict", "schema_version": "v1"}
}
```

Claim expired response shape:

HTTP status: `409`.

```json
{
  "ok": false,
  "error": {
    "code": "claim_expired",
    "message": "worker claim has expired",
    "retryable": true
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_complete_claim_expired", "schema_version": "v1"}
}
```

S003 uses endpoint-specific claim-expired request IDs so replay fixtures can
assert the exact operation:

- `req_s003_job_log_claim_expired` for append-log after claim expiry.
- `req_s003_job_complete_claim_expired` for completion after claim expiry.
- `req_s003_job_fail_claim_expired` for failure after claim expiry.

Missing or invalid worker credential:

HTTP status: `401`.

```json
{
  "ok": false,
  "error": {
    "code": "unauthorized",
    "message": "job worker operation requires a valid runner credential",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_worker_unauthorized", "schema_version": "v1"}
}
```

Missing or invalid component credential:

HTTP status: `401`.

```json
{
  "ok": false,
  "error": {
    "code": "unauthorized",
    "message": "job component operation requires a valid component credential",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_gateway_unauthorized", "schema_version": "v1"}
}
```

Append-log worker mismatch:

HTTP status: `403`.

Precondition fixture:

- Start from `checkpoint_after_claim`.
- Active claim worker: `runner_s003_0001`.
- Claim expires: `2026-06-05T20:01:01Z`.
- Request timestamp: `2026-06-05T20:00:02Z`.
- No terminal transition has occurred.

Request:

```json
{
  "worker_id": "runner_s003_other",
  "entries": [
    {
      "timestamp": "2026-06-05T20:00:02Z",
      "level": "info",
      "message": "attempted append from another worker",
      "fields": {}
    }
  ]
}
```

```json
{
  "ok": false,
  "error": {
    "code": "forbidden",
    "message": "worker_id does not match the active job claim",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_log_worker_mismatch", "schema_version": "v1"}
}
```

Append-log expired claim:

HTTP status: `409`.

Precondition fixture:

- Start from `checkpoint_after_claim`.
- Active claim worker: `runner_s003_0001`.
- Claim expires: `2026-06-05T20:01:01Z`.
- No C05 heartbeat has occurred after the original claim.
- Request timestamp: `2026-06-05T20:01:02Z`, strictly after claim expiry.
- No terminal transition has occurred.

Request:

```json
{
  "worker_id": "runner_s003_0001",
  "entries": [
    {
      "timestamp": "2026-06-05T20:01:02Z",
      "level": "error",
      "message": "append after expired claim",
      "fields": {}
    }
  ]
}
```

```json
{
  "ok": false,
  "error": {
    "code": "claim_expired",
    "message": "worker claim has expired",
    "retryable": true
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_log_claim_expired", "schema_version": "v1"}
}
```

Running-cancel conflict:

HTTP status: `400`.

```json
{
  "ok": false,
  "error": {
    "code": "validation_failed",
    "message": "job cancellation is only available while queued",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_cancel_running_conflict", "schema_version": "v1"}
}
```

Not-found example:

HTTP status: `404`.

```json
{
  "ok": false,
  "error": {
    "code": "not_found",
    "message": "job not found",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_not_found", "schema_version": "v1"}
}
```

Forbidden worker or component operation:

HTTP status: `403`.

```json
{
  "ok": false,
  "error": {
    "code": "forbidden",
    "message": "caller is not authorized for this job operation",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_forbidden", "schema_version": "v1"}
}
```

Terminal job conflict:

HTTP status: `409`.

```json
{
  "ok": false,
  "error": {
    "code": "job_terminal",
    "message": "job is already terminal",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_complete_terminal", "schema_version": "v1"}
}
```

Terminal claim conflict uses the same `job_terminal` envelope with
`meta.request_id: req_s003_job_claim_terminal`.
