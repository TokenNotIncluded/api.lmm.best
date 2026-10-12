---
name: deployment
description: "Release, deploy, upgrade or recover api.lmm.best using reviewed scripts for the actual hosts, signed artifacts and exact acceptance evidence."
---

# Release and deployment

Read [the deployment workflow](../../../docs/deployment-workflow.md). Deployment
uses temporary shell scripts written and checked for the actual host state on
every update and database migration. This is the required deployment method;
never rebuild a generic Go deployment CLI or controller. Release publication
remains independent and uses the official build/signing workflows with source-matched local test evidence.

Inspect the active services, installation owner, exact candidate artifacts,
unfinished maintenance, locks and previous receipts before writing the script.
Reuse existing authorization and qualified signed artifacts. Resolve an unfinished
transaction in place; never delete its state or stop its owner to bypass a gate.
Preserve signature verification, host identity, file/database locks, a backup
and actual restore validation before migrations, old-data integrity, credential
protection, schema compatibility, financial drains, writer exclusivity and startup
seals. Check the shell syntax and applicable local fixtures before execution.
Deleting source must never stop an existing deployment process, delete its
private state or roll back the interrupted batch. Continue from its receipts.

Freeze the batch, make each mutation explicit and observe the exact result before
continuing. After an unknown result, read the live state before proceeding; never
blindly replay an update or migration. Existing
historical recovery scripts are evidence and source material, not a current CLI
or permission to replay an old incident against production.

Run relevant checks locally. Do not rebuild an unchanged component. Report the
actual released and deployed versions separately, with service/provider identity,
public routing/assets and required financial/writer acceptance evidence. A
successful command or health response alone does not establish deployment.
