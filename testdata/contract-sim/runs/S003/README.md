# S003 Run Log And Improvement Trace

Scenario: `S003-async-gpu-remote-node-image`

Latest result: [run-024.md](run-024.md) is the current full bounded
fixture-readiness rerun. It is fixture-ready for the declared S003 scope.

## Run Index

| Run | Status | Main Finding | Improvements Produced |
| --- | --- | --- | --- |
| [Run 001](run-001.md) | blocked | Flat role packages lacked local contract context; gateway and runner could not produce canonical responses. | Created implementation-mirrored workspace, bounded actor folders, OpenAPI excerpts, and agent-safe vs worker-visible job projection split. |
| [Run 002](run-002.md) | blocked | Workspace structure worked, but actors still needed local fixtures, state, and provider byte handoff. | Added local fixture/state/run-note packages, gateway and runner dependency fixtures, and runner-facing provider `content_ref` handoff. |
| [Run 003](run-003.md) | blocked | Runner still blocked at provider bytes into C07 upload; contracts needed concrete size/checksum and artifact upload steps. | Added concrete execution plans, C07 upload/content/complete fixtures, C10 binary content details, C04 logs/content behavior, C08 owner context rules, C09 lifecycle examples, and fixed C06 Markdown. |
| [Run 004](run-004.md) | pass-with-findings | Happy path completed, but security/lifecycle precision gaps remained. | Added explicit observe/read policy checks, C10 runner auth, C06 heartbeat/release rules, local byte fixtures, C07 idempotency/error rules, C05 claim lifecycle rules, redacted public logs, and C03/C09 clarity. |
| [Run 005](run-005.md) | pass-with-findings | C04 still needed a stateless owner-context strategy and clear idempotency ownership. | Added C05/C07 minimal policy-context projections, C04 read/content flow, C04-owned invocation idempotency records, exact bearer grammar, and fixture index updates. |
| [Run 006](run-006.md) | pass-with-findings | Artifact-list authorization was ambiguous before artifact IDs were known. | Added `job_artifacts:{producer_ref}` collection authorization pattern and generic policy-denied fixture. |
| [Run 007](run-007.md) | pass | Final C04/C08 focused run passed. | Accepted S003 role-play transcript as the current fixture source. |
| [Run 008](run-008.md) | blocked-with-approved-fixes | Full rerun blocked when runner omitted required C07 upload idempotency header. | Validator approved runner-visible upload idempotency keys, C07 missing-idempotency error, provider URL base/path normalization, C10 invoke auth, envelope precision, and public schema projection fixes. |
| [Run 009](run-009.md) | pass-with-findings | Focused rerun from artifact upload through public retrieval passed after Run 008 fixes. | Verified C07 artifact ownership, C05 opaque artifact refs, C06 release, C04/C08 read authorization, and public binary retrieval; recorded remaining fixture precision findings. |
| [Run 010](run-010.md) | scenario-pass-with-findings | Full scenario rerun passed after acceptance-level process update. | Verified discovery, invocation, job claim, lease, runtime start, provider invoke, C07 upload, C05 completion, lease release, public status, artifact list, and binary content retrieval; recorded remaining fixture/process findings. |
| [Run 011](run-011.md) | scenario-pass-with-findings | Full bounded rerun passed after post-Run 010 fixture fixes. | Verified all S003 actors with scenario clock/checkpoints; C03/C04/C07/C08 passed cleanly; recorded remaining fixture precision findings for envelopes, C05, C06, C09, C10, and runner failure paths. |
| [Run 012](run-012.md) | scenario-pass-with-findings | Full bounded rerun passed after post-Run 011 fixture fixes. | Verified C04, C07, C08, C09, and C10 cleanly; recorded remaining precision findings for exact paths, C05 envelopes/logs, C06 negative envelopes, and runner failure branches. |
| [Run 013](run-013.md) | scenario-pass-with-findings | Full bounded rerun passed with exact actor paths after post-Run 012 fixes. | Verified all actors stayed bounded and validated the C05/C06 independent lifetime branch; recorded remaining fixture precision findings for public links/cursors, negative envelopes, pending/cancel, and timestamps. |
| [Run 014](run-014.md) | scenario-pass-with-findings | Full bounded rerun passed after post-Run 013 replay fixes. | C03, agent-user, C09, and runner passed cleanly; C04/C05/C06/C07/C08/C10 recorded remaining replay-extraction precision findings. |
| [Run 015](run-015.md) | scenario-pass-with-findings | Full bounded rerun passed after post-Run 014 replay fixes. | Verified state-specific cancel, missing-header, status-map, replay-envelope, and router precision fixes; recorded remaining fixture-extraction findings and provider-failure timing mismatch. |
| [Run 016](run-016.md) | scenario-pass-with-findings | Full bounded rerun passed after post-Run 015 fixture-extraction fixes. | Verified local JSON fixture packages, bounded actor context, provider-failure timing alignment, and release-response-only runner scope; recorded remaining fixture coverage, replay precondition, and provider-timeout liveness findings. |
| [Run 017](run-017.md) | fixture-readiness-rerun-with-findings | Full bounded fixture-readiness rerun after post-Run 016 fixes did not reach fixture-ready. | Agent-user and C03 are fixture-ready; C04/C05/C06/C07/C08/C09/C10/runner need canonical fixture alignment and machine-readable replay coverage. |
| [Run 018](run-018.md) | fixture-readiness-rerun-with-findings | Full bounded fixture-readiness rerun after post-Run 017 fixes did not reach fixture-ready. | Agent-user and C03 remain fixture-ready; C04/C05/C06/C07/C08/C09/C10/runner need edge-case, replay-chain, and prose/JSON fixture precision. |
| [Run 019](run-019.md) | fixture-readiness-rerun-with-findings | Full bounded fixture-readiness rerun after post-Run 018 fixes did not reach fixture-ready. | C03, C06, C07, and runner are clean for their bounded scopes; public actor context, C04, C05, C08, C09, and C10 still need context-hygiene and fixture/contract alignment fixes. |
| [Run 020](run-020.md) | fixture-readiness-rerun-with-findings | Full bounded fixture-readiness rerun after post-Run 019 fixes did not reach fixture-ready. | C03 and C07 are fixture-ready for bounded scopes; public actor, C04, C05, C06, C08, C09, C10, and runner need public fixture hygiene, replay precision, or prose/JSON alignment. |
| [Run 021](run-021.md) | fixture-readiness-rerun-with-findings | Full bounded fixture-readiness rerun after post-Run 020 fixes did not reach fixture-ready. | C03, C06, and C10 are fixture-ready for bounded scopes; public actor, C04, C05, C07, C08, C09, and runner need context packaging, fixture coverage, or prose/JSON alignment. |
| [Run 022](run-022.md) | fixture-readiness-rerun-with-findings | Full bounded fixture-readiness rerun after post-Run 021 fixes did not reach fixture-ready. | Public actor, C03, C06, C08, C10, and runner are fixture-ready for bounded scopes; C04, C05, C07, and C09 need local projection, replay, auth-fact, or prose/JSON alignment fixes. |
| [Run 023](run-023.md) | fixture-readiness-rerun-with-findings | Full bounded fixture-readiness rerun after post-Run 022 fixes did not reach fixture-ready. | Public actor, C03, C04, C05, C06, C07, C08, C10, and runner are fixture-ready; C09 has one local `service_start_accepted` status-code regression. |
| [Run 024](run-024.md) | fixture-ready-pass | Full bounded fixture-readiness rerun after post-Run 023 fixes passed. | All bounded S003 actor packages are fixture-ready for the declared scope; code-level fixture extraction can begin. |

## Post-Run 010 Fix Batch

After Run 010, a read-only constraint validator returned
`approved-with-notes` for a fixture-readiness patch batch. The batch preserved
component isolation and approved bounded fixes for:

- Canonical C04 dependency endpoint names.
- C03-owned schema projection into C04 `Tool` records.
- C05 component-facing create-job idempotency and worker-validated log append.
- Scenario-level clock/checkpoint context.
- C06 authenticated/idempotent release with audit timing.
- C07 byte upload headers, digest conversion, and completed upload-session
  state.
- C08 unknown-token/action/resource behavior and S003 secrets scope.
- C09 auth and start idempotency.
- C10 invoke context, dry-run, timeout, backend-unavailable, and auth failures.
- Coordinator context hygiene and fixture index updates.

This was an improvement batch, not an accepted fixture-ready run. Run 010
remained scenario-pass evidence until a later bounded rerun proved the updated
packages.

## Post-Run 011 Fix Batch

After Run 011, a read-only constraint validator returned
`approved-with-notes` for a second fixture-precision patch batch. The batch
preserved component isolation and approved bounded fixes for:

- Optional `warnings` and public link-location rules.
- Explicit gateway component credential `Bearer token_s003_gateway` for
  C04-to-C05 component calls.
- C05 concrete create replay, non-transition heartbeat, component log read,
  append-log, and fail-job fixtures.
- C06 optional heartbeat checkpoint, request ids, and release replay audit
  dedupe.
- C09 deterministic start and replay behavior.
- C10 missing-context and content-unauthorized envelopes.
- Runner provider-failure and lease-expiration path fixtures.

This was an improvement batch, not accepted fixture-ready evidence. Run 011
remained scenario-pass-with-findings until a later bounded rerun proved the
updated packages.

## Post-Run 012 Fix Batch

After Run 012, a read-only constraint validator first returned
`needs-design-decision` for a timing conflict between C05 job claim expiry and
C06 resource lease expiry. The coordinator proposed keeping the lifetimes
independent and using a C05 non-transition heartbeat to keep the job claim
writable while intentionally allowing the C06 lease to expire. A separate
read-only arbitrator and the original validator both returned
`approved-with-notes`.

The approved batch preserves component isolation and fixes:

- Exact scenario and scenario-level context paths in actor prompts.
- C05 full success envelopes for heartbeat, log append/read, completion, and
  failure examples.
- C05 second running-log append fixture.
- C05 running-state claim heartbeat at `2026-06-05T20:00:32Z`, extending the
  C05 claim to `2026-06-05T20:01:32Z`.
- C06 holder-mismatch, expired-heartbeat, and expired-release envelopes with
  distinct request IDs.
- Runner provider-failure C10 error response, C06 failure-release response, and
  C05 fail-job sequence.
- Runner lease-expiration branch from `checkpoint_after_lease`, with no C06
  heartbeat, C06 expiry at `2026-06-05T20:01:02Z`, C06 expired responses at
  `2026-06-05T20:01:03Z`, and C05 log/fail at
  `2026-06-05T20:01:04Z`.

This is an improvement batch, not accepted fixture-ready evidence. Run 012
remains scenario-pass-with-findings until a later bounded rerun with exact
paths proves the updated packages.

## Post-Run 016 Fix Batch

After Run 016, a read-only constraint validator returned
`approved-with-notes` for a fixture-completion patch batch. The batch preserves
component isolation and fixes:

- Public actor replay preconditions for logs, artifact list, and artifact
  content.
- C03 repeated-query and intersection fixture cases.
- C04 local JSON fixtures for public gateway responses and addressed
  dependency-contract fakes.
- C05 lifecycle, idempotency, liveness, failure, projection, and error
  fixtures.
- C06 request status, negative lease cases, provider-timeout liveness, timeout
  release, and C06-owned audit/dedupe fixtures.
- C07 upload-session, idempotency, checksum, expiry, digest, policy-context,
  and missing-artifact fixtures.
- C08 local JSON policy/auth fixtures with explicit supplied context.
- C09 README/JSON fixture ID alignment and runtime error fixtures.
- C10 standalone failure request bodies and advertised provider error fixtures.
- Runner provider-timeout liveness and cleanup fixtures, with C06 audit kept
  out of runner scope.

This is an improvement batch, not accepted fixture-ready evidence. Run 016
remains scenario-pass-with-findings until a full bounded Run 017 proves the
updated packages.

## Post-Run 017 Fix Batch

After Run 017, a read-only constraint validator returned
`approved-with-notes` for a canonical fixture-alignment batch. The batch
preserves component isolation and fixes:

- C04 public and dependency fixture coverage.
- C05 canonical create, claim, logs, liveness, completion, cancellation, and
  error envelopes.
- C06 unknown selector, pending status, provider-timeout liveness, timeout
  release, and C06-owned audit fixtures.
- C07 upload idempotency, expired upload, missing-idempotency, and missing
  artifact content fixtures.
- C08 credential scopes, unknown-token behavior, resource naming, collection
  authorization, and missing explicit-context cases.
- C09 invalid lifecycle envelope.
- C10 health, dry-run, missing-context, auth, and content error fixture drift.
- Runner happy path, provider failure, lease expiration, upload, completion,
  timeout cleanup, and deterministic heartbeat event-list fixtures.

This is an improvement batch, not accepted fixture-ready evidence. Run 017
remains fixture-readiness-rerun-with-findings until a full bounded Run 018
proves the updated packages.

## Run 018 Result

Run 018 tested the post-Run 017 canonical fixture-alignment batch. It did not
reach fixture-ready acceptance.

The run preserved the main component boundaries and proved that `agent-user`
and C03 are fixture-ready for their bounded scopes. C04, C05, C06, C07, C08,
C09, C10, and the runner still have fixture precision gaps. The remaining work
is not a redesign of the system shape; it is local replay data, canonical
field alignment, explicit edge-case fixtures, and complete runner
request/response chains.

Process finding: several actors used broad path discovery that exposed sibling
path names, and C06 read one extra workspace README. Future bounded prompts
should provide exact allowed paths and prohibit broad path discovery. C06 must
be rerun cleanly before it can provide fixture-ready evidence.

## Post-Run 018 Fix Batch

After Run 018, a read-only constraint validator returned
`approved-with-notes` for a fixture-precision patch batch. The batch preserves
component isolation and approves:

- Local fixture and contract-excerpt precision fixes only.
- Endpoint-addressed runner dependency request/response pairs, with no sibling
  private storage or internal lifecycle state.
- C06-owned audit replay/dedupe fixtures kept out of runner expectations.
- C08 policy fixtures that use explicit supplied context only.
- C04 public log projection cleanup that removes provider internals unless
  public contract prose explicitly allows them.
- Process guidance that bounded actor reruns must use exact allowed paths and
  avoid broad filesystem discovery.

This is an improvement batch, not accepted fixture-ready evidence. Run 018
remains fixture-readiness-rerun-with-findings until a full bounded Run 019
proves the updated packages.

## Run 019 Result

Run 019 tested the post-Run 018 fixture-precision batch with stricter
exact-path actor prompts and no broad filesystem discovery. It did not reach
fixture-ready acceptance.

The stricter prompt improved evidence quality, and the main component
boundaries held. C03 remains fixture-ready. C06, C07, and the runner returned
clean bounded passes for their S003 scopes. The remaining blockers are narrower:
public actor context packaging, C04 dependency/error fixture precision, C05
canonical envelope drift, C08 worker/action policy fixtures, C09 lifecycle
ambiguity, and C10 provider fixture drift.

Process finding: public actors need public-only scenario slices. Giving the
full internal scenario file to `agent-user` leaks orchestration context and
prevents clean fixture-ready evidence even if the public API fixtures are
otherwise correct.

At initial Run 019 recording time, no post-Run 019 patch had been applied.
The proposed fix batch requires read-only constraint validation before editing
requirements, role packages, or fixture packages.

## Post-Run 019 Fix Batch

After Run 019, a read-only constraint validator returned
`approved-with-notes` for a context-hygiene and fixture-alignment batch. The
batch preserves component isolation and fixes:

- Public-only scenario context for `agent-user`.
- Public actor wording that avoids internal C04 mediation details.
- C04 create-job dependency request body, discovery policy context, and
  advertised public error fixtures.
- C05 canonical error messages, request IDs, cursor metadata placement,
  append-log error bodies, and worker-visible claim edge fixtures.
- C08 worker/action policy fixtures and precise missing-owner context fixture.
- C09 stop replay and invalid lifecycle fixture alignment with the local
  contract.
- C10 invalid width, expired content ref, and provider-invoke forbidden
  fixture alignment with the local contract.
- V1 scope rule that keeps the project focused on basic generic host
  primitives and extension points instead of predefining every future service.
- Process rule that each completed revolution is described with jj, moves the
  `master` bookmark, and pushes after checks pass.

This is an improvement batch, not accepted fixture-ready evidence. Run 019
remains fixture-readiness-rerun-with-findings until a full bounded Run 020
proves the updated packages.

## Run 020 Result

Run 020 tested the post-Run 019 context-hygiene and fixture-alignment batch.
It did not reach fixture-ready acceptance.

The run confirmed that public scenario slicing works better than giving the
external actor the full internal scenario, but public actor fixtures still use
internal checkpoint names and worker/provider-flavored log messages. C03 and
C07 returned clean bounded fixture-ready passes. The rest of the findings are
local replay precision and contract/JSON alignment issues across C04, C05, C06,
C08, C09, C10, and the runner.

At initial Run 020 recording time, no post-Run 020 patch had been applied. The
proposed fix batch requires read-only constraint validation before editing
requirements, role packages, or fixture packages.

## Post-Run 020 Fix Batch

After Run 020, a read-only constraint validator returned
`approved-with-notes` for a replay-precision and fixture-alignment batch. The
batch preserves component isolation and fixes:

- Public actor branch fixtures, public-safe log wording, and public byte
  fixture naming.
- C04 running projection, artifact/content not-found, invalid-input dependency
  context, and error contract drift.
- C05 deterministic event-list replay, terminal request IDs,
  invalid-transition semantics, forbidden request ID drift, and completion
  request fields.
- C06 audit field shape, release replay dedupe assertion placement, bounded
  runner-token subject projection, and timeout liveness replay metadata.
- C08 gateway action policy fixtures for auth, policy, catalog, and route
  reads.
- C09 health and already-stopped stop status alignment.
- C10 timeout context, missing-context text, health timestamp, unauthorized IDs,
  and fixture index drift.
- Runner claim-to-complete happy path, lease-expiration checkpoint alignment,
  and deterministic provider-timeout heartbeat templates.

This is an improvement batch, not accepted fixture-ready evidence. Run 020
remains fixture-readiness-rerun-with-findings until a full bounded Run 021
proves the updated packages.

## Run 021 Result

Run 021 tested the post-Run 020 replay-precision and fixture-alignment batch.
It did not reach fixture-ready acceptance.

The run showed progress: C03 remains fixture-ready, and C06 and C10 now pass
their bounded S003 scopes. The public actor leakage is narrower than before,
but `aliases.md` and two internal checkpoint names still leak non-public
context. C04, C05, C07, C08, C09, and the runner have local contract/fixture
drift or missing fixture coverage.

The proposed post-Run 021 fix batch requires read-only constraint validation
before editing requirements, role packages, or fixture packages.

## Post-Run 021 Fix Batch

After Run 021, a read-only constraint validator returned
`approved-with-notes` for a contract/fixture alignment patch. The batch
preserves component isolation and fixes:

- Public-only alias context and public-observable branch preconditions for the
  public actor.
- C04 public log projection, content-forbidden coverage, idempotency replay
  dependencies, and validation schema source.
- C05 branch preconditions, cancel idempotency fixtures, status-map IDs,
  timeout heartbeat response shape, and complete/fail guard fixtures.
- C06 release replay audit assertion placement.
- C07 upload-session read documentation, size mismatch coverage, and checksum
  mismatch alignment.
- C08 supplied-context rules for existing jobs, pre-job creation, existing
  artifacts, pre-artifact registration, and state-specific job cancel.
- C09 generic runtime status-code alignment.
- Runner checkpoint, C10 envelope prose, and deferred runtime/artifact failure
  branch scope.

This is an improvement batch, not accepted fixture-ready evidence. Run 021
remains fixture-readiness-rerun-with-findings until a full bounded Run 022
proves the updated packages.

## Run 022 Result

Run 022 tested the post-Run 021 contract/fixture alignment batch. It did not
reach fixture-ready acceptance.

The run showed a tighter failure set. Public context hygiene now passes for
`agent-user`, and C03, C06, C08, C10, and the composition runner are
fixture-ready for their bounded S003 scopes. The remaining findings are local
to C04, C05, C07, and C09: C04 needs dependency projection and public link
alignment, C05 needs message/request-id prose alignment, C07 needs upload and
idempotency fixture precision, and C09 needs service-running/status and
bounded auth-fact precision.

The proposed post-Run 022 fix batch requires read-only constraint validation
before editing requirements, role packages, or fixture packages.

## Post-Run 022 Fix Batch

After Run 022, a read-only constraint validator returned
`approved-with-notes` for a local fixture/prose alignment batch. The batch
preserves component isolation and fixes:

- C04 queued-cancel dependency projection, artifact-read deny subject drift,
  and tool-detail link alignment.
- C05 forbidden message drift and generic claim-expired request-id prose.
- C07 upload-session read shape, first-write Digest and content-length
  validation fixtures, duplicate-complete-without-matching-key coverage, and
  operation-specific idempotency conflict documentation.
- C09 service-running status, already-running start without idempotency key,
  and bounded local endpoint-auth facts.

This is an improvement batch, not accepted fixture-ready evidence. Run 022
remains fixture-readiness-rerun-with-findings until a full bounded Run 023
proves the updated packages.

## Run 023 Result

Run 023 tested the post-Run 022 local fixture/prose alignment batch. It did not
reach fixture-ready acceptance.

The run showed that the remaining work is now extremely narrow. Public actor,
C03, C04, C05, C06, C07, C08, C10, and the composition runner all returned
fixture-ready verdicts. C09 found one local JSON/prose mismatch:
`service_start_accepted` should return HTTP `202` for a stopped-service start,
but the machine-readable fixture currently returns `200`.

The proposed post-Run 023 fix batch requires read-only constraint validation
before editing requirements, role packages, or fixture packages.

## Post-Run 023 Fix Batch

After Run 023, a read-only constraint validator returned
`approved-with-notes` for a narrow C09 status-code patch and process guardrail.
The batch preserves component isolation and fixes:

- C09 `service_start_accepted` response status restored to HTTP `202`.
- C09 `service_running` response status kept at HTTP `200`.
- Process guidance requiring fixture-ID-specific assertions after JSON fixture
  edits.

This is an improvement batch, not accepted fixture-ready evidence. Run 023
remains fixture-readiness-rerun-with-findings until a full bounded Run 024
proves the updated packages.

## Run 024 Result

Run 024 tested the post-Run 023 C09 status-code patch and process guardrail.
It reached fixture-ready acceptance for the declared S003 scope.

All bounded actors returned fixture-ready verdicts: public actor, C03, C04,
C05, C06, C07, C08, C09, C10, and the composition runner. No actor needed
sibling private state, broad requirements context, or hidden coordinator
memory. Machine-readable fixture packages and binary fixtures are now accepted
for the covered branches.

Non-blocking notes:

- Public actor error branches are documented but not machine-readable in the
  S003 public actor package. This is acceptable because S003 covers public
  success/cancel branches; future public error scenarios should add public
  error fixtures.
- C09 fixture README lists `service_running` twice. This does not block replay
  and can be cleaned up later.

## Improvement Trace

| Issue Family | Found In | Improvement | Captured In |
| --- | --- | --- | --- |
| Actor context gaps | Runs 001-002 | Local implementation-mirrored actor folders with role, state, fixtures, and contract excerpts. | `workspaces/S003/` |
| Public vs worker projections | Runs 001-003 | Split `AgentJob` from worker-visible `Job` and kept execution plans out of agent responses. | `openapi/public-interface.v1.yaml`, C04/C05 packages |
| Provider artifact handoff | Runs 002-003 | Added runner-only opaque provider `content_ref`, binary retrieval headers, expiry, and C07 upload handoff. | C10 and runner packages |
| Artifact ownership | Runs 003-005 | C07 owns artifact bytes, metadata, list, content, checksums, and public retrieval. | C07 package and OpenAPI |
| Owner-scoped authorization | Runs 004-006 | Added C05/C07 minimal policy-context projections and C08 missing-context denial. | C04/C05/C07/C08 packages |
| Gateway idempotency | Run 005 | C04 owns public invocation idempotency records without owning job lifecycle state. | C04 package |
| Collection authorization | Run 006 | Added `job_artifacts:{producer_ref}` resource pattern for pre-list authorization. | C04/C08 packages |
| Runtime lifecycle | Runs 004-005 | Added stopped -> starting -> running node lifecycle and polling timing. | C09 and runner packages |
| Lease lifecycle | Runs 004-005 | Added heartbeat/release responses, expiration math, cancel shape, and queue grant behavior. | C06 and runner packages |
| Fixture readiness | Runs 004-007 | Added local base64 byte fixtures and identified accepted transcript source. | `fixtures/S003/`, workspace fixture folders |
| Upload idempotency | Run 008 | Added runner-visible C07 operation idempotency keys and C07 missing-idempotency error envelope. | C07 and runner packages |
| Provider URL composition | Run 008 | Normalized provider endpoint as origin/base URL and kept provider paths as absolute API paths. | C03/C09/C10 and runner packages |
| Public schema projection | Run 008 | Projected C03 `input_schema` and `output_schema` into agent-safe C04 tool records. | C04 and agent-user packages |
| Acceptance labeling | Run 010 | Distinguished focused pass, scenario pass, fixture-ready pass, and implementation-ready pass. | `AGENTS.md`, `Learnings.md`, `blog.md` |
| Fixture-ready precision | Post-Run 010 | Added canonical dependency endpoints, scenario clock/checkpoints, worker auth/idempotency rules, digest conversion, upload terminal state, and negative-path envelopes. | S003 workspace packages and `fixtures/S003/README.md` |
| Fixture replay precision | Post-Run 011 | Added gateway component auth, concrete replay/heartbeat/failure fixtures, C06 heartbeat/release replay precision, C09 start replay, and C10 missing-context/content auth errors. | S003 workspace packages and `fixtures/S003/README.md` |
| Fixture branch precision | Post-Run 012 | Added exact actor context paths, C05/C06 independent lifetime rule, full C05 envelopes, distinct C06 expired-operation envelopes, and addressed runner failure-branch fixtures. | `AGENTS.md`, S003 workspace packages, `scenario-clock.md` |
| Fixture replay extraction blockers | Run 013 | Found remaining exact-envelope, advertised-link, cursor, pending/cancel, auth-negative, and timestamp precision gaps after boundaries held. | `runs/S003/run-013.md` |
| Post-Run 013 replay precision | Post-Run 013 | Added state-specific queued cancel, tool detail/list envelopes, cursor rules, final logs, mediated artifact content, C05/C06/C07/C08/C09/C10 negative fixtures, and timestamp corrections. | S003 workspace packages, L02, OpenAPI |
| Remaining replay precision | Run 014 | Found last-mile fixture extraction gaps around C04 detail/clamp/error fixtures, C05 statuses/checkpoints/preconditions, C06 replay/mismatch fixtures, C07 missing-header envelope, C08 cancel state context, and C10 branch/router envelopes. | `runs/S003/run-014.md` |
| Post-Run 014 replay precision | Post-Run 014 | Validator-approved patch added exact status maps, branch checkpoints, preconditions, replay envelopes, missing-header and router errors, and explicit cancel policy state context. | S003 workspace packages, `runs/S003/run-014.md` |
| Fixture extraction boundary | Run 015 | Confirmed scenario behavior passes, but fixture-ready now requires standalone machine-readable request/response fixtures, provider-failure timing alignment, and a few branch-specific request/precondition fixtures. | `runs/S003/run-015.md` |
| Post-Run 015 fixture extraction | Post-Run 015 | Validator-approved patch added local JSON fixture sets, fixture manifest, C03 query semantics, C05 terminal/timeout/final-log fixtures, provider-failure timing alignment, C09 stop/replay precision, C10 route paths, and removed runner dependence on C06 audit internals. | S003 workspace packages, `fixtures/S003/manifest.json`, `runs/S003/run-015.md` |
| Fixture coverage and liveness | Run 016 | Confirmed local JSON fixtures are useful but not complete enough for fixture-ready acceptance; long-running provider timeout needs explicit C05/C06 liveness and release behavior. | `runs/S003/run-016.md` |
| Post-Run 016 fixture completion | Post-Run 016 | Validator-approved patch added fixture coverage alignment, C04/C08/runner JSON fixture sets, timeout liveness heartbeats, C06 timeout release, and provider failure request-body precision. | S003 workspace packages, `fixtures/S003/manifest.json`, `runs/S003/run-016.md` |
| Canonical fixture drift | Run 017 | Found remaining prose/JSON drift across C04-C10 and runner fixture coverage after boundaries held. | `runs/S003/run-017.md` |
| Post-Run 017 canonical alignment | Post-Run 017 | Validator-approved patch aligned fixture JSON with local prose contracts and expanded runner/C04 replay coverage. | S003 workspace packages, `runs/S003/run-017.md` |
| Edge-fixture and replay-chain precision | Run 018 | Found that parseable JSON and broad fixture coverage still are not fixture-ready when edge cases, cursor semantics, auth grammar, replay preconditions, or runner request/response chains remain incomplete. | `runs/S003/run-018.md` |
| Post-Run 018 fixture precision | Post-Run 018 | Validator-approved patch added edge-case fixtures, normalized public projections and messages, made C06 event-list replay explicit, and expanded runner request/response chains. | S003 workspace packages, `runs/S003/run-018.md` |
| Context hygiene and final fixture drift | Run 019 | Found public actor scenario leakage and remaining local C04/C05/C08/C09/C10 fixture drift after exact-path bounded prompts. | `runs/S003/run-019.md` |
| Post-Run 019 context and fixture alignment | Post-Run 019 | Validator-approved patch added public-only actor scenario context, aligned local fixture packages to their contracts, and recorded v1 scope plus jj persistence rules. | S003 workspace packages, `runs/S003/run-019.md`, `AGENTS.md` |
| Public fixture hygiene and replay precision | Run 020 | Found internal public preconditions/logs, C06 audit assertion gaps, C05/C10 event and contract drift, C09 health/stop ambiguity, C08 gateway fixture gaps, and runner checkpoint/liveness materialization gaps. | `runs/S003/run-020.md` |
| Public helper hygiene and local drift | Run 021 | Found that public actors need public-only helper files and that fixture-ready still requires proving or explicitly deferring every advertised v1 behavior. | `runs/S003/run-021.md` |
| Local projection and auth-fact precision | Run 022 | Found that remaining blockers are now mostly local response projection, upload/idempotency coverage, exact messages/request IDs, and bounded auth/action facts for components that enforce local auth. | `runs/S003/run-022.md` |
| Fixture-ID-specific regression checks | Run 023 | Found that broad status-code checks can miss wrong-fixture edits; post-patch verification should assert the exact fixture IDs changed by a finding. | `runs/S003/run-023.md` |
| Fixture-ready acceptance | Run 024 | Full bounded rerun passed across all S003 actors with accepted machine-readable fixtures for the declared scope. | `runs/S003/run-024.md`, `fixtures/S003/manifest.json` |

## Current Acceptance

S003 currently has a full-scenario pass for requirements-level contract
role-play:

- Actors used bounded local packages and exact scenario-level context paths
  rather than sibling folders or full requirements context.
- No actor required sibling folders or full requirements context.
- The agent-facing flow completed without internal route, lease, runtime,
  provider, or storage knowledge.
- The runner completed the distributed workflow and stored only the final C07
  artifact ID in C05.
- C04 authorizes stateless reads through minimal policy context and C08 checks.
- C08 does not inspect C05/C07 internals.
- C07 remains the durable artifact owner.

S003 is fixture-ready for the declared scope as of Run 024:

- All actors used bounded local packages and exact scenario-level context paths
  rather than sibling folders or full requirements context.
- No actor required sibling folders or full requirements context.
- Machine-readable fixture packages exist for the covered public, component,
  provider, and runner branches.
- The runner completed the distributed workflow and stored only the final C07
  artifact ID in C05.
- C04 authorizes stateless reads through minimal policy context and C08 checks.
- C08 does not inspect C05/C07 internals.
- C07 remains the durable artifact owner.

S003 is not yet implementation-ready. The next step is to extract the accepted
fixtures into code-level contract tests and prepare build work items.

## Next Fixture Work

When implementation begins, extract replay fixtures from Run 024 plus the
accepted workspace examples:

- Public discovery and invocation.
- Agent job status, logs, artifact list, and content retrieval.
- C04 idempotency replay and conflict.
- C05 job policy context.
- C07 artifact policy context and artifact upload flow.
- C08 allow, missing-context deny, and policy-denied decisions.
- C10 runner-only provider content retrieval.
- C06 lease grant, heartbeat, release, cancel, and expiry.

Remaining fixture precision work before code-level test extraction:

- None blocking for the declared S003 scope.
- Future public error scenarios should add machine-readable public error
  fixtures.
- A future documentation tidy pass may remove the duplicate C09
  `service_running` README bullet.
