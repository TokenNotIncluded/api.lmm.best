# Historical incident 343 workflow

The owner-requested GitHub Actions recovery entry has been retired. Its request
file, workflow, SSH controller, and workflow-only repair wrappers were removed
when server operations returned to manual deployment.

Historical server audit logs and native recovery evidence are retained. Do not
replay a request commit or rerun an old workflow to perform server operations.
Use the native CLI through operator-controlled SSH and verify the current state
before any manual recovery. See [server operations](server-ops.md).
