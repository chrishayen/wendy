# Contract Simulation: Testing The System Before The System Exists

I started this project with a pretty simple idea: I want a system that can host
services and expose them to agents. Some services might run on a dedicated Mac.
Some might run on a Linux GPU box. Some might be local, some remote, some
expensive, some queued. The important part is that agents should be able to use
the services through a clean public interface without caring where anything
actually runs.

At first that sounds like an API gateway. But once we started refining it, it
became clear that this is really a distributed service host. The gateway is only
one part of it. There is a catalog, jobs, leases, artifacts, policy, runtime
nodes, providers, runners, and all the public contracts between them.

The hard part was not naming the parts. The hard part was keeping the parts
isolated.

## The Rule That Changed The Project

The most useful rule we landed on was this:

A component should be something that can live and be built in isolation. It
should have value on its own. It should not know about things outside of its
boundary except through public contracts.

That forced the requirements into a different shape. Instead of starting with
layers, we started with components. Layers came later. That mattered because a
layer can describe how things are composed, but it should not secretly become a
shared implementation dependency that every component has to know about.

The goal became: build a component, wrap it up, forget about its internals, and
move on.

Subcomponents followed the same idea at a smaller scale. Each subcomponent
should have a small local API, fixture set, schema, or test harness. If a
subcomponent only works because the developer remembers some outside detail,
that is not isolated enough.

## Why We Built A Simulation Project

After the first requirements pass, I wanted to test the requirements without
writing the system yet.

The first idea was to role-play the components. Spawn a subagent for the gateway,
another for jobs, another for policy, another for artifacts, and so on. Then
pretend to send real requests through the system and see what breaks.

That sounds a little strange, but it immediately exposed something important:
if every role-player sees the whole requirements folder, the test is fake. A
component in the real system will not get to read a sibling's private storage
model. It will only get the public contract.

So the simulation had to be bounded.

Each actor gets only:

- Its local role.
- Its local state.
- Its local contract excerpts.
- Its local fixtures.
- The scenario step being executed.
- Messages addressed to it.

If the actor cannot respond from that context, that is a finding. We do not let
the coordinator invent missing details just to make the run pass.

## The Filesystem Became Part Of The Test

One of the bigger improvements was changing the simulation layout to mirror the
future implementation layout.

The first role packages were flat. That was useful for a dry run, but it did not
tell us enough about the future code organization. Once the workspace was
reorganized into `components/`, `runners/`, and `actors/`, the structure itself
started testing the design.

For S003, the workspace looks like this:

```text
workspaces/S003/
  actors/
    agent-user/
  components/
    c03-service-catalog/
    c04-agent-tool-gateway/
    c05-async-job-service/
    c06-resource-lease-service/
    c07-artifact-store/
    c08-access-policy-and-secrets/
    c09-runtime-node-agent/
    c10-comfyui-provider/
  runners/
    composition-runner/
```

That layout forced the same questions the code will force later:

- What does this component own?
- What does it expose?
- What does it consume?
- What state is private?
- What state needs a public projection?
- What fixtures prove the boundary?

This was one of the better findings from the whole process. The filesystem is
not just documentation. It is a cheap way to rehearse the codebase.

## The Loop

The process that worked was:

1. Run a simulation.
2. Record the result.
3. Treat blocked actors as evidence.
4. Identify missing contracts or unclear ownership.
5. Before changing requirements, run a constraint validation pass.
6. If the fix is approved, patch the requirements.
7. If the fix is not approved, have the coordinator and validator debate the
   right fix, with arbitration if needed.
8. Rerun the simulation.
9. Keep the logs append-only.

That last part matters. The logs should not be cleaned up to make the process
look smoother than it was. The failed runs are where the useful information is.

By Run 007, the final focused authorization issue had passed. Then the more
important lesson showed up: I had treated that focused pass too broadly. A full
rerun in Run 008 still blocked at artifact upload creation because the runner
did not have the C07 upload idempotency rule in its local package. Run 009
passed the artifact path after that was fixed, but it still left fixture
precision findings.

That was not a failure of the simulation. That was the simulation doing its
job. The mistake was in how I labeled the result.

## What The Simulation Found

The scenario was intentionally concrete: an agent discovers a GPU-backed image
generation tool, invokes it through the public API, the work runs on a remote
Linux GPU node, and the agent retrieves the final artifact.

ComfyUI was only an example provider. The point was to prove the generic host
model.

The simulation found several issues that would have been expensive to discover
in code.

The first problem was actor context. The flat role packages did not contain
enough local contract information. The gateway and runner could not produce
canonical responses without reaching for global context. That led to the
implementation-mirrored workspace.

The second problem was public versus worker-visible data. Agent-facing job
responses should not expose routes, leases, runtime details, provider
endpoints, or execution plans. But the runner does need enough worker-visible
metadata to execute the job. The fix was to split the public `AgentJob`
projection from worker-visible job and execution plan contracts.

The third problem was artifact ownership. The provider can generate bytes, but
the artifact store has to own durable artifact bytes, metadata, checksums, list
behavior, and public retrieval. The provider can hand the runner a temporary
`content_ref`, but that reference must never become part of the agent-facing
API.

The fourth problem was authorization. The gateway needs to authorize reads
without owning sibling state. That forced a minimal policy-context pattern:
components expose small owner-scoped projections with opaque IDs, owner subject,
resource kind, and policy-relevant state. Policy can make a decision without
reading component internals.

The fifth problem was artifact listing. To authorize an artifact list, the
gateway does not know the artifact IDs yet. The fix was to authorize the
collection first with an opaque resource string like `job_artifacts:{job_id}`,
then call the artifact store only after policy allows.

The sixth problem was idempotency. Public invocation idempotency belongs at the
gateway because it is a gateway concern. That does not mean the gateway owns job
lifecycle state. It owns the idempotency record for the public request and
delegates the actual job state to the job service.

None of those are huge ideas by themselves. The value is that the simulation
forced them to become explicit before implementation.

## What Worked Well

The strongest part of this approach is that it tests isolation directly.

Normal requirements review can find contradictions, but it often misses the
moment when a component quietly needs private knowledge from a sibling. Contract
simulation makes that visible because the actor simply gets stuck.

It also creates better requirements. A vague requirement can survive a document
review. It has a much harder time surviving a simulated request where each actor
has to return a concrete response.

The other strength is that the output is reusable. The accepted run becomes a
source for fixtures. The fixtures can later become code-level contract tests.
So the simulation is not throwaway planning work. It is early test design.

The process also fits agent-based development. If the final implementation will
be farmed out to agents, the requirements need to be shaped for isolated agents
from the beginning. Each agent should get a bounded package with a contract,
fixtures, state assumptions, and acceptance criteria. The simulation rehearses
that handoff.

## What Did Not Work At First

Giving everyone too much context made the test weak.

Flat role files were also too abstract. They let us describe actors, but they
did not flush out code layout or ownership. The move to mirrored workspaces was
the point where the simulation started producing better findings.

Another problem was the temptation to fix a run by adding hidden knowledge.
That would make the transcript pass, but it would weaken the system. The
constraint validation step solved that by checking proposed fixes against the
project rule: components stay isolated and communicate through public contracts.

There is also a cost. This is slower than just writing a big requirements doc.
But it is cheaper than building the wrong distributed system and discovering
the boundaries are wrong after the code exists.

## The Acceptance Problem

The biggest process bug was the word "accepted."

I let a focused pass sound like a full scenario pass. Run 007 proved that the
C04/C08 artifact-list authorization fix worked. It did not prove the whole
distributed image-generation flow still worked end to end. When we reran the
full flow, the runner blocked at C07 upload creation because the runner package
did not include the required upload idempotency header.

That changed the process. A run now needs an acceptance level:

- Focused pass: one issue or slice passed.
- Scenario pass: the full scenario passed end to end.
- Fixture-ready pass: the accepted transcript is precise enough to extract
  replayable fixtures.
- Implementation-ready pass: the contracts and fixtures are ready to drive code
  work without hidden coordinator memory.

That distinction matters. A focused pass is useful, but it should not update
the project-level accepted run by itself. After a focused fix, the next question
is simple: run the whole thing again.

The other lesson is about checkpoint state. If a rerun starts in the middle of
a flow, the filesystem needs an explicit checkpoint fixture. The coordinator
should not be the only place that knows the active job, claim, lease, uploaded
bytes, or artifact state.

Run 010 made the next distinction obvious: a full happy-path scenario pass is
still not the same as fixture-ready. The flow worked end to end, but the logs
showed small precision gaps: some dependency paths were still label-driven,
the clock was partly coordinator memory, digest headers were not in their
canonical wire form, and a public actor briefly received internal completion
context before polling the public API.

Those are exactly the kinds of details that become test fixtures later. So the
process now keeps going after a scenario pass. The next target is fixture-ready:
canonical requests, canonical responses, explicit checkpoint state, byte
fixtures, error envelopes, and no hidden coordinator memory.

Run 012 added another useful lesson: reproducibility is stricter than "the
actor basically understood the scenario." One actor reported that the prompt
said to use the S003 scenario file, but did not give the exact path. The run
still produced useful findings, but that is not fixture-ready. A future test
runner should not have to guess where the scenario lives.

The other good Run 012 finding was about lifetimes. A job claim and a resource
lease are not the same thing. The runner can keep its C05 job claim alive while
forgetting to refresh the C06 GPU lease. That should fail the job, but it
should fail through the documented C05 log and fail contracts, not because C05
secretly knows C06 expired something. That is the kind of boundary problem this
process is good at finding before code exists.

Run 013 made the next layer of the problem visible: every link is a promise.
If the gateway returns a `cancel` link, the cancel flow has to be fixture-backed
for that job state. If it returns a `details` link, tool details need a real
contract. If it returns a log cursor, the cursor rules need to be precise
enough that a test can replay the next request. This is where the process stops
being a high-level walkthrough and starts becoming a real fixture generator.

Run 014 pushed on the same idea from the failure side. A response envelope can
look exact and still not be fixture-ready if the HTTP status, branch checkpoint,
or precondition is only implied. The fix was not to make the actors smarter.
The fix was to make the contracts more concrete: the queued cancel branch gets
its own checkpoint, append-log errors get exact preconditions, C08 gets
`job_state` as explicit policy context, and undefined provider-run routes get a
real `404` envelope. That is the difference between "I can reason through
this" and "I can turn this into a test fixture."

Run 015 made the next line even sharper. The actors could run the system from
bounded context, but several packages still had their fixtures as Markdown
examples instead of standalone JSON files. That is good enough for human role
play, but not good enough for code tests. So the next refinement target is not
more architecture. It is extraction: promote the canonical requests and
responses into replayable fixture files, fix the remaining branch timing
mismatch, and only then call the scenario fixture-ready.

Run 016 proved that adding JSON files is not the end of the job. The JSON has
to line up with the fixture names, README promises, error cases, and replay
preconditions. It also found a more subtle problem in the timeout path. A
900-second provider timeout cannot just jump from "provider accepted" to "job
failed" if the runner is still supposed to release an active GPU lease. The
fixtures have to show the runner keeping both the job claim and the resource
lease alive during the wait, then releasing the lease and failing the job
through the documented contracts. That is exactly the kind of thing I want this
process to catch before there is production code.

The post-Run 016 patch is the first pass that really looks like fixture
completion instead of architecture discovery. The work was mostly alignment:
make the JSON fixture IDs match the local README promises, add missing negative
cases, give C04 and C08 their own bounded fixture files, and turn the timeout
liveness story into replayable data. It still needs another full bounded run.
That is the rule now: a patch can improve the requirements, but only a later
simulation can prove the improved requirements.

Run 017 showed the next trap: machine-readable does not automatically mean
canonical. Several JSON files parsed and covered the right rough behavior, but
still disagreed with their own local contract excerpts on status codes, request
paths, idempotency keys, response fields, or error messages. That is a
different class of problem than the early architecture issues. The system shape
is mostly holding now. The work has narrowed to making the fixtures exact
enough that generated code tests will not inherit a contradiction.

The post-Run 017 patch was mostly correction, not invention. It aligned the
machine-readable files with the contracts that were already written, added
deterministic heartbeat event lists for long waits, and promoted the runner and
gateway paths out of Markdown-only examples. The next run is the proof point:
if the actors can replay from those bounded packages without finding another
contract drift, then S003 is much closer to actual fixture-ready status.

Run 018 proved the bar is even higher than "the JSON parses and covers the
main branches." The remaining problems are smaller now, but they are exactly
the kind that would break generated tests: a replay response missing the
original fields, a cursor that does not clearly mean "after this entry," a
policy check missing one explicit context field, a runner branch that says
"call C10" without embedding the actual request and response. It also exposed
a process issue: a few actors used broad path discovery and one read an extra
workspace README. That does not mean the architecture is wrong, but it means
the evidence is not clean enough. A bounded test has to be bounded all the way
down to filesystem access.

Run 019 answered a question I had to ask directly: is the issue the process or
the specs? The answer is both, but mostly the specs now. The process improved:
actors got exact paths, avoided broad discovery, and the component boundaries
mostly held. But that stricter run made a public-context leak more obvious. The
agent-user should not receive the full internal scenario file just because it
is convenient for the simulation. It needs a public-only slice of the scenario,
the same way a real user only sees public requests, public responses, public
links, and public state. The rest of the findings were mostly last-mile
contract drift: an error code that disagreed with prose, a missing request body
in a dependency fixture, a provider width case that contradicted the declared
range. That is annoying work, but it is the right work. It means the simulation
has moved from "what is this system?" to "can these contracts become tests?"

Run 020 made the bar more precise again. The public actor no longer got the
internal scenario, but its fixture file still had internal fingerprints:
checkpoint names, worker-flavored progress messages, and a provider-named byte
fixture. That is a useful distinction. It is not enough to hide internal docs
from a role player if the public replay data still talks like the internals.
The same run also forced a decision about long-running fixtures. A list of
heartbeats is not replayable just because it is written in JSON. It has to say
exactly how each event expands into request, response, request id, timestamp,
and state transition. That is the shape that can become generated contract
tests later.

Run 021 was frustrating in a useful way. Some packages finally cleared the bar:
C03, C06, and C10 could answer from their own folders without reaching outside
their boundaries. But the run also proved that "fixture-ready" is not a single
switch. C07 had passed before, then failed under a stricter question because
the contract advertised a size-mismatch case that the machine fixtures did not
prove. C08 exposed a real policy contract issue: not every artifact action can
require an `artifact_id` if registration happens before the artifact exists.
And the public actor still received a shared alias file that named internals.
The fix is not to broaden the system. The fix is to make the current basic
surface honest: prove it with fixtures, or mark it out of scope.

The post-Run 021 patch followed that rule. Some fixes added concrete fixtures,
like C05 cancel replay and C07 size mismatch. Some fixes narrowed S003 without
weakening the system, like deferring runner runtime-start and artifact-upload
failure branches to later scenarios. That distinction matters. Otherwise a
fixture-readiness loop can accidentally turn into scope creep.

Run 022 is the first run where the remaining failures feel like true last-mile
test material instead of system discovery. The public actor passed with a
public-only helper file. C03, C06, C08, C10, and the runner passed from their
own bounded folders. What is left is not "who owns the system?" It is exact
stuff: can C04 return a field if the C05 fake did not give it that field, does
C09 have local facts for which token can start a runtime, do C07's upload
fixtures prove every validation rule the prose advertises. That is tedious,
but it is also the point. These are the contradictions that would otherwise
show up later as failing generated tests or, worse, as slightly different
implementations built by different agents.

Run 023 was the smallest failure yet, and it was my fault in a useful way. Nine
packages passed. C09 failed because a broad status-code patch changed the wrong
fixture: `service_start_accepted` became `200` when the contract still said a
stopped-service start returns `202`, while the actual `service_running` read
was already fixed. That is a process lesson, not a system lesson. From here on,
fixture edits need exact assertions against the fixture ID and field that the
finding named. "The JSON parses" and "I see a 200 somewhere" are not enough.

Run 024 passed. That matters because it was not a happy-path hand wave. Every
actor got only its own files and the exact shared context it was allowed to
see. The public actor stayed public. C05 and C06 kept their lifetimes separate.
C07 owned the artifact. C08 made decisions from supplied context. C09 exposed
runtime status without becoming policy. The runner tied the pieces together
without reaching into component-owned audit internals. There were still two
small notes, but they were scoped notes, not blockers. That is the difference
between "we made the simulation say yes" and "the fixture set is ready to
become tests."

## The Main Lesson

I think this is a real testing method.

It is not unit testing, because there is no code yet. It is not just design
review, because actors have to execute flows using only the contracts they are
allowed to know. It is not just role play, because the filesystem, logs,
fixtures, contracts, and validators make it reproducible.

The best name for it right now is contract simulation.

The point is to test whether the system shape is buildable before building it.
For distributed services, that matters a lot. The hardest failures are often
not inside one component. They happen at the boundaries: ownership, policy,
state projection, retries, artifacts, queues, and what one service is allowed
to know about another service.

This process found those boundary problems early.

## Where This Goes Next

The next step is to promote the accepted S003 fixture-ready run into code-level
contract tests, starting from Run 024 rather than the first focused pass:

- Discovery and invocation.
- Job status, logs, artifact list, and artifact content.
- Gateway idempotency replay and conflict.
- Job and artifact policy context.
- Policy allow, missing-context deny, and policy-denied decisions.
- Runner-only provider content retrieval.
- Lease grant, heartbeat, release, cancel, and expiry.

When the real code exists, those fixtures should become matching contract tests.
The same structure used for role players should map to the Go implementation.

That is the larger idea: requirements should not just be written and then
discarded. They should turn into simulations, fixtures, and finally tests. The
same boundary that makes a component easy to give to an agent should also make
it easy to build, test, wrap up, and forget.
