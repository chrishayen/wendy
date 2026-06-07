# Composition Runner Dependency Response Fixtures

These are addressed public-contract fakes for the runner. They are not sibling-folder reads.

## C05 Claim Response

```json
{
  "ok": true,
  "data": {
    "job_id": "job_s003_0001",
    "state": "claimed",
    "created_at": "2026-06-05T20:00:00Z",
    "updated_at": "2026-06-05T20:00:01Z",
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
    "claim": {
      "worker_id": "runner_s003_0001",
      "claimed_at": "2026-06-05T20:00:01Z",
      "expires_at": "2026-06-05T20:01:01Z"
    },
    "artifact_refs": [],
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_claim", "schema_version": "v1"}
}
```

## C05 Heartbeat Running

```json
{
  "ok": true,
  "data": {
    "job_id": "job_s003_0001",
    "state": "running",
    "created_at": "2026-06-05T20:00:00Z",
    "updated_at": "2026-06-05T20:00:03Z",
    "status_message": "running provider invocation",
    "claim": {
      "worker_id": "runner_s003_0001",
      "claimed_at": "2026-06-05T20:00:01Z",
      "expires_at": "2026-06-05T20:01:03Z"
    },
    "input_summary": {"prompt_present": true, "width": 1024, "height": 1024},
    "artifact_refs": [],
    "log_cursor": "cursor_s003_logs_0001",
    "terminal_error": null,
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_heartbeat_running", "schema_version": "v1"}
}
```

## C05 Running Claim Heartbeat

Used only by the lease-expiration failure branch to keep the C05 job claim
active while intentionally not refreshing the C06 resource lease.

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

## C06 Lease Grant

```json
{
  "ok": true,
  "data": {
    "request_id": "lease_req_s003_0001",
    "state": "granted",
    "resource_selector": "gpu",
    "queue_position": null,
    "lease": {
      "lease_id": "lease_s003_0001",
      "resource_id": "res_gpu_0",
      "holder_id": "job_s003_0001",
      "expires_at": "2026-06-05T20:01:02Z",
      "links": {}
    },
    "created_at": "2026-06-05T20:00:02Z",
    "updated_at": "2026-06-05T20:00:02Z"
  },
  "links": {},
  "meta": {"request_id": "req_s003_lease_grant", "schema_version": "v1"}
}
```

## C06 Lease Heartbeat

```json
{
  "ok": true,
  "data": {
    "lease_id": "lease_s003_0001",
    "resource_id": "res_gpu_0",
    "holder_id": "job_s003_0001",
    "expires_at": "2026-06-05T20:01:32Z",
    "links": {
      "heartbeat": {"method": "POST", "href": "/v1/leases/lease_s003_0001/heartbeat", "description": "Refresh lease.", "idempotency": "none", "side_effects": "write"},
      "release": {"method": "POST", "href": "/v1/leases/lease_s003_0001/release", "description": "Release lease.", "idempotency": "required", "side_effects": "write"}
    }
  },
  "links": {},
  "meta": {"request_id": "req_s003_lease_heartbeat", "schema_version": "v1"}
}
```

## Provider Failure Path

Fixture id: `provider_failure_path`.

When C10 returns `provider_unavailable`, the runner appends a diagnostic log,
releases the active lease if possible, and fails the job.
This branch is triggered by scenario checkpoint
`provider_backend_down_before_invoke`; from the runner perspective it is a C10
provider invoke error, not a separate runner-visible health call.

C10 provider unavailable response:

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

C05 append-log request:

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

C05 append-log response:

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

C06 release request:

```http
POST /v1/leases/lease_s003_0001/release
Authorization: Bearer token_s003_runner
Idempotency-Key: idem_s003_lease_release_provider_failure
```

```json
{
  "holder_id": "job_s003_0001",
  "reason": "provider failed"
}
```

C06 release response:

```json
{
  "ok": true,
  "data": {
    "lease_id": "lease_s003_0001",
    "resource_id": "res_gpu_0",
    "holder_id": "job_s003_0001",
    "expires_at": "2026-06-05T20:00:08Z",
    "released_at": "2026-06-05T20:00:08Z",
    "released_by": "sub_runner_s003",
    "release_reason": "provider failed",
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_lease_release_provider_failure", "schema_version": "v1"}
}
```

C05 fail-job request:

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

C05 fail-job response:

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

## Provider Timeout Path

Fixture id: `provider_timeout_path`.

When C10 returns `provider_timeout`, the runner has kept both the C05 job claim
and C06 resource lease alive through their own heartbeat contracts. It then
appends a diagnostic log, releases the active lease, and fails the job. No C07
artifact is created.

The provider-timeout branch preserves the existing failure ordering:

1. append C05 diagnostic log;
2. release active C06 lease when possible;
3. fail C05 job.

Provider-timeout liveness summary:

```json
{
  "cadence_seconds": 30,
  "first_heartbeat_at": "2026-06-05T20:00:37Z",
  "last_heartbeat_at": "2026-06-05T20:14:37Z",
  "heartbeat_count": 29,
  "c05_claim_expires_at": "2026-06-05T20:15:37Z",
  "c06_lease_expires_at": "2026-06-05T20:15:37Z"
}
```

C05 representative heartbeat request:

```json
{
  "worker_id": "runner_s003_0001",
  "status_message": "waiting for provider completion"
}
```

C05 representative heartbeat response at `2026-06-05T20:14:37Z`:

```json
{
  "ok": true,
  "data": {
    "job_id": "job_s003_0001",
    "state": "running",
    "updated_at": "2026-06-05T20:14:37Z",
    "status_message": "waiting for provider completion",
    "claim": {
      "worker_id": "runner_s003_0001",
      "claimed_at": "2026-06-05T20:00:01Z",
      "expires_at": "2026-06-05T20:15:37Z"
    },
    "artifact_refs": [],
    "terminal_error": null,
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_job_heartbeat_provider_timeout_last", "schema_version": "v1"}
}
```

C06 representative heartbeat request:

```json
{
  "holder_id": "job_s003_0001"
}
```

C06 representative heartbeat response at `2026-06-05T20:14:37Z`:

```json
{
  "ok": true,
  "data": {
    "lease_id": "lease_s003_0001",
    "resource_id": "res_gpu_0",
    "holder_id": "job_s003_0001",
    "expires_at": "2026-06-05T20:15:37Z",
    "links": {
      "heartbeat": {"method": "POST", "href": "/v1/leases/lease_s003_0001/heartbeat", "description": "Refresh lease."},
      "release": {"method": "POST", "href": "/v1/leases/lease_s003_0001/release", "description": "Release lease."}
    }
  },
  "links": {},
  "meta": {"request_id": "req_s003_lease_heartbeat_provider_timeout_last", "schema_version": "v1"}
}
```

C10 provider timeout response:

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

C05 append-log request:

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

C05 append-log response:

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

C06 release request:

```http
POST /v1/leases/lease_s003_0001/release
Authorization: Bearer token_s003_runner
Idempotency-Key: idem_s003_lease_release_provider_timeout
```

```json
{
  "holder_id": "job_s003_0001",
  "reason": "provider timed out"
}
```

C06 release response:

```json
{
  "ok": true,
  "data": {
    "lease_id": "lease_s003_0001",
    "resource_id": "res_gpu_0",
    "holder_id": "job_s003_0001",
    "expires_at": "2026-06-05T20:15:08Z",
    "released_at": "2026-06-05T20:15:08Z",
    "released_by": "sub_runner_s003",
    "release_reason": "provider timed out",
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_lease_release_provider_timeout", "schema_version": "v1"}
}
```

C05 fail-job request:

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

C05 fail-job response:

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

## Lease Expiration Path

Fixture id: `lease_expiration_path`.

When C06 returns `lease_expired`, the runner records the C06 response, appends
a diagnostic log, and fails the job. The runner does not need C05 internals to
decide this path.

This path branches from `checkpoint_after_lease`. The runner sends the C05
running claim heartbeat at `2026-06-05T20:00:32Z`, sends no C06 heartbeat, and
lets the C06 lease expire at `2026-06-05T20:01:02Z`.

C06 expired heartbeat response at `2026-06-05T20:01:03Z`:

```json
{
  "ok": false,
  "error": {
    "code": "lease_expired",
    "message": "lease heartbeat rejected because lease has expired",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_lease_heartbeat_expired", "schema_version": "v1"}
}
```

C06 expired release request at `2026-06-05T20:01:03Z`:

```http
POST /v1/leases/lease_s003_0001/release
Authorization: Bearer token_s003_runner
Idempotency-Key: idem_s003_lease_release_expired
```

```json
{
  "holder_id": "job_s003_0001",
  "reason": "lease expired"
}
```

C06 expired release response at `2026-06-05T20:01:03Z`:

```json
{
  "ok": false,
  "error": {
    "code": "lease_expired",
    "message": "lease release rejected because lease has expired",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_lease_release_expired", "schema_version": "v1"}
}
```

C05 append-log request:

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

C05 append-log response:

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

C05 fail-job request:

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

C05 fail-job response:

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

## C06 Lease Release

Request includes `Authorization: Bearer token_s003_runner` and
`Idempotency-Key: idem_s003_lease_release`.

```json
{
  "ok": true,
  "data": {
    "lease_id": "lease_s003_0001",
    "resource_id": "res_gpu_0",
    "holder_id": "job_s003_0001",
    "expires_at": "2026-06-05T20:00:46Z",
    "released_at": "2026-06-05T20:00:46Z",
    "released_by": "sub_runner_s003",
    "release_reason": "job completed",
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_lease_release", "schema_version": "v1"}
}
```

## C09 Node Health

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

## C09 Service Stopped

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
      "start": {"method": "POST", "href": "/v1/node/services/svc_comfyui_gpu/start", "description": "Start service.", "idempotency": "supported", "side_effects": "write"}
    }
  },
  "links": {},
  "meta": {"request_id": "req_s003_node_service_stopped", "schema_version": "v1"}
}
```

## C09 Service Start Accepted

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
      "status": {"method": "GET", "href": "/v1/node/services/svc_comfyui_gpu", "description": "Poll service status.", "idempotency": "none", "side_effects": "read"}
    }
  },
  "links": {},
  "meta": {"request_id": "req_s003_node_start", "schema_version": "v1"}
}
```

## C09 Service Running

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
  "meta": {"request_id": "req_s003_node_service", "schema_version": "v1"}
}
```

## C09 Service Start Replay

Replay of `Idempotency-Key: idem_s003_node_start` after the service is running.

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

## C10 Provider Invoke

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
        "media_type": "image/png",
        "name": "job_s003_0001.png",
        "size": 68,
        "checksum": "sha256:4b5c5c92cec3b23e6a294fc0eea43234ef5126c5a64f4c6c531ac8430ab0b844",
        "expires_at": "2026-06-05T20:15:00Z"
      }
    ]
  },
  "links": {},
  "meta": {"request_id": "req_s003_provider", "schema_version": "v1"}
}
```

## C10 Provider Content Retrieval

This is a local fixture description, not a JSON wire field.

```http
HTTP/1.1 200 OK
Content-Type: image/png
Content-Length: 68
Digest: sha-256=S1xcks7Dsj5qKU/A7qQyNO9RJsWmT0xsUxrIQwqwuEQ=
```

Local byte fixture id: `provider_png_s003_0001`.

## C07 Upload Create

```json
{
  "ok": true,
  "data": {
    "upload_id": "upload_s003_0001",
    "state": "created",
    "name": "job_s003_0001.png",
    "media_type": "image/png",
    "producer_ref": "job_s003_0001",
    "owner_subject_id": "sub_agent_s003",
    "received_size": null,
    "expected_size": 68,
    "expected_checksum": "sha256:4b5c5c92cec3b23e6a294fc0eea43234ef5126c5a64f4c6c531ac8430ab0b844",
    "artifact_id": null,
    "expires_at": "2026-06-05T20:15:00Z",
    "links": {
      "content": {"method": "PUT", "href": "/v1/artifact-uploads/upload_s003_0001/content", "description": "Upload bytes.", "idempotency": "required", "side_effects": "write"},
      "complete": {"method": "POST", "href": "/v1/artifact-uploads/upload_s003_0001/complete", "description": "Complete upload.", "idempotency": "required", "side_effects": "write"}
    }
  },
  "links": {},
  "meta": {"request_id": "req_s003_artifact_upload_create", "schema_version": "v1"}
}
```

## C07 Upload Content

```json
{
  "ok": true,
  "data": {
    "upload_id": "upload_s003_0001",
    "state": "received",
    "name": "job_s003_0001.png",
    "media_type": "image/png",
    "producer_ref": "job_s003_0001",
    "owner_subject_id": "sub_agent_s003",
    "received_size": 68,
    "expected_size": 68,
    "expected_checksum": "sha256:4b5c5c92cec3b23e6a294fc0eea43234ef5126c5a64f4c6c531ac8430ab0b844",
    "artifact_id": null,
    "expires_at": "2026-06-05T20:15:00Z",
    "links": {
      "complete": {"method": "POST", "href": "/v1/artifact-uploads/upload_s003_0001/complete", "description": "Complete upload.", "idempotency": "required", "side_effects": "write"}
    }
  },
  "links": {},
  "meta": {"request_id": "req_s003_artifact_upload_content", "schema_version": "v1"}
}
```

## C07 Upload Complete

```json
{
  "ok": true,
  "data": {
    "artifact_id": "art_s003_0001",
    "name": "job_s003_0001.png",
    "media_type": "image/png",
    "size": 68,
    "checksum": "sha256:4b5c5c92cec3b23e6a294fc0eea43234ef5126c5a64f4c6c531ac8430ab0b844",
    "created_at": "2026-06-05T20:00:45Z",
    "producer_ref": "job_s003_0001",
    "owner_subject_id": "sub_agent_s003",
    "links": {
      "metadata": {"method": "GET", "href": "/v1/artifacts/art_s003_0001", "description": "Read artifact metadata.", "idempotency": "none", "side_effects": "read"},
      "content": {"method": "GET", "href": "/v1/artifacts/art_s003_0001/content", "description": "Read artifact content.", "idempotency": "none", "side_effects": "read"}
    }
  },
  "links": {},
  "meta": {"request_id": "req_s003_artifact_complete", "schema_version": "v1"}
}
```

## C05 Complete Job

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
