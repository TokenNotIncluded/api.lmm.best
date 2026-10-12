# Local Go package

Build the backend and frontend with the root build commands, then call
`build-local-package.sh --workspace PATH --binary PATH --frontend PATH` with
absolute reviewed paths and a workspace carrying `.lmm-deploy-workspace`.
The package contains the application and frontend files, not a deployment tool.
Installing it does not activate the frontend. Deployment uses scripts written
for the actual host state; see [the workflow](../../../docs/deployment-workflow.md).
