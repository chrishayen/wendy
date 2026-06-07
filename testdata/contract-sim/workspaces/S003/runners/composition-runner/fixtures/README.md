# Composition Runner Fixtures

Named addressed dependency fixtures for S003:

- `c05_claim_ok`: claim response with full execution plan.
- `c05_heartbeat_running_ok`: heartbeat response after explicit
  `transition_to=running`.
- `c05_heartbeat_claim_renewed`: running-state non-transition heartbeat used
  to keep the C05 claim active during the lease-expiration branch.
- `c06_lease_grant_ok`: GPU lease grant.
- `c06_lease_release_ok`: idempotent lease release response.
- `c06_lease_release_provider_failure`: release response with
  `reason=provider failed`.
- `c05_provider_timeout_liveness_heartbeats`: deterministic C05 heartbeat
  event list during the 900-second provider wait.
- `c06_provider_timeout_liveness_heartbeats`: deterministic C06 heartbeat
  event list during the 900-second provider wait.
- `c06_lease_release_provider_timeout`: release response with
  `reason=provider timed out`.
- `c06_lease_heartbeat_expired`: expired heartbeat response.
- `c06_lease_release_expired`: expired release response.
  - Request uses `Idempotency-Key: idem_s003_lease_release_expired` and
    `reason=lease expired`.
- C06 release audit events are C06-owned and are not runner dependency
  fixtures. Runner S003 fixtures assert the release response only.
- `c09_node_health_ok`: node health response.
- `c09_service_stopped`: service status before on-demand start.
- `c09_service_start_accepted`: start response in `starting` state.
- `c09_service_running_ok`: service status after start or already running.
- `c10_invoke_success`: invoke response with `content_refs`.
- `c10_invoke_provider_unavailable`: provider-unavailable failure envelope.
- `c10_invoke_dry_run`: dry-run response when requested.
- `c10_invoke_timeout`: timeout failure envelope.
- `c10_content_ok`: provider content retrieval headers.
- `c07_upload_create_ok`: C07 upload session.
- `c07_upload_content_ok`: accepted uploaded bytes.
- `c07_upload_complete_ok`: final artifact metadata.
- `c05_complete_ok`: final job completion response.
- `c05_log_provider_failure`: append-log response for provider failure.
- `c05_fail_provider_unavailable`: fail-job response for provider failure.
- `c05_log_provider_timeout`: append-log response for provider timeout.
- `c05_fail_provider_timeout`: fail-job response for provider timeout.
- `c05_log_lease_expired`: append-log response for lease expiration.
- `c05_fail_lease_expired`: fail-job response for lease expiration.
- `provider_png_s003_0001`: local 68-byte PNG fixture, SHA-256
  `4b5c5c92cec3b23e6a294fc0eea43234ef5126c5a64f4c6c531ac8430ab0b844`.
  Stored as `provider_png_s003_0001.base64`.
- `provider_failure_path`: provider failure sequence fixture.
- `provider_timeout_path`: provider timeout sequence fixture with deterministic
  `event-list.v1` liveness events, C10 timeout invoke, release, log, and fail
  steps.
- `lease_expiration_path`: lease expiration sequence fixture with concrete
  request/response steps. It starts from `checkpoint_after_lease`; the first
  step renews only the C05 claim before the C06 lease expiry is observed.
- `happy_path`: happy-path orchestration sequence fixture with concrete
  claim-to-complete request/response steps and C07 upload subpath reference.
- `c07_upload_path`: C07 upload sequence fixture with create/content/complete
  request bodies.

S003 fixture-concrete runner failure branches are provider unavailable,
provider timeout, and lease expiration. Runtime-start failure and C07
upload/content/complete failure handling are runner-owned requirements deferred
to a later scenario, not hidden S003 fixture expectations.

Fixture ids are local runner test names, not public wire fields.

`provider_timeout_path.liveness_event_list` is replayable only by applying the
documented templates to the ordered event list. Each event supplies timestamp,
C05/C06 request ids, and the resulting C05 claim and C06 lease expiry values.

Artifact upload fixture idempotency keys:

- Create: `idem_s003_artifact_upload_create`.
- Content: `idem_s003_artifact_upload_content`.
- Complete: `idem_s003_artifact_upload_complete`.
- Lease release: `idem_s003_lease_release`.
- Provider-failure lease release: `idem_s003_lease_release_provider_failure`.
- Provider-timeout lease release: `idem_s003_lease_release_provider_timeout`.
- Node start: `idem_s003_node_start`.

These are replay values for S003. Real runners generate operation-scoped keys
per job/artifact operation.
