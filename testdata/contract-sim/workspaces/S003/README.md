# S003 Mirrored Simulation Workspace

This workspace mirrors the intended implementation boundary for S003. Future simulation runs should use these folders instead of the legacy flat `roles/S003` files.

## Rule

Each actor receives only:

- Its local folder.
- The exact S003 scenario file path.
- Exact scenario-level context file paths explicitly listed in this workspace
  README.
- Messages addressed to it during the run.

Actors must not read sibling folders or the full requirements tree during a bounded run.

Scenario-level context files are read-only shared fixtures for the scenario
itself, not component implementation state:

- `aliases.md` maps scenario actor names to workspace folders.
- `scenario-clock.md` defines canonical timestamps and checkpoints for replay.

Actor prompts for S003 must include these exact paths:

- Scenario:
  `/home/chris/Vaults/Pluggable Agent Control Plane/requirements/contract-sim/scenarios/S003-async-gpu-remote-node-image.md`
- Scenario clock:
  `/home/chris/Vaults/Pluggable Agent Control Plane/requirements/contract-sim/workspaces/S003/scenario-clock.md`
- Workspace aliases:
  `/home/chris/Vaults/Pluggable Agent Control Plane/requirements/contract-sim/workspaces/S003/aliases.md`

If a run uses vague references like "the S003 scenario file" and an actor
cannot locate the file, record the result as a process finding. That run may
still be useful, but it is not fixture-ready evidence.

## Layout

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
  aliases.md
  scenario-clock.md
```

## Constraint Validation

This layout was approved by a read-only constraint validator before generation. Approval depends on:

- Preserving component isolation.
- Keeping legacy `roles/S003` files as evidence during migration.
- Giving actors only bounded excerpts, not global context.
- Treating worker-visible job metadata as a documented projection, not a private backchannel.
- Keeping metadata and provider context non-secret and bounded.

## Future Code Mapping

The same boundaries should map naturally to a Go implementation:

- `cmd/wendy-gateway` or `internal/components/gateway`
- `internal/components/catalog`
- `internal/components/jobs`
- `internal/components/leases`
- `internal/components/artifacts`
- `internal/components/policy`
- `internal/components/nodeagent`
- `internal/providers/comfyui`
- `cmd/wendy-runner` or `internal/runners/composition`
