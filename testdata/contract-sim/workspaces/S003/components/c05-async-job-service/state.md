# Local State: C05 Async Job Service

Initial S003 job state after gateway submission:

- `job_id`: `job_s003_0001`
- `state`: `queued`
- `requester_id`: `sub_agent_s003`
- `capability_id`: `cap_image_generate_gpu`
- `metadata.execution_plan`: worker-visible documented projection.
- `artifact_refs`: empty until completion.

C05 stores artifact refs only, not bytes or storage paths.

C05 job claims are independent from C06 resource leases. A C05 heartbeat keeps
the active worker claim writable, but it does not refresh any resource lease.
C05 does not infer job failure from C06 lease state; the runner records that
through C05's public log and fail contracts.

S003 C05 cancellation is defined only for queued jobs. Running cancellation is
deferred until a separate orchestration contract is defined.
