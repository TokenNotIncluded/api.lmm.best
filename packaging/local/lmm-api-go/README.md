# Local `lmm-api-go` package

The Go backend binary owns the local build and packaging workflow. It builds the
frontend, produces the static backend binary, validates both artifacts, and
creates a single `lmm-api-go` Arch package inside an explicit marker-owned
workspace. The package installs `lmm-api.service` and the canonical
`/usr/bin/lmm-api` symlink to the Go provider; Rust remains an independently
packaged candidate.

```bash
apps/api-go/out/lmm-api deploy build \
  --repo /absolute/path/to/api.lmm.best \
  --workspace /absolute/marker-owned/workspace
```
