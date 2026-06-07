# Local State: C04 Agent Tool Gateway

- Owns only per-request auth context and public response shaping.
- Owns public invocation idempotency records: key, subject, request
  fingerprint, created `job_id`, expiry, and replay status.
- Does not own durable job lifecycle, catalog records, policy decisions, lease
  state, runtime state, provider state, artifact metadata, artifact bytes, or
  storage state.
- Public caller credential: `Bearer token_s003_agent`.
- Component credential for C05 calls: `Bearer token_s003_gateway`.
- Component credential for C07 public-content proxy calls:
  `Bearer token_s003_gateway`.
- Public subject after auth: `sub_agent_s003`.
- Scenario job id returned by C05: `job_s003_0001`.
- Queued cancel idempotency record is public gateway state; C05 still owns the
  actual job state transition.
