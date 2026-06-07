# C07 Fixtures

Named fixtures for S003:

- `artifact_upload_create_ok`: create upload response for
  `producer_ref=job_s003_0001`.
- `artifact_upload_create_idempotency_replay`: replay response for the same
  create idempotency key and equivalent body.
- `artifact_upload_create_idempotency_conflict`: conflict response for the
  same create idempotency key and different body.
- `artifact_upload_content_ok`: upload content accepted response.
- `artifact_upload_content_idempotency_replay`: replay response for the same
  content idempotency key and equivalent byte fingerprint.
- `artifact_upload_content_idempotency_conflict`: conflict response for the
  same content idempotency key and different byte fingerprint.
- `artifact_upload_content_missing_headers`: `validation_failed` envelope when
  `Content-Type`, `Content-Length`, or `Digest` is missing.
  - Request id: `req_s003_artifact_missing_headers`.
- `artifact_upload_content_bad_digest`: first-write `validation_failed`
  envelope when `Digest` does not match the uploaded bytes.
- `artifact_upload_content_length_mismatch`: first-write `validation_failed`
  envelope when `Content-Length` does not match the uploaded bytes.
- `artifact_upload_content_missing_idempotency`: `missing_idempotency_key`
  envelope for content upload.
- `artifact_upload_complete_ok`: complete upload response creating
  `art_s003_0001`.
- `artifact_upload_complete_idempotency_replay`: replay response for the same
  complete idempotency key and equivalent body.
- `artifact_upload_complete_idempotency_conflict`: conflict response for the
  same complete idempotency key and different body.
- `artifact_upload_complete_duplicate_without_matching_key`: conflict response
  when an already completed upload receives a complete request with a new or
  unrecorded idempotency key.
- `artifact_upload_complete_missing_idempotency`: `missing_idempotency_key`
  envelope for upload completion.
- `artifact_upload_session_completed`: C07-owned terminal upload-session state.
- `artifact_upload_missing_idempotency`: `missing_idempotency_key` envelope for
  upload operations that omit a required `Idempotency-Key` header.
- `artifact_metadata_ok`: metadata response for `art_s003_0001`.
- `artifact_policy_context_ok`: policy-context response for `art_s003_0001`.
- `artifact_policy_context_missing`: `not_found` policy-context response.
- `artifact_metadata_missing`: `not_found` metadata response.
- `artifact_content_missing`: `not_found` content response.
- `artifact_list_empty`: empty artifact list response for an authorized
  producer with no artifacts.
- `artifact_list_by_producer_ok`: artifact list response for
  `producer_ref=job_s003_0001`.
- `artifact_content_ok`: binary success headers for `art_s003_0001`.
  - Component-facing read uses `Authorization: Bearer token_s003_gateway`.
    Public agent reads are mediated by C04.
- `artifact_expired_upload`: `artifact_expired` envelope.
  - HTTP status: `410`.
  - Request id: `req_s003_artifact_expired`.
  - Includes an explicit expired, uncompleted upload checkpoint.
- `artifact_checksum_mismatch`: `validation_failed` envelope.
- `artifact_size_mismatch`: `validation_failed` envelope.
- `artifact_upload_session_completed`: C07-owned upload-session read contract.
  It exposes upload-session state only to runner/component callers.
- `provider_png_s003_0001`: local 68-byte PNG fixture, SHA-256
  `4b5c5c92cec3b23e6a294fc0eea43234ef5126c5a64f4c6c531ac8430ab0b844`.
  Stored as `provider_png_s003_0001.base64`.
- `artifact_digest_conversion`: metadata checksum
  `sha256:4b5c5c92cec3b23e6a294fc0eea43234ef5126c5a64f4c6c531ac8430ab0b844`
  maps to HTTP Digest
  `sha-256=S1xcks7Dsj5qKU/A7qQyNO9RJsWmT0xsUxrIQwqwuEQ=`.

Fixture ids are local test names, not public wire fields.

Artifact upload fixture idempotency keys:

- Create: `idem_s003_artifact_upload_create`.
- Content: `idem_s003_artifact_upload_content`.
- Complete: `idem_s003_artifact_upload_complete`.

Machine-readable fixture set: `s003-fixtures.json`.
