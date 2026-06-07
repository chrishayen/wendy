# Contract Excerpts: C08 Access Policy And Secrets

## Verify Credential

Request:

```json
{
  "credential": "Bearer <token>",
  "context": {"surface": "public_api"}
}
```

S003 bearer grammar:

- Scheme is exactly `Bearer`, case-sensitive.
- Scheme and token are separated by one ASCII space.
- Token is non-empty and contains no ASCII whitespace.
- Unknown tokens that match this grammar return `valid: false`.
- Empty tokens, extra whitespace, wrong casing, or unsupported schemes return
  the malformed credential error envelope.

Allowed S003 response envelope:

```json
{
  "ok": true,
  "data": {
    "valid": true,
    "subject_id": "sub_agent_s003",
    "scopes": ["agent"]
  },
  "links": {},
  "meta": {"request_id": "req_s003_auth", "schema_version": "v1"}
}
```

Invalid credentials return `ok: true` with `valid: false` when the credential
is syntactically valid but unknown. Malformed credentials return an
`unauthorized` error envelope.

Concrete credential fixtures:

| Credential | Subject | Scopes |
| --- | --- | --- |
| `Bearer token_s003_agent` | `sub_agent_s003` | `agent` |
| `Bearer token_s003_gateway` | `sub_gateway_s003` | `component` |
| `Bearer token_s003_runner` | `sub_runner_s003` | `worker` |

Invalid credential response:

```json
{
  "ok": true,
  "data": {
    "valid": false,
    "subject_id": null,
    "scopes": []
  },
  "links": {},
  "meta": {"request_id": "req_s003_auth_invalid", "schema_version": "v1"}
}
```

Gateway component credential response:

```json
{
  "ok": true,
  "data": {
    "valid": true,
    "subject_id": "sub_gateway_s003",
    "scopes": ["component"]
  },
  "links": {},
  "meta": {"request_id": "req_s003_auth_gateway", "schema_version": "v1"}
}
```

Runner credential response:

```json
{
  "ok": true,
  "data": {
    "valid": true,
    "subject_id": "sub_runner_s003",
    "scopes": ["worker"]
  },
  "links": {},
  "meta": {"request_id": "req_s003_auth_runner_verify", "schema_version": "v1"}
}
```

Malformed credential response:

```json
{
  "ok": false,
  "error": {
    "code": "unauthorized",
    "message": "credential could not be parsed",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_auth_malformed", "schema_version": "v1"}
}
```

## Policy Check

Request:

```json
{
  "subject_id": "sub_agent_s003",
  "action": "tool.invoke",
  "resource": "cap_image_generate_gpu",
  "context": {}
}
```

Response envelope:

```json
{
  "ok": true,
  "data": {
    "allowed": true,
    "reason": "allowed_by_s003_fixture"
  },
  "links": {},
  "meta": {"request_id": "req_s003_policy", "schema_version": "v1"}
}
```

## S003 Allowed Decisions

For `sub_agent_s003`:

- `tool.discover` on `tools`: allow.
- `tool.discover` on `cap_image_generate_gpu`: allow.
- `tool.invoke` on `cap_image_generate_gpu`: allow.
- `job.read` on `job_s003_0001`: allow if requester or owner is `sub_agent_s003`.
- `job.cancel` on `job_s003_0001`: allow if requester or owner is
  `sub_agent_s003` and supplied `job_state` is `queued`.
- `artifact.read` on artifacts produced by `job_s003_0001`: allow if requester or owner is `sub_agent_s003`.

For worker subject `sub_runner_s003`:

- `job.execute`: allow.
- `lease.request`: allow.
- `lease.release`: allow.
- `artifact.register`: allow.
- `node.read`: allow.
- `node.service.start`: allow for `svc_comfyui_gpu`.
- `provider.invoke`: allow for `cap_image_generate_gpu`.

For gateway component subject `sub_gateway_s003`:

- `auth.verify`: allow.
- `policy.check`: allow.
- `catalog.read`: allow for `cap_image_generate_gpu`.
- `catalog.route.read`: allow for `cap_image_generate_gpu`.
- `job.create`: allow for `cap_image_generate_gpu`.
- `job.read`: allow for component-facing job policy context, logs, and
  agent-safe projection when `owner_subject_id` is supplied for owner-scoped
  reads.

Gateway component policy allow example:

```json
{
  "ok": true,
  "data": {
    "allowed": true,
    "reason": "allowed_by_s003_fixture"
  },
  "links": {},
  "meta": {"request_id": "req_s003_policy_gateway_component", "schema_version": "v1"}
}
```

Required policy context fields:

- For actions on existing jobs: `job_id`, `requester_id`,
  `owner_subject_id` when known, and `job_state` when the action is
  state-specific, such as `job.cancel`.
- For pre-job creation policy, such as `job.create`, the known context is the
  capability or tool being invoked before a job id exists.
- For actions on existing artifacts: `artifact_id`, `producer_ref`, `job_id`,
  and `owner_subject_id` when known.
- For pre-artifact registration, such as `artifact.register`, the artifact id
  does not exist yet. The required context is `job_id`, `producer_ref`, and
  `owner_subject_id`.
- For service actions: `service_id` and `node_id` when known.
- For provider actions: `capability_id`, `service_id`, and `job_id` when known.
- For lease actions: `lease_id` or `request_id` when known, `holder_id`, and
  `resource_selector` when known.

For owner-scoped job and artifact actions, `owner_subject_id` is mandatory. If
the caller omits required owner context, C08 denies with `reason:
missing_context`; it does not infer ownership from C05 or C07 internals.
For state-specific job actions, C08 uses the supplied `job_state` context and
does not infer job state from C05 internals.

S003 observe/read policy fixtures:

- `job.read` on `job_s003_0001` with `owner_subject_id: sub_agent_s003`:
  allow.
- `job.read` on `job_s003_0001` without `owner_subject_id`: deny with
  `missing_context`.
- `job.cancel` on `job_s003_0001` with `owner_subject_id: sub_agent_s003` and
  `job_state: queued`: allow.
- `job.cancel` on `job_s003_0001` with `owner_subject_id: sub_agent_s003` and
  `job_state: running`: deny with `policy_denied`.
- `job.cancel` on `job_s003_0001` without `job_state`: deny with
  `missing_context`.
- `artifact.read` on `art_s003_0001` with `producer_ref: job_s003_0001` and
  `owner_subject_id: sub_agent_s003`: allow.
- `artifact.read` on `job_artifacts:job_s003_0001` with `producer_ref:
  job_s003_0001` and `owner_subject_id: sub_agent_s003`: allow.
- `artifact.read` on `art_s003_0001` without `owner_subject_id`: deny with
  `missing_context`.

Queued job-cancel allow fixture:

```json
{
  "subject_id": "sub_agent_s003",
  "action": "job.cancel",
  "resource": "job_s003_0001",
  "context": {
    "job_id": "job_s003_0001",
    "requester_id": "sub_agent_s003",
    "owner_subject_id": "sub_agent_s003",
    "job_state": "queued"
  }
}
```

```json
{
  "ok": true,
  "data": {
    "allowed": true,
    "reason": "allowed_by_s003_fixture"
  },
  "links": {},
  "meta": {"request_id": "req_s003_policy_job_cancel_queued", "schema_version": "v1"}
}
```

Running job-cancel deny fixture:

```json
{
  "subject_id": "sub_agent_s003",
  "action": "job.cancel",
  "resource": "job_s003_0001",
  "context": {
    "job_id": "job_s003_0001",
    "requester_id": "sub_agent_s003",
    "owner_subject_id": "sub_agent_s003",
    "job_state": "running"
  }
}
```

```json
{
  "ok": true,
  "data": {
    "allowed": false,
    "reason": "policy_denied"
  },
  "links": {},
  "meta": {"request_id": "req_s003_policy_job_cancel_running_denied", "schema_version": "v1"}
}
```

Collection resource pattern:

- `job_artifacts:{producer_ref}` represents the artifact collection produced by
  an opaque producer such as a job.
- It is used when the caller must authorize listing before individual artifact
  IDs are known.
- C08 still requires `owner_subject_id`; it does not infer ownership from the
  resource string.

Worker lease fixtures:

- `lease.request` for `sub_runner_s003` with `holder_id: job_s003_0001` and
  `resource_selector: gpu`: allow.
- `lease.release` for `sub_runner_s003` with `lease_id: lease_s003_0001` and
  `holder_id: job_s003_0001`: allow.

Discovery authorization behavior for S003 is per candidate:

1. C04 checks `tool.discover` on `tools` before asking C03 for candidates.
2. C04 checks `tool.discover` on each returned capability ID.
3. C04 projects only allowed capabilities into the public tool list.

## Deny Shape

Denied decisions return a success envelope containing an explicit denial:

```json
{
  "ok": true,
  "data": {
    "allowed": false,
    "reason": "policy_denied"
  },
  "links": {},
  "meta": {"request_id": "req_s003_policy_denied", "schema_version": "v1"}
}
```

Missing context example:

```json
{
  "ok": true,
  "data": {
    "allowed": false,
    "reason": "missing_context"
  },
  "links": {},
  "meta": {"request_id": "req_s003_policy_missing_context", "schema_version": "v1"}
}
```

Unknown action example:

```json
{
  "ok": true,
  "data": {
    "allowed": false,
    "reason": "unknown_action"
  },
  "links": {},
  "meta": {"request_id": "req_s003_policy_unknown_action", "schema_version": "v1"}
}
```

Unknown resource example:

```json
{
  "ok": true,
  "data": {
    "allowed": false,
    "reason": "unknown_resource"
  },
  "links": {},
  "meta": {"request_id": "req_s003_policy_unknown_resource", "schema_version": "v1"}
}
```

If C08 receives an unknown but syntactically valid bearer token through
`POST /v1/auth/verify`, it returns `ok: true` with `valid: false`; callers
must not proceed to policy checks for that subject.

## Secrets In S003

Secrets resolution is out of scope for S003. No S003 actor may depend on a
secret lookup fixture, hidden credential material, or provider secret read.
Future scenarios that exercise secrets must add explicit C08 secret contracts
and bounded fixtures before simulation.
