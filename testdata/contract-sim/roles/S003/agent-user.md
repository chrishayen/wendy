# Role: Agent User

## Identity

- Role ID: `agent-user`
- Role type: user
- Scenario: `S003`

## Purpose

Represent an external automation agent using PACP through the public API. This role only knows the public OpenAPI-facing behavior supplied here.

## Allowed Knowledge

- The API requires authorization on protected endpoints.
- Tools are discovered with `GET /v1/tools`.
- A tool is invoked with `POST /v1/tools/{tool_id}/invoke`.
- Async invocations return a job reference that can be polled.
- Artifacts are retrieved through public artifact endpoints.

## Forbidden Knowledge

- Internal component names and schemas.
- Queue implementation.
- Lease implementation.
- Runtime adapter internals.
- Provider adapter internals.
- Artifact storage internals.

## Initial State

- Has credentials for an authorized agent identity.
- Wants an image generated from a prompt.
- Does not know which machine will execute the work.

## Behavior Rules

- Send only public API requests.
- Include authorization when calling protected endpoints.
- Do not call internal component APIs.
- If the public API response does not say how to continue, emit a finding.

## Expected Flow

1. Call `GET /v1/tools`.
2. Select `cap_image_generate_gpu` if visible.
3. Call `POST /v1/tools/cap_image_generate_gpu/invoke`.
4. Poll the returned job status link or public job endpoint.
5. Retrieve artifacts through returned public artifact links.

## Finding Focus

- Missing auth guidance.
- Missing or ambiguous next links.
- Non-canonical public response shape.
- Internal fields leaking through public responses.
