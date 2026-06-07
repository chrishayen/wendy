# C05 Fixtures

Fixture candidates:

- Create job request/response.
- Create job idempotency replay response.
- Create job idempotency conflict response.
- Gateway component credential fixture for C04-facing endpoints.
- Claim job request/response.
- Mark running request/response.
- Heartbeat request/response.
- Non-transition heartbeat response with `status_message=waiting for gpu lease`.
- Running-state non-transition heartbeat at `2026-06-05T20:00:32Z`
  extending the C05 claim to `2026-06-05T20:01:32Z`.
- Provider-timeout deterministic heartbeat event list with 30-second cadence, last
  heartbeat at `2026-06-05T20:14:37Z`, and claim expiry at
  `2026-06-05T20:15:37Z`.
  The event list is accepted as a machine-checkable replay fixture when it uses
  `schema_version: event-list.v1`, exact request/response templates, ordered
  events, request ids, timestamps, expiry values, and a documented state
  transition.
- Append log request/response.
- Second append-log fixture for `running provider invocation`.
- Append log worker mismatch error.
- Append log expired claim error.
- Component-facing log read request/response for C04.
- Component-facing final log page response.
- Component-facing final log page request/precondition.
- Component-facing job policy-context envelope.
- Component-facing succeeded job policy-context envelope.
- Component-facing queued cancel request/response.
- Queued cancel idempotency replay and idempotency conflict responses.
- Queued-cancel branch checkpoint:
  `workspaces/S003/scenario-clock.md#checkpoint_after_public_invoke_before_claim`.
- Complete job request/response.
- Fail job request/response.
- Complete/fail worker-mismatch, expired-claim, and terminal guard fixtures.
- Provider failure append-log and fail-job fixtures.
- Provider timeout append-log and fail-job fixtures.
- Lease expiration append-log and fail-job fixtures at
  `2026-06-05T20:01:04Z`.
- Component-facing agent-safe job projection for C04.
- Queued, running, and canceled agent-safe job projections for C04.
- Provider-failure and lease-expiration agent-safe job projections for C04.
- Provider-timeout agent-safe job projection for C04.
- Invalid transition error.
- Worker/gateway auth failure envelopes.
- Gateway forbidden and job not-found envelopes.
- Claim same-worker replay, different-worker conflict, expired-claim reclaim,
  and terminal-claim conflict.
- Running cancel conflict and terminal completion conflict.
- Append-log worker mismatch and expired-claim precondition fixtures and
  envelopes.
- Component-facing log cursor semantics for first-page and final-page tokens.
- Explicit HTTP status map for all named S003 C05 envelopes.
- Machine-readable fixture set: `s003-fixtures.json`.
