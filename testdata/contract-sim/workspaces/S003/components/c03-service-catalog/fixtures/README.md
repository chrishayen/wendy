# C03 Fixtures

Fixture candidates:

- Catalog list response with one `cap_image_generate_gpu` record and
  `req_s003_catalog_list`.
- Exact C03-owned `input_schema` and `output_schema` copied into C04 public
  `Tool` projections.
- Route lookup response for `cap_image_generate_gpu`.
  - `node_id` is an opaque route key. C03 does not own C09 node-agent address
    resolution.
- Empty list response when caller supplies an allowed-capability filter that excludes the capability.
- `not_found` error for unknown capability id.
- `visible_capability_ids` multi-value query encoding uses repeated query
  parameters.
- Combined `capability_id` and `visible_capability_ids` uses intersection
  semantics.
- Machine-readable fixture set: `s003-fixtures.json`.
- `invalid_cursor` error for stale, unknown, or malformed cursor.
