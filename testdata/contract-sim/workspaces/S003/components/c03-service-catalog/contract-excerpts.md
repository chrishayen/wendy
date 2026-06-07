# Contract Excerpts: C03 Service Catalog

## Capability List Response

List request:

```http
GET /v1/catalog/capabilities?limit=50
```

Optional query fields:

- `cursor`: opaque cursor from previous page.
- `limit`: integer from 1 to 500.
- `capability_id`: optional exact capability id filter.
- `visible_capability_ids`: optional already-policy-filtered allow list supplied by the caller as bounded context, not as a policy decision made by C03.

S003 capability detail uses this list endpoint with `capability_id` filtering,
for example:

```http
GET /v1/catalog/capabilities?capability_id=cap_image_generate_gpu
```

S003 does not define a separate `GET /v1/catalog/capabilities/{id}` endpoint.

Multi-value `visible_capability_ids` uses repeated query parameters:

```http
GET /v1/catalog/capabilities?visible_capability_ids=cap_a&visible_capability_ids=cap_b
```

When `capability_id` and `visible_capability_ids` are both supplied, C03 uses
intersection semantics: the capability must match the exact `capability_id`
and be present in the visible allow list. If the intersection is empty, return
`ok: true`, `items: []`, and `next_cursor: null`.

C03 does not authenticate or authorize the caller in S003. C04 calls C08 first and only asks C03 for records it is allowed to project.

Cursor rule: `next_cursor` is `null` when there are no more results. Non-null
cursors are opaque and must be echoed only through the `cursor` query
parameter. Unknown, stale, or malformed cursors return the standard error
envelope with `code: invalid_cursor`.

Catalog list responses are component-facing. They may include route metadata so
C04 and runners can build worker-visible execution plans. C04 must project a
separate agent-safe `Tool` record and must not expose route metadata directly
to agents.

## Schema Ownership And Projection

C01 remains the source of the schema contract rules. C03 owns the stored
capability records for S003, including each capability's `input_schema` and
`output_schema` values. C04 must project those schema values exactly into the
public `Tool` record. C04 may add public links, names, descriptions, resource
hints, and artifact hints, but it must not independently narrow, widen, or
rewrite C03's schema values.

If C04 has a local schema example, that example is a fixture copy of the C03
record for replay. It is not an independent schema source.

List filter miss behavior: exact filters such as `capability_id` or
`visible_capability_ids` that match no records return `ok: true`,
`items: []`, and `next_cursor: null`.
The S003 filtered-empty fixture uses `meta.request_id:
req_s003_catalog_filtered_empty`.

Catalog list responses use:

```json
{
  "ok": true,
  "data": {
    "items": [
      {
        "capability": {},
        "route": {},
        "service": {}
      }
    ],
    "next_cursor": null
  },
  "links": {},
  "meta": {"request_id": "req_...", "schema_version": "v1"}
}
```

S003 catalog list fixtures use `meta.request_id:
req_s003_catalog_list`.
S003 capability detail lookup through `capability_id` filtering uses
`meta.request_id: req_s003_catalog_detail_lookup`.

## S003 Capability Record

Capability:

```json
{
  "id": "cap_image_generate_gpu",
  "service_id": "svc_comfyui_gpu",
  "name": "GPU image generation",
  "description": "Generate an image using a GPU-backed provider.",
  "execution_mode": "async",
  "input_schema": {
    "type": "object",
    "required": ["prompt"],
    "properties": {
      "prompt": {"type": "string"},
      "width": {"type": "integer"},
      "height": {"type": "integer"},
      "seed": {"type": "integer"}
    }
  },
  "output_schema": {
    "type": "object",
    "properties": {
      "artifact_refs": {"type": "array", "items": {"type": "string"}}
    }
  },
  "examples": [],
  "side_effects": "external",
  "resource_hints": [{"selector": "gpu", "required": true, "quantity": 1}],
  "artifact_hints": [{"media_type": "image/png", "count": "one"}],
  "timeout_hint": "15m"
}
```

Route:

```json
{
  "capability_id": "cap_image_generate_gpu",
  "service_id": "svc_comfyui_gpu",
  "provider_endpoint": "http://node_linux_gpu:8188",
  "provider_health_path": "/v1/provider/health",
  "provider_invoke_path": "/v1/provider/capabilities/cap_image_generate_gpu/invoke",
  "node_id": "node_linux_gpu",
  "node_managed": true,
  "service_start_mode": "on_demand",
  "resource_hints": [{"selector": "gpu", "required": true, "quantity": 1}],
  "artifact_hints": [{"media_type": "image/png", "count": "one"}]
}
```

`provider_endpoint` is the provider origin/base URL and must not include the
API path prefix. Provider paths such as `provider_invoke_path` remain absolute
API paths. Callers join `provider_endpoint` and `provider_invoke_path` without
duplicating `/v1`.

`node_id` is an opaque route key identifying the node that can host or expose
the provider. C03 does not own C09 runtime-node addresses, C09 deployment
configuration, or service-discovery internals. Runners resolve `node_id` to a
C09 node-agent address through runner deployment config or a separately
documented service-discovery contract.

Service:

```json
{
  "id": "svc_comfyui_gpu",
  "name": "ComfyUI GPU Provider",
  "description": "Node-managed image generation provider.",
  "version": "v1",
  "provider_kind": "comfyui",
  "tags": ["image", "gpu"]
}
```

## Route Lookup

`GET /v1/catalog/capabilities/cap_image_generate_gpu/route` returns the route object above in a success envelope.

Concrete response:

```json
{
  "ok": true,
  "data": {
    "capability_id": "cap_image_generate_gpu",
    "service_id": "svc_comfyui_gpu",
    "provider_endpoint": "http://node_linux_gpu:8188",
    "provider_health_path": "/v1/provider/health",
    "provider_invoke_path": "/v1/provider/capabilities/cap_image_generate_gpu/invoke",
    "node_id": "node_linux_gpu",
    "node_managed": true,
    "service_start_mode": "on_demand",
    "resource_hints": [{"selector": "gpu", "required": true, "quantity": 1}],
    "artifact_hints": [{"media_type": "image/png", "count": "one"}]
  },
  "links": {},
  "meta": {"request_id": "req_s003_catalog_route", "schema_version": "v1"}
}
```

Unknown capability response:

```json
{
  "ok": false,
  "error": {
    "code": "not_found",
    "message": "capability not found",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_catalog_not_found", "schema_version": "v1"}
}
```

Invalid cursor response:

```json
{
  "ok": false,
  "error": {
    "code": "invalid_cursor",
    "message": "cursor is invalid or expired",
    "retryable": false
  },
  "links": {},
  "meta": {"request_id": "req_s003_catalog_invalid_cursor", "schema_version": "v1"}
}
```
