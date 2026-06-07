# Local State: C06 Resource Lease Service

Initial S003 resource state:

- `resource_id`: `res_gpu_0`
- `selector`: `gpu`
- `status`: `available`
- `node_id`: `node_linux_gpu`
- Active lease: none.
- Queue: empty.

C06 treats `job_s003_0001` as an opaque holder id.
C06 does not read or mutate C05 job claims. A lease can expire while the C05
worker claim remains active through C05's own heartbeat contract.

Bounded S003 caller projection:

- `Bearer token_s003_runner` resolves to `sub_runner_s003` for C06 audit
  attribution.
- This projection is local S003 fixture context, not a shared auth database.
