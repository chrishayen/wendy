# Contract Excerpts: Composition Runner

## Runner Credential

Runner component calls in S003 use:

```http
Authorization: Bearer token_s003_runner
```

The credential resolves through C08 to worker subject `sub_runner_s003`. The
runner still sends `worker_id: runner_s003_0001` where job contracts require
the active worker identity; the bearer credential authenticates the caller and
`worker_id` identifies the runner instance.

## Claim Job

Call C05:

```http
POST /v1/jobs/job_s003_0001/claim
Authorization: Bearer token_s003_runner
```

Request:

```json
{
  "worker_id": "runner_s003_0001",
  "lease_seconds": 60
}
```

Claim response data is worker-visible `Job` and includes `metadata.execution_plan`.

Required execution plan fields:

- `capability_id`
- `subject_id`
- `input`
- `route`
- `resource_selector`
- `timeout_seconds`
- `artifact_hints`
- `provider_context`

## Heartbeat Job

Call C05 while running:

```http
POST /v1/jobs/job_s003_0001/heartbeat
Authorization: Bearer token_s003_runner
```

```json
{
  "worker_id": "runner_s003_0001",
  "transition_to": "running",
  "status_message": "string"
}
```

C05 job claim renewal and C06 resource lease renewal are separate operations.
A C05 heartbeat keeps the job claim writable; it does not refresh the C06
lease. A C06 lease can expire while the runner still has an active C05 claim
that allows it to append logs and fail the job through C05.

## Request Lease

Call C06:

```http
POST /v1/lease-requests
Authorization: Bearer token_s003_runner
```

Request:

```json
{
  "requester_id": "job_s003_0001",
  "resource_selector": "gpu",
  "priority": 0,
  "heartbeat_timeout_seconds": 60
}
```

If granted, use returned `lease.lease_id`.

Heartbeat lease:

```http
POST /v1/leases/lease_s003_0001/heartbeat
Authorization: Bearer token_s003_runner
```

```json
{
  "holder_id": "job_s003_0001"
}
```

Release lease:

```http
POST /v1/leases/lease_s003_0001/release
Authorization: Bearer token_s003_runner
Idempotency-Key: idem_s003_lease_release
```

```json
{
  "holder_id": "job_s003_0001",
  "reason": "job completed"
}
```

## Ensure Runtime

Call C09:

- `GET /v1/node/health` with runner authorization.
- `GET /v1/node/services/svc_comfyui_gpu` with runner authorization.
- `POST /v1/node/services/svc_comfyui_gpu/start` with runner authorization and
  `Idempotency-Key: idem_s003_node_start` when service is stopped or unhealthy.

Use the returned `provider_endpoint`.

`provider_endpoint` is the provider origin/base URL and must not include the
API path prefix. Provider paths remain absolute API paths. Join
`provider_endpoint: http://node_linux_gpu:8188` and
`provider_invoke_path: /v1/provider/capabilities/cap_image_generate_gpu/invoke`
to call `http://node_linux_gpu:8188/v1/provider/capabilities/cap_image_generate_gpu/invoke`.

## Invoke Provider

Call C10:

```http
POST /v1/provider/capabilities/cap_image_generate_gpu/invoke
Authorization: Bearer token_s003_runner
```

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

Provider call is blocking from runner perspective.

Expected response includes `content_refs`, for example:

```json
{
  "ok": true,
  "data": {
    "output": {"result": "image_generated", "media_type": "image/png", "filename": "job_s003_0001.png"},
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
  "meta": {"request_id": "req_s003_provider_invoke", "schema_version": "v1"}
}
```

Provider unavailable response:

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

Fetch provider bytes:

```http
GET /v1/provider/artifacts/pcr_s003_0001/content
Authorization: Bearer token_s003_runner
```

Expected provider content response:

```http
HTTP/1.1 200 OK
Content-Type: image/png
Content-Length: 68
Digest: sha-256=S1xcks7Dsj5qKU/A7qQyNO9RJsWmT0xsUxrIQwqwuEQ=
```

Provider content refs must never be written into C05 or exposed to agents.

## Upload Artifact

Create upload with C07:

```http
POST /v1/artifact-uploads
Authorization: Bearer token_s003_runner
Idempotency-Key: idem_s003_artifact_upload_create
```

```json
{
  "name": "job_s003_0001.png",
  "media_type": "image/png",
  "producer_ref": "job_s003_0001",
  "owner_subject_id": "sub_agent_s003",
  "expected_size": 68,
  "expected_checksum": "sha256:4b5c5c92cec3b23e6a294fc0eea43234ef5126c5a64f4c6c531ac8430ab0b844",
  "metadata": {"capability_id": "cap_image_generate_gpu"}
}
```

Then:

- `PUT /v1/artifact-uploads/{upload_id}/content` with
  `Authorization: Bearer token_s003_runner`,
  `Idempotency-Key: idem_s003_artifact_upload_content`,
  `Content-Type: image/png`, `Content-Length: 68`, and
  `Digest: sha-256=S1xcks7Dsj5qKU/A7qQyNO9RJsWmT0xsUxrIQwqwuEQ=`
- `POST /v1/artifact-uploads/{upload_id}/complete` with
  `Authorization: Bearer token_s003_runner`,
  `Idempotency-Key: idem_s003_artifact_upload_complete`, and:

```json
{
  "checksum": "sha256:4b5c5c92cec3b23e6a294fc0eea43234ef5126c5a64f4c6c531ac8430ab0b844",
  "size": 68
}
```

Use final C07 artifact ID, such as `art_s003_0001`, when completing the job.

The S003 idempotency keys above are fixture values for replay. In an
implementation, the runner generates operation-scoped keys per job/artifact
operation. These keys are transport controls and must not be stored as artifact
metadata or exposed to agents.

## Complete Job

Call C05:

```http
POST /v1/jobs/job_s003_0001/complete
Authorization: Bearer token_s003_runner
```

```json
{
  "worker_id": "runner_s003_0001",
  "artifact_refs": ["art_s003_0001"],
  "output": {"artifact_refs": ["art_s003_0001"]}
}
```

## Failure Rule

On provider, runtime, artifact, or lease failure:

1. Append diagnostic logs to C05.
2. Release any active lease when possible.
3. Fail the job in C05 with a normalized error envelope.

S003 fixture-concrete failure branches are provider unavailable, provider
timeout, and lease expiration. Runtime-start failure and C07
upload/content/complete failure branches are explicitly deferred from S003
fixture readiness; they remain runner-owned failure requirements for a later
scenario.

Provider failure path:

The provider-failure path branches after `checkpoint_after_lease_heartbeat` or
any other state where the runner still has an active C06 lease. The runner uses
the C10 error envelope above, appends a C05 diagnostic log, releases the lease
with `reason: provider failed`, and then fails the C05 job.

```http
POST /v1/jobs/job_s003_0001/logs
Authorization: Bearer token_s003_runner
```

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

```http
POST /v1/jobs/job_s003_0001/fail
Authorization: Bearer token_s003_runner
```

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

Lease expiration path:

The S003 lease-expiration path branches from `checkpoint_after_lease`. The
runner sends a C05 non-transition heartbeat at
`2026-06-05T20:00:32Z`, extending the C05 claim to
`2026-06-05T20:01:32Z`, and intentionally sends no C06 lease heartbeat. The
C06 lease expires at `2026-06-05T20:01:02Z`. At
`2026-06-05T20:01:03Z`, C06 returns distinct expired heartbeat and expired
release responses. At `2026-06-05T20:01:04Z`, while the C05 claim is still
active, the runner appends the lease-expiration diagnostic log and fails the
job through C05.

```http
POST /v1/jobs/job_s003_0001/heartbeat
Authorization: Bearer token_s003_runner
```

```json
{
  "worker_id": "runner_s003_0001",
  "status_message": "monitoring provider invocation"
}
```

```http
POST /v1/jobs/job_s003_0001/logs
Authorization: Bearer token_s003_runner
```

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

```http
POST /v1/jobs/job_s003_0001/fail
Authorization: Bearer token_s003_runner
```

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
