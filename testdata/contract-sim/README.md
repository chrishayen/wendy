# PACP Contract Simulation

This folder applies the reusable `Agent contract role play framework` to the Pluggable Agent Control Plane requirements.

The goal is to validate PACP as a distributed service host for arbitrary services, not only the example services named in the raw idea. Every scenario should preserve the component isolation rule: a component should provide standalone value, own its boundary, and avoid knowledge of siblings except through explicit contracts.

## Folder Map

- `workspaces/`: implementation-mirrored scenario workspaces used by current simulations.
- `roles/`: legacy flat role context packages kept as migration evidence.
- `scenarios/`: scenario scripts.
- `runs/`: execution logs and findings.
- `fixtures/`: accepted request and response examples that future implementation tests can replay.

## Reference Docs

- [blog.md](blog.md) is a first-person writeup of the process, experience,
  findings, strengths, and problems solved during the S003 loop.
- [Learnings.md](Learnings.md) captures reusable lessons from the S003
  simulation loop.
- [runs/S003/README.md](runs/S003/README.md) indexes the S003 run logs and
  traces each issue family to the improvements it produced.

## Current Scenarios

- `S003-async-gpu-remote-node-image`: agent invokes a GPU image generation capability running on a remote Linux node.
  - Active workspace: `workspaces/S003/`.
  - Legacy role source: `roles/S003/`.
  - Latest run: `runs/S003/run-024.md` as a full bounded fixture-ready pass
    for the declared S003 scope.
  - Active refinement target: extract accepted fixtures into code-level
    contract tests and prepare implementation work items.

## Operating Rule

When running a simulation, do not hand each actor the full requirements folder.
Use the active workspace folder for that actor, the exact scenario file path,
exact scenario-level context file paths, the scenario step, and messages
addressed to that actor. Missing local context is recorded as a finding.

Scenario workspaces should mirror intended implementation boundaries. If an actor needs a sibling workspace folder to participate, record that as evidence that the component boundary, contract package, or future code layout needs refinement.
