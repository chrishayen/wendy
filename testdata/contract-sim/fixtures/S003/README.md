# S003 Fixtures

This folder indexes accepted request and response fixtures from the successful
S003 contract role-play transcript.

Latest source run: `runs/S003/run-024.md`.

Fixture readiness: achieved for the declared S003 scope. Run 024 is a full
bounded fixture-readiness rerun with exact actor paths, public-only actor helper
context, and local JSON fixture packages. It proved public actor, C03, C04,
C05, C06, C07, C08, C09, C10, and the composition runner for bounded S003.

Supporting trace: `runs/S003/README.md`.
Scenario clock and checkpoint source: `workspaces/S003/scenario-clock.md`.
Machine-readable extraction manifest: `manifest.json`.

The manifest is an extraction/test index only. Do not provide it to bounded
role-play actors unless a future run explicitly lists it as allowed scenario
context.

## Accepted Fixture Groups

- Public discovery request and response.
- Public tool detail request and response.
- Public invocation request and async response.
- Public queued cancellation request and response.
- Job creation request.
- Runner claim response.
- Lease request and response.
- Runtime health/start request and response.
- Provider invocation request and response.
- Artifact upload session request and response.
- Artifact completion request and response.
- Public job status response.
- Public log first page, final page, and cursor semantics.
- Public artifact list and content retrieval responses.
- C04 idempotency replay and conflict responses.
- C05 create-job idempotency replay and conflict responses.
- C05 job policy-context response.
- C05 agent-safe projection response for C04.
- C05 queued-cancel response for C04.
- C05 queued-cancel branch checkpoint
  `checkpoint_after_public_invoke_before_claim`.
- C05 worker log append and component-facing log read responses.
- C05 provider-failure and lease-expiration fail-job responses.
- C05 running-state claim heartbeat for lease-expiration failure branch.
- C05 lease-expiration append-log response.
- C05 explicit HTTP status map and append-log precondition fixtures.
- C07 artifact policy-context response.
- C08 allow, missing-context deny, and policy-denied decisions.
- C08 state-specific job-cancel policy fixtures.
- C08 unknown-token, unknown-action, and unknown-resource decisions.
- C10 runner-only provider content retrieval response.
- C10 backend-unavailable, timeout, unauthorized, forbidden, and dry-run
  responses.
- C10 missing-context and content-unauthorized responses.
- C10 provider failure checkpoints and undefined provider-run route envelope.
- C06 release auth/idempotency and audit checkpoint response.
- C06 provider-failure release response and expired heartbeat/release
  responses.
- C06 pending and pending-cancel envelopes.
- C06 provider-timeout heartbeat sequence and timeout release response.
- C06 holder-mismatch request and full release-replay envelope.
- C07 byte upload required headers, completed upload-session state, and digest
  conversion examples.
- C07 missing-header validation envelope.
- C07 first-write Digest and content-length validation envelopes.
- C07 duplicate-complete-without-matching-key idempotency conflict.
- C09 already-running start without an idempotency key.
- C09 bounded local endpoint-auth facts for node actions.

## Body Fixtures

The canonical byte body for S003 is already present in bounded actor packages.
Provider/runner-facing packages use `provider_png_s003_0001.base64`. The public
actor package uses `artifact_png_s003_0001.base64` so public fixtures do not
expose provider-local naming. Code-level fixtures should use that body with:

- Size: `68`.
- Metadata checksum:
  `sha256:4b5c5c92cec3b23e6a294fc0eea43234ef5126c5a64f4c6c531ac8430ab0b844`.
- HTTP Digest header:
  `sha-256=S1xcks7Dsj5qKU/A7qQyNO9RJsWmT0xsUxrIQwqwuEQ=`.

## Rule

Do not add invented fixtures. Fixtures should come from an accepted transcript
or from a requirement patch that explicitly defines the canonical example.

Fixture-ready means the replay source is machine-readable enough for code
tests. Markdown contract excerpts can define canonical behavior, but accepted
fixtures should eventually be promoted into standalone JSON request/response
files, plus binary body fixtures where relevant.
