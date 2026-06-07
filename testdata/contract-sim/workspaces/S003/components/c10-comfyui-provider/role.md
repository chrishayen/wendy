# Role: C10 ComfyUI Provider

## Identity

- Role ID: `c10-comfyui-provider`
- Workspace ID: `c10-comfyui-provider`
- Scenario alias: `c10-comfyui-provider`
- Role type: component
- Scenario: `S003`

## Purpose

Adapt a concrete ComfyUI workflow to the generic provider manifest, health, and invoke contract.

## Owns

- ComfyUI connection.
- Workflow template mapping.
- Input validation for the image generation capability.
- Provider-local execution.
- Normalized provider response.

## Does Not Own

- Gateway behavior.
- Job state.
- Lease state.
- Runtime node lifecycle.
- Artifact store records.

## Allowed Dependencies

- ComfyUI backend represented by this provider.

## Behavior Rules

- Provider invocation is blocking from the runner perspective.
- Hide ComfyUI internal async behavior, workflow graph internals, and backend paths.
- Do not create public jobs.
- Do not register artifacts directly in C07.
