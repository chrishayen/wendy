# C06 Fixtures

Fixture candidates:

- Lease granted response.
- Lease pending response.
- Lease pending request body and full envelope with
  `req_s003_lease_pending`.
- Lease heartbeat response.
- Lease heartbeat checkpoint at `2026-06-05T20:00:32Z`.
- Lease release response.
- Provider-failure lease release response with
  `Idempotency-Key: idem_s003_lease_release_provider_failure`.
- Provider-timeout deterministic heartbeat event list and lease release response with
  `Idempotency-Key: idem_s003_lease_release_provider_timeout`.
  The event-list template plus events is the standalone replay shape for the
  29 timeout heartbeats.
- Lease release replay response for the same idempotency key, including full
  envelope `req_s003_lease_release_replay`.
- Lease release idempotency conflict response.
- Lease release audit event.
- Normal successful release audit event.
- Release replay audit dedupe behavior.
- Provider-failure release audit event.
- Audit events use `actor_subject_id`; lease response data uses
  `released_by`.
- `Bearer token_s003_runner` resolves to `sub_runner_s003` in bounded S003 C06
  fixture context.
- Lease canceled request response.
- Lease pending cancel envelope with `req_s003_lease_pending_cancel`.
- Lease pending cancel replay envelope.
- Machine-readable JSON fixtures for grant, pending, heartbeat, release, cancel, and errors.
- Unknown selector error: HTTP `409`, `resource_unavailable`, retryable.
- No-capacity resource unavailable error: HTTP `503`, `resource_unavailable`,
  retryable.
- Holder mismatch request fixture and error.
- Expired lease heartbeat error.
- Expired lease release error.
- Expired release request body with
  `Idempotency-Key: idem_s003_lease_release_expired`.
- Lease-expiration branch where no C06 heartbeat is sent after
  `checkpoint_after_lease`.
- Provider-timeout branch where C06 is heartbeated every 30 seconds until
  `2026-06-05T20:14:37Z`, extending the lease to
  `2026-06-05T20:15:37Z`.
- Scenario clock/checkpoint alignment with `workspaces/S003/scenario-clock.md`.
- Machine-readable fixture set: `s003-fixtures.json`.
