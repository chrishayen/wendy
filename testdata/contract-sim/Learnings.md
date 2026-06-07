# Contract Simulation Learnings

These lessons come from the S003 role-play loop for the Pluggable Agent
Control Plane. Keep this file project-level: it should help future scenarios
avoid relearning the same boundary and contract lessons.

## Process Lessons

- Mirror the future implementation layout in the simulation workspace. Actor
  folders under `components/`, `runners/`, and `actors/` exposed structural
  gaps earlier than flat role files.
- A blocked actor is useful evidence. The runner and gateway blocking in early
  runs identified missing public contracts instead of hiding them behind
  coordinator guesses.
- Every actor folder needs local role, state, contract excerpts, fixtures, and
  run-note expectations. A role should not need sibling folders to participate.
- Validate proposed fixes before patching requirements. The read-only
  constraint gate prevented fixes that would have solved the run by weakening
  component isolation.
- Keep run logs append-only. Add new run logs and improvement traces rather
  than rewriting older logs to look cleaner.
- Treat fixture precision as a product of the loop. Start with prose examples,
  then promote accepted examples to replayable fixtures after the scenario
  passes.
- Do not let a focused pass stand in for a full scenario pass. Run 007 passed
  the C04/C08 artifact-list fix, but a later full rerun still found C07 upload
  idempotency and provider URL composition gaps.
- Every run log should state its scope and acceptance level. A run can be a
  focused pass, full scenario pass, fixture-ready pass, or
  implementation-ready pass; the project should not call it accepted beyond
  what was actually exercised.
- After a focused fix, rerun the full scenario before updating the project-level
  accepted run. Focused reruns are useful evidence, not final scenario
  acceptance.
- Focused reruns need precise checkpoint fixtures. Addressed state can unblock
  a rerun, but fixture-ready testing should have explicit checkpoint state for
  active jobs, claims, leases, uploaded bytes, and owned artifacts.
- A scenario pass is an intermediate result. Continue into fixture precision
  until the transcript can be replayed without hidden coordinator memory or the
  remaining work is explicitly deferred.
- Public actors should receive only public responses, public links, and their
  own request history. Internal completion state can be represented by public
  polling, not by coordinator summaries of component internals.
- When an actor needs many fields from a dependency response, pass the full
  addressed public/component response. Lossy coordinator summaries hide
  missing fixture fields.
- Component-to-component calls need explicit credentials in the actor package.
  Relying on "internal trust" is too vague for fixture-ready tests.
- Failure paths should have fixtures before implementation handoff, even if
  the happy path is the active scenario. Otherwise runners can pass the main
  flow while failure handling remains prose-only.
- Actor prompts need exact scenario and scenario-level context file paths. A
  vague "use the S003 scenario" prompt can still produce useful findings, but
  it is not fixture-ready evidence if an actor cannot locate the file.
- Independent lifetimes need independent fixtures. In S003, a C05 job claim
  heartbeat keeps the job writable, but it does not refresh a C06 resource
  lease; the lease-expiration failure branch has to prove both timelines
  explicitly.
- Advertised links are part of the test surface. If a response includes
  `details`, `cancel`, `logs`, `artifacts`, or `content`, each linked action
  needs an exact contract or must be withheld for states where it is not
  supported.
- State-specific actions should be fixture-backed by state. Queued
  cancellation and running cancellation are different contracts; documenting
  one does not imply the other.
- Fixture-ready error cases need HTTP status, request id, request body or
  precondition, and response envelope. An error code alone is not enough to
  generate replay fixtures.
- Fixture-ready is stricter than "the actor can answer from the excerpt."
  Before code handoff, canonical requests and responses should be promoted to
  standalone machine-readable fixtures or explicitly deferred.
- Policy may receive policy-relevant state as explicit context, such as
  `job_state`, but it must not infer sibling component state on its own.
- Long-running branches need liveness evidence. If a timeout path still expects
  an active job claim or resource lease at the end, the fixture must show the
  heartbeats or renewals that kept those records alive.
- Machine-readable fixtures need coverage alignment. A local JSON file is not
  fixture-ready just because it parses; it should cover the fixture IDs and
  error cases the role package advertises, or the package should explicitly
  narrow what is in scope.
- Fixture readiness also requires canonical alignment with prose contracts.
  A request/response JSON fixture that parses can still fail if endpoint paths,
  idempotency keys, status codes, error codes, messages, or required fields
  drift from the local contract excerpt.
- JSON fixture edits need fixture-ID-specific verification. Broad text or
  status-code searches can miss a wrong-location edit; assert the exact fixture
  ID and field that the finding named.
- Bounded actor runs should avoid broad path discovery. Even when an actor does
  not open sibling files, listing sibling paths weakens the evidence. Give
  actors exact allowed paths and ask them to read only those files.
- A local fixture file can be parseable and still not replayable. Fixture-ready
  requires complete request/response chains, edge-case preconditions, cursor
  semantics, and binary fixture files local to the actor that references them.
- Public actors need public-only scenario slices. A full internal scenario file
  can leak orchestration roles, component names, and private sequencing facts
  even when the actor package itself is otherwise public.
- Late-stage simulation findings are often spec/fixture readiness issues, not
  process failure. If the findings get smaller, more local, and more
  machine-checkable over time, the loop is doing useful work.
- Public fixture packages can still leak internals after public scenario
  slicing is fixed. Check precondition names, log text, byte fixture names, and
  branch/reset descriptions for worker, provider, queue, claim, lease, or
  checkpoint language.
- Event-list fixtures are acceptable only when they are deterministic replay
  objects. They need schema name, order, precondition, event fields, request
  templates, response templates, and resulting state transitions.
- Audit assertions belong to the component that owns the audit event. A runner
  can assert a public release response, but C06 owns release-event shape and
  dedupe behavior.
- Fixture-ready review can invalidate a previously clean package when the
  contract advertises more than the fixture file proves. Treat that as a scope
  alignment problem: add the fixture if it is v1-basic, or explicitly defer and
  narrow the prose if it is not.
- Public context hygiene includes shared helper files. A public actor should
  not receive a generic alias file if that file names internal component,
  runner, provider, or workspace paths.
- Late fixture-readiness blockers often come from local projection facts, not
  architecture. If a component returns a field derived from a dependency
  response, the dependency fake must contain that field, or the public response
  must be narrowed to fields the component can actually know.
- Components that enforce local auth need bounded credential and action facts
  in their own package, or explicit policy-check fixtures. They should not rely
  on coordinator memory about which token is allowed.
- A fixture-ready pass can still have non-blocking scoped notes. Record them
  explicitly, but do not patch them after the passing run unless the project is
  willing to rerun and prove the new package state.

## Boundary Patterns

- Use minimal policy-context projections for owner-scoped authorization.
  C05/C07 expose only opaque IDs, owner subject, resource kind, and
  policy-relevant state. C08 does not read component internals.
- Let the gateway own gateway concerns. C04 can own public invocation
  idempotency records, but not job lifecycle state or artifact metadata.
- Keep provider-local content refs transient and runner-only. Providers can
  hand bytes to runners with opaque expiring refs, but C07 remains the owner of
  final artifact bytes, metadata, and public retrieval.
- Split agent-safe and worker-visible projections. Execution plans, routes,
  provider endpoints, leases, and runtime details may be worker-visible
  contracts, but must not leak through agent APIs.
- Distinguish component-facing catalog metadata from agent-facing tools. C03 can
  return route metadata to components; C04 must project agent-safe `Tool`
  records.
- Authorize collections before item IDs are known by using opaque collection
  resource names, such as `job_artifacts:{producer_ref}`, with explicit owner
  context.

## Future Scenario Checklist

- Does every actor have enough local contract context to run without global
  requirements?
- Are all cross-boundary calls explicit public contracts?
- Can the gateway authorize reads without owning sibling state?
- Does every owner-scoped policy check have explicit owner context?
- Are binary responses documented as envelope exceptions with JSON errors?
- Are provider-local refs forbidden from agent-facing responses?
- Is each accepted fixture traceable to a run log and scenario?
- Is there a validator decision recorded before each requirement patch batch?
- Does the run's acceptance label match its executed scope?
- If a focused run passed, has a full scenario rerun confirmed the fix did not
  expose another boundary gap?
- Are checkpoint fixtures explicit enough to restart a flow without coordinator
  memory?
- Has the latest full scenario rerun proven fixture-ready claims after the
  most recent contract patch batch?
- Did the run avoid leaking internal state to public actors before they asked
  through the public API?
- Do component-facing endpoints name their caller credential and policy action?
- Do failure paths have exact append-log, cleanup, and fail-job fixtures?
- Do actor prompts name exact scenario and shared context paths?
- Are independent leases, claims, sessions, and uploads renewed through their
  own public contracts rather than implied by another component's heartbeat?
- Does every advertised link have a fixture-backed behavior for the current
  resource state?
- Does every negative fixture include an exact HTTP status, request id, and
  enough precondition state to replay it?
- Are accepted fixtures available as standalone machine-readable files, or is
  fixture extraction explicitly deferred?
- For long waits, do the fixtures show every claim, lease, session, or upload
  renewal needed to make the terminal cleanup legal?
- Do fixture README names, JSON fixture IDs, and contract examples describe the
  same replay cases?
- Do JSON fixtures use the exact endpoint paths, idempotency keys, status
  codes, error codes, and canonical messages named by the local contract?
- After a JSON fixture fix, did a check assert the exact fixture ID and field
  that changed?
- Did bounded actors avoid broad filesystem discovery and read only exact
  allowed files?
- Does each runner branch have full request/response steps rather than labels,
  response-only events, or prose that a test generator would need to interpret?
- Does each public actor receive only public scenario context and public API
  history, with internal orchestration details removed or replaced by public
  observations?
- Do public fixture files avoid internal checkpoint names, worker/provider log
  text, and private binary fixture names?
- If a fixture uses an event list instead of expanded requests, is the event
  list deterministic enough for a generator to replay without interpretation?
- Are component-owned audit assertions located in that component's fixture
  package rather than in a runner or coordinator package?
- Does each fixture package prove every behavior its local contract advertises,
  or explicitly defer behaviors outside the current readiness target?
- Are helper/context files actor-specific enough, especially for public actors?
- Does each response field come from the component's owned state, request, or
  an explicit public dependency response available in the fixture?
- If a component authenticates or rejects a caller locally, does its package
  include the bounded credential/action facts or public policy-check fixture
  needed to replay that decision?
- If a run passes with non-blocking notes, are those notes clearly scoped so
  they do not become hidden implementation assumptions?
