# C08 Fixtures

Fixture candidates:

- Valid agent credential response.
- Valid gateway component credential response.
- Valid runner credential response with `req_s003_auth_runner_verify`.
- Invalid credential response.
- Malformed credential variants for wrong scheme case, empty token, extra
  whitespace, unsupported scheme, and token whitespace.
- Unknown validly formed token response.
- Tool discovery allow with `context.operation: listTools`.
- Per-capability tool discovery allow and deny.
- Tool invoke allow.
- Worker action allows for `job.execute`, `lease.request`, `lease.release`,
  `artifact.register`, `node.read`, `node.service.start`, and
  `provider.invoke`.
- Job read allow for owner.
- Job read missing-owner deny with `job_id` and `requester_id` supplied but no
  `owner_subject_id`.
- Job cancel allow for owner with explicit `job_state: queued`.
- Job cancel deny for owner with explicit `job_state: running`.
- Job cancel missing-`job_state` deny for state-specific policy checks.
- Gateway component auth verify, policy check, catalog read, catalog route
  read, job create, and job read allow fixtures.
- Artifact read allow for owner.
- Artifact read and job-artifacts collection missing-owner denies.
- Artifact registration allow before an artifact id exists, using explicit
  `job_id`, `producer_ref`, and `owner_subject_id` context.
- Policy deny.
- Missing context deny.
- Unknown action deny.
- Unknown resource deny.
- Secrets out-of-scope marker for S003.
- Machine-readable fixture set: `s003-fixtures.json`.
