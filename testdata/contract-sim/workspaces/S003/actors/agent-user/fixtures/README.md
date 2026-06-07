# Agent User Fixtures

Fixture candidates:

- `GET /v1/tools` with `Authorization: Bearer token_s003_agent`.
- `POST /v1/tools/cap_image_generate_gpu/invoke` with an `input` envelope.
- `GET /v1/agent/jobs/job_s003_0001`.
- `GET /v1/agent/jobs/job_s003_0001/logs`.
- `GET /v1/agent/jobs/job_s003_0001/artifacts`.
- `GET /v1/artifacts/art_s003_0001/content`.
- Machine-readable fixture set: `s003-fixtures.json`.
- Binary body fixture: `artifact_png_s003_0001.base64`.
- `queued_cancel_branch` and `succeeded_artifact_branch` are public-observable
  fixture branches. They must not expose leases, claims, runners, providers, or
  component checkpoints.
- Public actor prompts should use `public-aliases.md`, not the workspace-level
  `aliases.md`, because the workspace alias file names internal packages.
