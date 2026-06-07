# Contract Excerpts: C07 Artifact Store

Unless a response is explicitly documented as a binary-envelope exception,
successful C07 responses use the standard JSON success envelope:

```json
{
  "ok": true,
  "data": "<response data>",
  "links": {},
  "meta": {"request_id": "req_s003_artifact", "schema_version": "v1"}
}
```

## Create Upload Session

Endpoint: `POST /v1/artifact-uploads`.

Required caller context:

```http
Authorization: Bearer token_s003_runner
```

The credential must resolve to a worker or component subject authorized for
`artifact.register`. C07 owns artifact upload state and final artifact records;
the caller owns only the request to register bytes.

Required header:

```http
Idempotency-Key: idem_s003_artifact_upload_create
```

Request:

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

`producer_ref` is the canonical job-artifact correlation field for S003. It
contains the opaque job ID and must not be called `owner_id`. Ownership for
policy checks remains separate as `owner_subject_id`.

Response status: `201`.
S003 fixture request id: `req_s003_artifact_upload_create`.

Response data:

```json
{
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
    "content": {"method": "PUT", "href": "/v1/artifact-uploads/upload_s003_0001/content", "description": "Upload bytes."},
    "complete": {"method": "POST", "href": "/v1/artifact-uploads/upload_s003_0001/complete", "description": "Complete upload."}
  }
}
```

## Upload Content

Endpoint: `PUT /v1/artifact-uploads/{upload_id}/content`.

Required header:

```http
Authorization: Bearer token_s003_runner
Idempotency-Key: idem_s003_artifact_upload_content
Content-Type: image/png
Content-Length: 68
Digest: sha-256=S1xcks7Dsj5qKU/A7qQyNO9RJsWmT0xsUxrIQwqwuEQ=
```

`Content-Type`, `Content-Length`, and `Digest` are required and validated for
content upload. Content type may be `image/png` or `application/octet-stream`;
the upload session's declared `media_type` remains the canonical artifact
media type.

Digest conversion rule:

- Artifact metadata stores SHA-256 checksums as `sha256:<lowercase-hex>`.
- HTTP byte responses and byte uploads use the Digest header form
  `sha-256=<base64-of-digest-bytes>`.
- For S003, metadata checksum
  `sha256:4b5c5c92cec3b23e6a294fc0eea43234ef5126c5a64f4c6c531ac8430ab0b844`
  converts to HTTP header
  `Digest: sha-256=S1xcks7Dsj5qKU/A7qQyNO9RJsWmT0xsUxrIQwqwuEQ=`.

S003 byte fixture:

- Fixture id: `provider_png_s003_0001`.
- Fixture body: a 68-byte PNG used only by local role-play and contract tests.
- SHA-256: `4b5c5c92cec3b23e6a294fc0eea43234ef5126c5a64f4c6c531ac8430ab0b844`.

Response data updates state to `received`.

Response status: `200`.
S003 fixture request id: `req_s003_artifact_upload_content`.

Response data:

```json
{
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
    "complete": {"method": "POST", "href": "/v1/artifact-uploads/upload_s003_0001/complete", "description": "Complete upload."}
  }
}
```

## Complete Upload

Endpoint: `POST /v1/artifact-uploads/{upload_id}/complete`.

Required header:

```http
Authorization: Bearer token_s003_runner
Idempotency-Key: idem_s003_artifact_upload_complete
```

Request:

```json
{
  "checksum": "sha256:4b5c5c92cec3b23e6a294fc0eea43234ef5126c5a64f4c6c531ac8430ab0b844",
  "size": 68
}
```

After completion, C07's upload session transitions to terminal state
`completed`. The upload session remains C07-owned state and is not exposed to
agents.

## Read Upload Session

Endpoint: `GET /v1/artifact-uploads/{upload_id}`.

This is a runner/component-facing C07 diagnostic contract for replay and
operator tests. It returns C07-owned upload-session state only; it does not
expose storage paths, provider refs, or public agent data.

Completed upload-session state:

```json
{
  "upload_id": "upload_s003_0001",
  "state": "completed",
  "artifact_id": "art_s003_0001",
  "received_size": 68,
  "expected_size": 68,
  "expected_checksum": "sha256:4b5c5c92cec3b23e6a294fc0eea43234ef5126c5a64f4c6c531ac8430ab0b844",
  "completed_at": "2026-06-05T20:00:45Z"
}
```

GET upload-session response status: `200`.
S003 upload-session fixture request id:
`req_s003_artifact_upload_session_completed`.

Complete upload response status: `201`.
S003 complete fixture request id: `req_s003_artifact_complete`.

Response data:

```json
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
    "metadata": {"method": "GET", "href": "/v1/artifacts/art_s003_0001", "description": "Read artifact metadata."},
    "content": {"method": "GET", "href": "/v1/artifacts/art_s003_0001/content", "description": "Read artifact content."}
  }
}
```

## Job Artifact Reference

C05 may store only the opaque artifact ID `art_s003_0001` or an equivalent opaque artifact reference. C05 must not store storage paths or bytes.

## Artifact Policy Context

Endpoint: `GET /v1/artifacts/art_s003_0001/policy-context`.

Required caller context:

```http
Authorization: Bearer token_s003_gateway
```

This component-facing projection exists only so C04 can ask C08 for
owner-scoped decisions without C08 reading C07 internals. It returns policy
context only and must not include artifact bytes, storage paths, checksums,
media metadata, provider refs, or runtime details.

Response data:

```json
{
  "resource_kind": "artifact",
  "artifact_id": "art_s003_0001",
  "owner_subject_id": "sub_agent_s003",
  "producer_ref": "job_s003_0001",
  "policy_state": "available"
}
```

S003 fixture request id: `req_s003_artifact_policy_context`.

If the artifact does not exist, C07 returns the standard `not_found` error
envelope.

## Artifact Metadata

Endpoint: `GET /v1/artifacts/art_s003_0001`.

Required caller context:

```http
Authorization: Bearer token_s003_gateway
```

Response data is the artifact object returned by complete upload.
S003 fixture request id: `req_s003_artifact_metadata`.

## Artifact List For Job

Endpoint: `GET /v1/artifacts?producer_ref=job_s003_0001`.

Required caller context:

```http
Authorization: Bearer token_s003_gateway
```

Response data:

```json
{
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
        "content": {"method": "GET", "href": "/v1/artifacts/art_s003_0001/content", "description": "Read artifact content."}
      }
    }
  ],
  "next_cursor": null
}
```

S003 fixture request id: `req_s003_artifact_list_by_producer`.

## Artifact Content

Endpoint: `GET /v1/artifacts/art_s003_0001/content`.

Required caller context:

```http
Authorization: Bearer token_s003_gateway
```

In S003, C04 owns the public artifact content route and calls this C07
component-facing content contract after public auth and policy checks. Agents
do not call C07 directly. This is a binary-envelope exception. Success returns
bytes with these headers:

```http
HTTP/1.1 200 OK
Content-Type: image/png
Content-Length: 68
Digest: sha-256=S1xcks7Dsj5qKU/A7qQyNO9RJsWmT0xsUxrIQwqwuEQ=
```

Errors return the standard error envelope.

## Upload Validation Errors

Artifact upload idempotency keys are scoped to one upload operation:

- `POST /v1/artifact-uploads`: upload session creation.
- `PUT /v1/artifact-uploads/{upload_id}/content`: byte upload for one
  `upload_id`.
- `POST /v1/artifact-uploads/{upload_id}/complete`: finalization for one
  `upload_id`.

The same idempotency key must not be reused across different operation kinds
or different upload IDs.

Replay behavior:

- Same upload operation, same idempotency key, and same request/content
  fingerprint returns HTTP `200` with the current resource state.
- Same upload operation and same idempotency key with different request/content
  fingerprint returns HTTP `409` with `idempotency_conflict`.
- First successful `POST /v1/artifact-uploads/{upload_id}/complete` returns
  HTTP `201`; replay of the same complete operation returns HTTP `200` with
  the existing artifact response.

- Missing required idempotency key: `missing_idempotency_key`.
- Missing required byte headers: `validation_failed`.
- Expired upload: `artifact_expired`.
- Size mismatch: `validation_failed`.
- Checksum mismatch: `validation_failed`.
- Duplicate completed upload with the same idempotency key and same payload:
  return the existing artifact response.
- Duplicate completed upload with the same idempotency key and different
  payload: `idempotency_conflict`.
- Duplicate completed upload without a matching idempotency key means an
  already completed upload receives a complete request with a new or unrecorded
  `Idempotency-Key`; it returns `idempotency_conflict`.

Validation error envelope:

HTTP status: `400`.

```json
{
  "ok": false,
	"error": {
	  "code": "validation_failed",
	  "message": "checksum does not match uploaded content",
	  "retryable": false
	},
	"links": {},
	"meta": {"request_id": "req_s003_artifact_checksum_mismatch", "schema_version": "v1"}
}
```

S003 size mismatch uses the same `validation_failed` envelope with message
`artifact size mismatch` and request id `req_s003_artifact_size_mismatch`.

S003 first-write Digest mismatch uses the same `validation_failed` envelope
with message `Digest does not match uploaded content` and request id
`req_s003_artifact_bad_digest`.

S003 first-write content-length mismatch uses the same `validation_failed`
envelope with message `Content-Length does not match uploaded content` and
request id `req_s003_artifact_content_length_mismatch`.

Missing content-header validation envelope:

HTTP status: `400`.

```json
{
  "ok": false,
  "error": {
    "code": "validation_failed",
    "message": "Content-Type, Content-Length, and Digest headers are required for artifact content upload",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_artifact_missing_headers", "schema_version": "v1"}
}
```

Idempotency conflict envelope shape:

HTTP status: `409`.

```json
{
  "ok": false,
  "error": {
    "code": "idempotency_conflict",
    "message": "idempotency key was reused with different upload content",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_artifact_content_idempotency_conflict", "schema_version": "v1"}
}
```

S003 uses operation-specific idempotency conflict messages and request IDs:

- `artifact_upload_create_idempotency_conflict` uses request id
  `req_s003_artifact_idempotency_conflict` and message
  `idempotency key was reused with different request content`.
- `artifact_upload_content_idempotency_conflict` uses request id
  `req_s003_artifact_content_idempotency_conflict` and message
  `idempotency key was reused with different upload content`.
- `artifact_upload_complete_idempotency_conflict` uses request id
  `req_s003_artifact_complete_idempotency_conflict` and message
  `idempotency key was reused with different upload content`.
- `artifact_upload_complete_duplicate_without_matching_key` uses request id
  `req_s003_artifact_complete_duplicate_without_matching_key` and message
  `upload is already completed with a different idempotency key`.

Missing idempotency key envelope:

HTTP status: `400`.

```json
{
  "ok": false,
  "error": {
    "code": "missing_idempotency_key",
    "message": "Idempotency-Key header is required for artifact upload operations",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_artifact_missing_idempotency", "schema_version": "v1"}
}
```

Artifact expired envelope:

HTTP status: `410`.

```json
{
  "ok": false,
  "error": {
    "code": "artifact_expired",
    "message": "artifact upload session has expired",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_artifact_expired", "schema_version": "v1"}
}
```
