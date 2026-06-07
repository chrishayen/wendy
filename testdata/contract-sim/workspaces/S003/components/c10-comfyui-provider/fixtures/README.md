# C10 Fixtures

Named fixtures for S003:

- `provider_health_ok`: healthy provider envelope.
- `provider_invoke_success`: blocking invoke response with `content_refs`.
- `provider_invoke_dry_run`: dry-run response with no content refs.
  - Includes `resource_lease_id: null` coverage.
- `provider_content_ok`: binary success headers for `pcr_s003_0001`.
- `provider_invalid_input`: `validation_failed` envelope.
- `provider_invalid_width_out_of_range`: width validation envelope.
- `provider_invalid_height_not_multiple_of_8`: height multiple validation
  envelope.
- `provider_missing_context`: `validation_failed` envelope for missing
  required invoke context fields.
- `provider_backend_unavailable`: `provider_unavailable` envelope.
  - Replay checkpoint: `provider_backend_down_before_invoke`.
- `provider_timeout`: `provider_timeout` envelope.
  - Replay checkpoint: `provider_generation_exceeds_timeout`.
- `provider_invoke_unauthorized`: `unauthorized` envelope.
- `provider_invoke_forbidden`: `forbidden` envelope for authenticated
  agent-scoped credentials.
- `provider_content_unauthorized`: `unauthorized` envelope for missing,
  malformed, or unknown provider content credentials.
- `provider_content_forbidden`: `forbidden` envelope for agent-scoped content
  retrieval.
- `provider_content_not_found`: `not_found` envelope for unknown content refs.
- `provider_content_unavailable`: `provider_unavailable` envelope for local
  content read failures.
- `provider_content_expired`: expired `content_ref` envelope at
  `2026-06-05T20:15:01Z`.
- `provider_png_s003_0001`: local 68-byte PNG fixture, SHA-256
  `4b5c5c92cec3b23e6a294fc0eea43234ef5126c5a64f4c6c531ac8430ab0b844`.
  Stored as `provider_png_s003_0001.base64`.
- `provider_run_status_route_not_found`: routing-layer `404 not_found`
  expectation for the provider-run status route that S003 does not define.
- `provider_run_cancel_route_not_found`: routing-layer `404 not_found`
  expectation for the provider-run cancel route that S003 does not define.
  - Request id: `req_s003_provider_run_route_not_found`.
  - Exact accidental paths:
    `GET /v1/provider/runs/provider_run_s003_0001` and
    `POST /v1/provider/runs/provider_run_s003_0001/cancel`.

Fixture ids are local test names, not public wire fields.

Machine-readable fixture set: `s003-fixtures.json`.
