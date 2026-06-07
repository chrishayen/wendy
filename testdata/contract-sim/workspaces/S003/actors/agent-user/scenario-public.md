# S003 Public Scenario Slice

## Audience

This file is for the external `agent-user` actor only. It describes the public
API view of S003 and intentionally omits internal component names, runner
steps, routes, leases, runtime nodes, provider refs, and storage details.

## Public Goal

An external automation agent discovers a GPU-backed image generation tool,
invokes it through the public API, polls the returned job links, reads public
logs and artifact metadata, and downloads the final artifact bytes.

## Public Initial Facts

- The caller has public credential `Bearer token_s003_agent`.
- The public tool catalog may return `cap_image_generate_gpu`.
- The tool runs asynchronously for S003.
- The public invocation request uses idempotency key `idem_s003_0001`.
- The successful invocation returns public job id `job_s003_0001`.
- Public checkpoints are described only by observable public state:
  - After invocation, `job_s003_0001` may be queued and cancelable.
  - During execution, `job_s003_0001` may be running and expose logs.
  - After completion, `job_s003_0001` is succeeded and exposes artifact
    `art_s003_0001`.

## Public Steps

1. Request `GET /v1/tools` with `Authorization: Bearer token_s003_agent`.
2. Select `cap_image_generate_gpu` from the visible public tool list.
3. Invoke `POST /v1/tools/cap_image_generate_gpu/invoke` with:

```json
{
  "input": {
    "prompt": "a clean product photo of a red ceramic mug",
    "width": 1024,
    "height": 1024
  },
  "preferred_mode": "async",
  "dry_run": false
}
```

4. Follow only links returned by public responses.
5. Poll `GET /v1/agent/jobs/job_s003_0001` until it reaches a terminal public
   state.
6. Read logs from returned public log links.
7. List artifacts from returned public artifact links.
8. Download artifact content from returned public content links.

## Public Success Criteria

- All requests use public API paths only.
- Protected public endpoints include the caller credential.
- The actor follows public response links instead of inferring internal paths.
- Public responses do not expose route metadata, leases, runtime details,
  provider-local refs, storage paths, or execution plans.
- Binary artifact content returns PNG bytes with the documented content headers.
