# Local State: C07 Artifact Store

Initial S003 state:

- Upload sessions: none.
- Artifacts: none.

Expected happy path:

- Create `upload_s003_0001`.
- Receive content.
- Complete as `art_s003_0001`.

C07 owns final artifact metadata and bytes. In S003, agent-facing retrieval
links are mediated by C04 after public authorization.
