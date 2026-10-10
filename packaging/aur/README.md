# Packaging on the microkernel branch

Only the existing `lmm-api-web-bin` frontend package remains here. Its signed publication path is separate from backend implementation checks.

The old Go server source/binary packages, provider scripts and database-upgrade checks have been removed. The Go directory now builds `lmm-extensions`, not an interchangeable `lmm-api-go` server. Do not substitute it into an old service unit.

Use [the independent Docker projects](../../deployment/docker/README.md) for the Rust core and Go extensions. These are development stacks, not a completed production release.
