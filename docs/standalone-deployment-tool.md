# Removed deployment tool

The separate `lmm-api-deploy` CLI, its Go implementation and repository wrapper
have been removed. New deployments use [scripts written for the actual host
state](deployment-workflow.md). The application binary retains runtime safety
commands and does not provide replacement deployment commands.

Historical recovery documents may name an archived deployment engine. Preserve
an existing engine, signed artifacts and private owner state when they are needed
to finish an interrupted transaction. This source cleanup does not uninstall or
modify any server, release artifact or unfinished maintenance process.
