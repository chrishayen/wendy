# C04 Fixtures

Fixture candidates:

- Public tool discovery happy path.
- Public tool detail happy path.
- Public invocation happy path returning `202`.
- Public queued cancellation happy path.
- C08 auth success and policy allow responses.
- C08 list-level and per-capability tool discovery policy responses.
- C08 job cancel allow response.
- C03 catalog list and route responses.
- C05 create-job response.
- C05 create-job request body and response used by public invocation.
- C05 component-facing `agent-projection` response that C04 projects into
  public `/v1/agent/...` responses.
- Agent-safe job status response.
- Agent-safe running and succeeded job status responses.
- Public log first page and final page responses.
  Public log fields are agent-safe and do not expose provider internals.
  C04 maps component-facing log messages to public-safe progress messages.
- Public artifact list and content links.
- C04-mediated public artifact content proxy response using
  `provider_png_s003_0001`.
- Public artifact content not-found response.
- Public artifact content forbidden response and C08 artifact-read deny fake.
- Denied policy response.
- Invalid input, missing owner context, owner not found, C08 missing-context,
  and context-build failure responses.
- Public actor context hygiene check: C04 may return only public status,
  artifact list, and content links to `agent-user`, never internal completion
  state.
- Machine-readable fixture set: `s003-fixtures.json`.
- Full addressed dependency examples remain in `dependency-responses.md`.
