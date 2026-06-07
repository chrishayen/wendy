# C09 Fixtures

Named fixtures for S003:

- `node_health_ok`: node health response.
  - HTTP status: `200`.
  - S003 `checked_at`: `2026-06-05T20:00:04Z`.
- `service_stopped`: initial service status.
- `service_running`: service status after start.
- `service_start_accepted`: first start response from stopped service,
  returning `202` and `status: starting`.
- `service_running`: current running response after polling.
- `service_start_replay`: same start key and service returns deterministic
  HTTP `200` current running response with `req_s003_node_start_replay` after
  the service is running.
- `service_start_already_running_without_key`: start without an idempotency key
  returns deterministic HTTP `200` current running response when the service is
  already running.
- `service_start_idempotency_conflict`: same key reused for different start
  content.
- `service_stop_accepted`: idempotent stop response.
- `service_stop_already_stopped_replay`: explicit already-stopped stop
  response aligned to the stop contract with HTTP `202`.
- `unknown_service`: HTTP `404`, `not_found` envelope.
- `runtime_unavailable`: HTTP `503`, `provider_unavailable` envelope.
  - Message: `runtime adapter docker is unavailable`.
  - Request id: `req_s003_node_error`.
- `invalid_lifecycle`: HTTP `400`, `validation_failed` envelope for an
  unsupported lifecycle action request.
- `node_unauthorized`: HTTP `401`, `unauthorized` envelope.
- `node_forbidden`: HTTP `403`, `forbidden` envelope.
  - Both include exact messages, retryability, and request IDs in the contract
    excerpts.
- `bounded_auth_facts`: local C09 replay facts for `token_s003_runner` allowed
  node actions and `token_s003_agent` forbidden node actions. These facts do
  not make C09 the owner of global policy rules.
- Machine-readable fixture set: `s003-fixtures.json`.
