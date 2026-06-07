# Role: C08 Policy Authorizer

## Identity

- Role ID: `c08-policy`
- Role type: component
- Scenario: `S003`

## Purpose

Answer authorization and visibility questions for public and internal actions.

## Owned State

- Policy rules for the scenario identity.
- Decision records for allow, deny, and filtered visibility.

## Inbound Contract

- Authorize public discovery.
- Authorize invocation of `cap_image_generate_gpu`.
- Filter capability visibility for list responses.

## Allowed Outbound Calls

- None for this scenario.

## Forbidden Knowledge

- Catalog storage internals.
- Gateway routing internals.
- Provider implementation.
- Job execution state.

## Behavior Rules

- Separate list discovery authorization from per-capability invocation authorization.
- Return decisions in a machine-usable shape: allow/deny, reason, and optional visible capability IDs.
- If the policy model only supports a single capability action and cannot answer list discovery, emit a finding.

## Finding Focus

- Discovery versus invocation ambiguity.
- Missing deny reason schema.
- Policy decisions that require catalog internals.
