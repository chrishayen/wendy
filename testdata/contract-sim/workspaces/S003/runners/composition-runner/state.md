# Local State: Composition Runner

Initial runner state:

- `worker_id`: `runner_s003_0001`.
- `subject_id`: `sub_runner_s003`.
- Active job: none.
- Active lease: none.
- Active provider content refs: none.

The runner owns only transient execution state and operational logs.
The runner keeps C05 job claims and C06 resource leases alive through separate
public heartbeats. A C05 claim heartbeat does not refresh a C06 lease, and a
C06 lease expiration must be reported back to C05 through C05's public log and
fail contracts while the C05 claim is still active.
