# S003 Scenario Clock And Checkpoints

This file is read-only scenario context. It may be given to any S003 actor
alongside that actor's local folder and the scenario file. It is not owned by
any component and must not be treated as shared implementation state.

## Canonical Clock

| Event | Timestamp |
| --- | --- |
| Scenario start | `2026-06-05T20:00:00Z` |
| Public invocation accepted | `2026-06-05T20:00:00Z` |
| C05 job created | `2026-06-05T20:00:00Z` |
| Queued-cancel branch C05 job canceled | `2026-06-05T20:00:01Z` |
| C05 job claimed | `2026-06-05T20:00:01Z` |
| C06 lease requested and granted | `2026-06-05T20:00:02Z` |
| C05 job marked running | `2026-06-05T20:00:03Z` |
| C09 node health checked | `2026-06-05T20:00:04Z` |
| C09 service start accepted | `2026-06-05T20:00:04Z` |
| C09 service running | `2026-06-05T20:00:06Z` |
| C10 provider invocation accepted | `2026-06-05T20:00:07Z` |
| Provider backend-down branch observed before invoke | `2026-06-05T20:00:07Z` |
| Provider backend-down branch diagnostic log, lease release, and fail | `2026-06-05T20:00:08Z` |
| Optional happy-path C06 lease heartbeat | `2026-06-05T20:00:32Z` |
| Lease-expiration branch C05 claim heartbeat | `2026-06-05T20:00:32Z` |
| C10 provider content ready | `2026-06-05T20:00:44Z` |
| C07 artifact created | `2026-06-05T20:00:45Z` |
| C05 job completed | `2026-06-05T20:00:46Z` |
| C06 lease released | `2026-06-05T20:00:46Z` |
| Lease-expiration branch C06 expired heartbeat/release observed | `2026-06-05T20:01:03Z` |
| Lease-expiration branch C05 diagnostic log and fail | `2026-06-05T20:01:04Z` |
| Provider-timeout branch observed | `2026-06-05T20:15:07Z` |
| Provider-timeout branch first C05/C06 liveness heartbeat | `2026-06-05T20:00:37Z` |
| Provider-timeout branch last C05/C06 liveness heartbeat | `2026-06-05T20:14:37Z` |
| Provider-timeout branch C05 claim and C06 lease expire after last heartbeat | `2026-06-05T20:15:37Z` |
| Provider-timeout branch diagnostic log, lease release, and fail | `2026-06-05T20:15:08Z` |

## Replay Checkpoints

Checkpoint fixtures let a rerun start from the middle of a flow without using
coordinator memory.

### `checkpoint_after_job_create`

This checkpoint is for worker/runner replay after C04 has created the C05 job
and before any worker claim exists. It is not public actor context.

- Job: `job_s003_0001`
- Job state: `queued`
- Active worker: none.
- Public invocation accepted: `2026-06-05T20:00:00Z`
- C05 job created: `2026-06-05T20:00:00Z`
- C05 claim: none.
- C06 lease, C09 runtime start, C10 provider invocation, and C07 artifact: none.

### `checkpoint_after_public_invoke_before_claim`

This checkpoint is used only for the queued-cancel branch. It branches before
`checkpoint_after_claim`.

- Job: `job_s003_0001`
- Job state: `queued`
- Active worker: none.
- Public invocation accepted: `2026-06-05T20:00:00Z`
- C05 job created: `2026-06-05T20:00:00Z`
- Queued-cancel request received: `2026-06-05T20:00:01Z`
- C05 queued-cancel transition time: `2026-06-05T20:00:01Z`
- No C05 claim, C06 lease, C09 runtime start, C10 provider invocation, or C07
  artifact exists in this branch.

### `checkpoint_after_claim`

- Job: `job_s003_0001`
- Job state: `claimed`
- Active worker: `runner_s003_0001`
- Claim expires: `2026-06-05T20:01:01Z`
- Execution plan: available only through C05's worker-visible claim response.

### `checkpoint_after_lease`

- Job: `job_s003_0001`
- Job state: `running`
- Lease: `lease_s003_0001`
- Lease holder: `job_s003_0001`
- Lease expires: `2026-06-05T20:01:02Z`
- Runtime service: `svc_comfyui_gpu` may still be `stopped` or `starting`.

### `checkpoint_after_lease_heartbeat`

- Lease: `lease_s003_0001`
- Lease holder: `job_s003_0001`
- Heartbeat received: `2026-06-05T20:00:32Z`
- Lease expires: `2026-06-05T20:01:32Z`

This checkpoint is for the happy path or any path that intentionally keeps the
C06 resource lease alive. It must not be used as the starting point for the
S003 lease-expiration failure branch.

### `checkpoint_after_running_claim_heartbeat_for_lease_expiry`

This checkpoint branches from `checkpoint_after_lease` before any C06 lease
heartbeat is sent.

- Job: `job_s003_0001`
- Job state: `running`
- Active worker: `runner_s003_0001`
- C05 claim heartbeat received: `2026-06-05T20:00:32Z`
- C05 claim expires: `2026-06-05T20:01:32Z`
- Lease: `lease_s003_0001`
- Lease holder: `job_s003_0001`
- C06 lease expires: `2026-06-05T20:01:02Z`
- C06 lease heartbeat after `checkpoint_after_lease`: not sent.

This is a deliberate failure-path fixture: the runner kept its C05 job claim
alive but failed to keep the C06 resource lease alive.

### `checkpoint_after_lease_expired_no_heartbeat`

This checkpoint follows
`checkpoint_after_running_claim_heartbeat_for_lease_expiry`.

- Job: `job_s003_0001`
- Job state before failure call: `running`
- Active worker: `runner_s003_0001`
- C05 claim expires: `2026-06-05T20:01:32Z`
- Lease: `lease_s003_0001`
- Lease holder: `job_s003_0001`
- C06 lease expired at: `2026-06-05T20:01:02Z`
- C06 expired heartbeat/release observed at: `2026-06-05T20:01:03Z`
- C05 append-log and fail-job calls happen at: `2026-06-05T20:01:04Z`

### `checkpoint_after_provider_content`

- Provider content ref: `pcr_s003_0001`
- Content fixture: `provider_png_s003_0001`
- Content size: `68`
- Metadata checksum: `sha256:4b5c5c92cec3b23e6a294fc0eea43234ef5126c5a64f4c6c531ac8430ab0b844`
- HTTP digest header: `sha-256=S1xcks7Dsj5qKU/A7qQyNO9RJsWmT0xsUxrIQwqwuEQ=`

### `checkpoint_after_artifact`

- Upload session: `upload_s003_0001`
- Upload session state: `completed`
- Artifact: `art_s003_0001`
- Artifact owner subject: `sub_agent_s003`
- Producer ref: `job_s003_0001`
- Artifact created: `2026-06-05T20:00:45Z`
- Job state before completion call: `running`

### `checkpoint_after_completion`

- Job: `job_s003_0001`
- Job state: `succeeded`
- Job updated: `2026-06-05T20:00:46Z`
- Artifact refs: `["art_s003_0001"]`
- Lease: `lease_s003_0001`
- Lease state: `released`
- Lease released: `2026-06-05T20:00:46Z`

### `provider_backend_down_before_invoke`

This checkpoint branches after `checkpoint_after_lease`, after C09 has reported
`svc_comfyui_gpu` running, and before provider generation starts.

- Job: `job_s003_0001`
- Job state: `running`
- Active worker: `runner_s003_0001`
- Lease: `lease_s003_0001`
- Lease holder: `job_s003_0001`
- Runtime service: `svc_comfyui_gpu` is `running`.
- C10 provider adapter is reachable at `http://node_linux_gpu:8188`.
- C10 ComfyUI backend health fails before generation.
- Provider failure is observed at: `2026-06-05T20:00:07Z`
- The runner observes this as a C10 provider invoke error, then performs C05
  diagnostic log, C06 release, and C05 fail-job cleanup at:
  `2026-06-05T20:00:08Z`.
- No provider content ref, C07 upload session, or C07 artifact exists in this
  branch.

### `provider_generation_exceeds_timeout`

This checkpoint branches after C10 accepts the provider invocation and before
content is produced.

- Job: `job_s003_0001`
- Job state: `running`
- Active worker: `runner_s003_0001`
- Lease: `lease_s003_0001`
- Lease holder: `job_s003_0001`
- C10 provider invocation accepted: `2026-06-05T20:00:07Z`
- Invocation timeout seconds: `900`
- C05 and C06 liveness heartbeat cadence: every `30` seconds while waiting
  for provider completion.
- First C05/C06 liveness heartbeat: `2026-06-05T20:00:37Z`
- Last C05/C06 liveness heartbeat before timeout:
  `2026-06-05T20:14:37Z`
- C05 claim expires after last heartbeat: `2026-06-05T20:15:37Z`
- C06 lease expires after last heartbeat: `2026-06-05T20:15:37Z`
- C10 timeout observed: `2026-06-05T20:15:07Z`
- The runner performs C05 diagnostic log, C06 release, and C05 fail-job
  cleanup at:
  `2026-06-05T20:15:08Z`.
- C06 timeout release idempotency key:
  `idem_s003_lease_release_provider_timeout`
- C06 timeout release reason: `provider timed out`
- No provider content ref, C07 upload session, or C07 artifact exists in this
  branch.
