# S003 Public Aliases

Use these aliases only for the external public actor. They intentionally name
public API resources, not internal components, providers, runners, leases,
claims, upload sessions, or workspace folders.

| Alias | Public Meaning |
| --- | --- |
| `tool-list` | `GET /v1/tools` |
| `image-tool` | `cap_image_generate_gpu` |
| `tool-invoke` | `POST /v1/tools/cap_image_generate_gpu/invoke` |
| `public-job` | `job_s003_0001` through `/v1/agent/jobs/job_s003_0001` |
| `public-logs` | `/v1/agent/jobs/job_s003_0001/logs` |
| `public-artifacts` | `/v1/agent/jobs/job_s003_0001/artifacts` |
| `public-artifact` | `art_s003_0001` through public artifact responses |
| `public-artifact-content` | `/v1/artifacts/art_s003_0001/content` |
