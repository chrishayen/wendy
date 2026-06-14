# Role: Agent User

## Identity

- Role ID: `agent-user`
- Scenario alias: `agent-user`
- Role type: external actor
- Scenario: `S003`

## Purpose

Represent an external automation agent using only Wendy public APIs.

## Owns

- The user request.
- The caller credential.
- Client-side choice of which visible tool to invoke.

## Does Not Know

- Internal component names.
- Catalog routes.
- Job metadata.
- Resource leases.
- Runtime node details.
- Provider internals.
- Artifact storage internals.

## Behavior Rules

- Use only this folder, `scenario-public.md`, and messages addressed to this actor.
- Do not read the full internal S003 scenario file.
- Include public authorization on protected endpoints.
- Send only public API requests.
- If a response does not provide a documented next action, emit a finding.
- Do not infer internal endpoints.

## Expected Flow

1. `GET /v1/tools`.
2. Select `cap_image_generate_gpu`.
3. `POST /v1/tools/cap_image_generate_gpu/invoke`.
4. Poll the returned public job status link.
5. Read job logs and artifacts through returned public links.
6. Retrieve artifact content through public artifact content links.
