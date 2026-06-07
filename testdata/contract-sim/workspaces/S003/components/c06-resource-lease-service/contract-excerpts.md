# Contract Excerpts: C06 Resource Lease Service

## S003 Resource

```json
{
  "resource_id": "res_gpu_0",
  "selector": "gpu",
  "display_name": "Linux GPU",
  "status": "available",
  "node_id": "node_linux_gpu",
  "metadata": {"kind": "gpu"}
}
```

## Request Lease

Endpoint: `POST /v1/lease-requests`.

Required caller context:

```http
Authorization: Bearer token_s003_runner
```

The credential must resolve to a worker or component subject authorized for
`lease.request`. C06 may verify this directly or through the documented C08
auth/policy contract. `requester_id` and `holder_id` are opaque lease holder
identities; they are not substitutes for caller authentication.

Request:

```json
{
  "requester_id": "job_s003_0001",
  "resource_selector": "gpu",
  "priority": 0,
  "heartbeat_timeout_seconds": 60
}
```

For S003, `requester_id` is the job ID. C06 treats it as an opaque holder identity and does not read C05.
C06 lease lifetime is independent from C05 job claim lifetime. C06 does not
read C05 claim state, and C06 lease expiration does not automatically mutate a
C05 job.

Granted response status: `201`.

Granted response data:

```json
{
  "request_id": "lease_req_s003_0001",
  "state": "granted",
  "resource_selector": "gpu",
  "queue_position": null,
  "lease": {
    "lease_id": "lease_s003_0001",
    "resource_id": "res_gpu_0",
    "holder_id": "job_s003_0001",
    "expires_at": "2026-06-05T20:01:02Z",
    "links": {
      "heartbeat": {"method": "POST", "href": "/v1/leases/lease_s003_0001/heartbeat", "description": "Refresh lease."},
      "release": {"method": "POST", "href": "/v1/leases/lease_s003_0001/release", "description": "Release lease."}
    }
  },
  "created_at": "2026-06-05T20:00:02Z",
  "updated_at": "2026-06-05T20:00:02Z"
}
```

Granted response envelope:

```json
{
  "ok": true,
  "data": "<lease request shown above>",
  "links": {},
  "meta": {"request_id": "req_s003_lease_grant", "schema_version": "v1"}
}
```

Queued response uses `state: pending` and a numeric `queue_position`.

## Heartbeat Lease

Endpoint: `POST /v1/leases/{lease_id}/heartbeat`.

Required caller context:

```http
Authorization: Bearer token_s003_runner
```

Request:

```json
{
  "holder_id": "job_s003_0001"
}
```

Heartbeat extends `expires_at`.

Expiration rule: on a valid heartbeat, C06 sets `expires_at` to heartbeat
receipt time plus `heartbeat_timeout_seconds`. The S003 heartbeat cadence is
30 seconds and the timeout is 60 seconds.

Heartbeat response data:

```json
{
  "lease_id": "lease_s003_0001",
  "resource_id": "res_gpu_0",
  "holder_id": "job_s003_0001",
  "expires_at": "2026-06-05T20:01:32Z",
  "links": {
    "heartbeat": {"method": "POST", "href": "/v1/leases/lease_s003_0001/heartbeat", "description": "Refresh lease."},
    "release": {"method": "POST", "href": "/v1/leases/lease_s003_0001/release", "description": "Release lease."}
  }
}
```

Heartbeat response envelope:

```json
{
  "ok": true,
  "data": "<lease shown above>",
  "links": {},
  "meta": {"request_id": "req_s003_lease_heartbeat", "schema_version": "v1"}
}
```

## Release Lease

Endpoint: `POST /v1/leases/{lease_id}/release`.

Required caller context:

```http
Authorization: Bearer token_s003_runner
Idempotency-Key: idem_s003_lease_release
```

The credential must resolve to a worker or component subject authorized for
`lease.release` on the holder and lease context. C06 must not trust `holder_id`
alone.

Request:

```json
{
  "holder_id": "job_s003_0001",
  "reason": "job completed"
}
```

Release response status: `200`.

Release is idempotent by idempotency key and lease holder:

- Same idempotency key, same `holder_id`, and same `reason` returns the
  original release response with `meta.request_id:
  req_s003_lease_release_replay`.
- Same idempotency key with different request content returns
  `idempotency_conflict`.
- Releasing an already released lease with a new idempotency key returns the
  released lease state when the holder matches.

Release response data:

```json
{
  "lease_id": "lease_s003_0001",
  "resource_id": "res_gpu_0",
  "holder_id": "job_s003_0001",
  "expires_at": "2026-06-05T20:00:46Z",
  "released_at": "2026-06-05T20:00:46Z",
  "released_by": "sub_runner_s003",
  "release_reason": "job completed",
  "links": {}
}
```

C06 records a C06-owned audit event for successful release:

```json
{
  "event_type": "lease.released",
  "lease_id": "lease_s003_0001",
  "holder_id": "job_s003_0001",
  "actor_subject_id": "sub_runner_s003",
  "occurred_at": "2026-06-05T20:00:46Z"
}
```

Release response envelope:

```json
{
  "ok": true,
  "data": "<released lease shown above>",
  "links": {},
  "meta": {"request_id": "req_s003_lease_release", "schema_version": "v1"}
}
```

Release replay response envelope:

HTTP status: `200`.

This is returned for the same idempotency key, same `holder_id`, and same
`reason` as the original release. C06 does not emit a second
`lease.released` audit event.

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
  "meta": {"request_id": "req_s003_lease_release_replay", "schema_version": "v1"}
}
```

Provider-failure release uses a distinct idempotency key and reason:

```http
Idempotency-Key: idem_s003_lease_release_provider_failure
```

Request:

```json
{
  "holder_id": "job_s003_0001",
  "reason": "provider failed"
}
```

Provider-failure release response envelope:

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

Release replay does not emit a duplicate `lease.released` audit event. The
original audit event remains the only release audit event for that idempotency
record.

If release arrives after expiration, C06 returns `lease_expired`; the caller may
continue failure handling because the resource is already no longer held by
that lease.

Expired release request:

```http
Idempotency-Key: idem_s003_lease_release_expired
```

```json
{
  "holder_id": "job_s003_0001",
  "reason": "lease expired"
}
```

## Pending Response

Pending request body, sent while `lease_s003_0001` is active:

```json
{
  "requester_id": "job_s003_0002",
  "resource_selector": "gpu",
  "priority": 0,
  "heartbeat_timeout_seconds": 60
}
```

Pending response status: `201`.

```json
{
  "request_id": "lease_req_s003_0002",
  "state": "pending",
  "resource_selector": "gpu",
  "queue_position": 1,
  "lease": null,
  "created_at": "2026-06-05T20:00:03Z",
  "updated_at": "2026-06-05T20:00:03Z",
  "links": {
    "status": {"method": "GET", "href": "/v1/lease-requests/lease_req_s003_0002", "description": "Read lease request."},
    "cancel": {"method": "POST", "href": "/v1/lease-requests/lease_req_s003_0002/cancel", "description": "Cancel request."}
  }
}
```

Pending response envelope:

```json
{
  "ok": true,
  "data": {
    "request_id": "lease_req_s003_0002",
    "state": "pending",
    "resource_selector": "gpu",
    "queue_position": 1,
    "lease": null,
    "created_at": "2026-06-05T20:00:03Z",
    "updated_at": "2026-06-05T20:00:03Z",
    "links": {
      "status": {"method": "GET", "href": "/v1/lease-requests/lease_req_s003_0002", "description": "Read lease request."},
      "cancel": {"method": "POST", "href": "/v1/lease-requests/lease_req_s003_0002/cancel", "description": "Cancel request."}
    }
  },
  "links": {},
  "meta": {"request_id": "req_s003_lease_pending", "schema_version": "v1"}
}
```

Queue ordering is FIFO within the same `priority`. Higher numeric `priority` sorts ahead of lower priority.

On release or expiration, C06 may grant the next pending request synchronously
before returning or asynchronously on the next allocator tick. In either case,
`GET /v1/lease-requests/{request_id}` is the source of truth for the pending
request's current state. S003 fixtures use the allocator-tick model.

The `status` and `cancel` links above are part of the S003 contract:

- `GET /v1/lease-requests/{request_id}` returns the same lease request shape.
- `POST /v1/lease-requests/{request_id}/cancel` is idempotent and returns the
  canceled request shape.

Canceled request response data:

```json
{
  "request_id": "lease_req_s003_0002",
  "state": "canceled",
  "resource_selector": "gpu",
  "queue_position": null,
  "lease": null,
  "created_at": "2026-06-05T20:00:03Z",
  "updated_at": "2026-06-05T20:00:10Z",
  "links": {}
}
```

Canceled request response status: `200`.

Canceled request response envelope:

```json
{
  "ok": true,
  "data": {
    "request_id": "lease_req_s003_0002",
    "state": "canceled",
    "resource_selector": "gpu",
    "queue_position": null,
    "lease": null,
    "created_at": "2026-06-05T20:00:03Z",
    "updated_at": "2026-06-05T20:00:10Z",
    "links": {}
  },
  "links": {},
  "meta": {"request_id": "req_s003_lease_pending_cancel", "schema_version": "v1"}
}
```

Runner cadence:

- While a lease is active, the runner heartbeats every 30 seconds.
- The runner releases the lease after success, failure, or cancellation when a
  lease was granted.
- If the runner cannot release because the lease already expired, it records
  the C06 `lease_expired` response and continues job failure handling.

## Failure Envelopes

- Unknown selector: HTTP `409`, `code: resource_unavailable`, `retryable: true`.
- Known selector with no leasing capacity: HTTP `503`,
  `code: resource_unavailable`, `retryable: true`.
- Unknown lease: HTTP `404`, `code: not_found`, `retryable: false`.
- Holder mismatch: HTTP `403`, `code: forbidden`, `retryable: false`.
- Expired lease heartbeat or release: HTTP `409`, `code: lease_expired`,
  `retryable: false`.
- Invalid timeout: HTTP `400`, `code: validation_failed`, `retryable: false`.
- Idempotency key reused with different release content: HTTP `409`, `code:
  idempotency_conflict`, `retryable: false`.

Generic error envelope shape:

```json
{
  "ok": false,
  "error": {
    "code": "lease_expired",
    "message": "lease has expired",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_lease_error_generic", "schema_version": "v1"}
}
```

Holder mismatch example:

Request:

```http
POST /v1/leases/lease_s003_0001/heartbeat
Authorization: Bearer token_s003_runner
```

```json
{
  "holder_id": "job_s003_other"
}
```

Precondition: `lease_s003_0001` is active, held by `job_s003_0001`, and not
expired.

```json
{
  "ok": false,
  "error": {
    "code": "forbidden",
    "message": "holder mismatch",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_lease_holder_mismatch", "schema_version": "v1"}
}
```

Expired heartbeat example:

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

Expired release example:

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
