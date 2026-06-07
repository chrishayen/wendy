# Local State: C09 Runtime Node Agent

Initial S003 node state:

- `node_id`: `node_linux_gpu`
- Health: `healthy`.
- Service: `svc_comfyui_gpu`.
- Service status: `stopped` or `running` depending on scenario step.
- Provider endpoint: `http://node_linux_gpu:8188`.
- Advertised resource selector: `gpu`.

S003 bounded endpoint-auth facts:

- `token_s003_runner` resolves to `sub_runner_s003` and is allowed to call
  `node.read`, `node.service.start`, and `node.service.stop` for
  `node_linux_gpu` / `svc_comfyui_gpu`.
- `token_s003_agent` resolves to `sub_agent_s003` but is not allowed to call
  node lifecycle actions.
- Missing, malformed, or unknown bearer tokens are unauthorized.

These are local replay facts for C09 endpoint enforcement. They do not make
C09 the owner of global policy rules.
