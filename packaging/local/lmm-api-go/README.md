# Local `lmm-api-go` package

The Go backend CLI owns the local build and packaging workflow. It builds the
frontend, produces the real static `lmm-api-go` provider, validates both
artifacts, and creates a Go Arch package inside an explicit marker-owned
workspace. The package installs the provider and current shared service assets,
but does not own `/usr/bin/lmm-api`; the verified CLI selects providers by
atomically managing that one-hop symlink. Rust remains independently packaged.

```bash
apps/api-go/out/lmm-api deploy build \
  --repo /absolute/path/to/api.lmm.best \
  --workspace /absolute/marker-owned/workspace
```
