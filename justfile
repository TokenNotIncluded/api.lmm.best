set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

default: dev

# Install workspace dependencies from the committed lockfile.
setup:
    bun install --frozen-lockfile

# Start only the default PostgreSQL and Valkey development infrastructure.
infra-up:
    @if [[ ! -f docker-compose.dev.yml ]]; then \
      echo "error: docker-compose.dev.yml is not present in this branch; infra-up requires a local compose file." >&2; \
      echo "Set up a compose stack manually or restore docker-compose.dev.yml before using just infra-up." >&2; \
      exit 1; \
    fi
    docker compose -f docker-compose.dev.yml up -d postgres valkey

# Stop only the default PostgreSQL and Valkey development infrastructure.
infra-down:
    @if [[ ! -f docker-compose.dev.yml ]]; then \
      echo "error: docker-compose.dev.yml is not present in this branch; infra-down requires a local compose file." >&2; \
      exit 1; \
    fi
    docker compose -f docker-compose.dev.yml stop postgres valkey

# Start PostgreSQL, Valkey, the Go API, and the shared web frontend.
dev: infra-up
    #!/usr/bin/env bash
    set -euo pipefail
    pids=()
    cleanup() { for pid in "${pids[@]}"; do kill "$pid" 2>/dev/null || true; done; }
    trap cleanup EXIT INT TERM
    bun run dev:go & pids+=("$!")
    bun run dev:web & pids+=("$!")
    wait -n "${pids[@]}"

# Start only the Go API development process.
dev-go:
    bun run dev:go

# Start only the shared web development process.
dev-web:
    bun run dev:web

# Start the isolated Rust preview profile and shared web frontend without Go.
dev-rust:
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ ! -f docker-compose.dev.yml ]]; then \
      echo "error: docker-compose.dev.yml is not present in this branch; dev-rust requires the preview compose stack." >&2; \
      exit 1; \
    fi
    docker compose -f docker-compose.dev.yml --profile rust-preview up -d postgres valkey lmm-api-rs-preview
    exec bun run dev:web

# Build and run the default Go provider through the public symlink.
run: build
    exec apps/api-go/out/lmm-api serve

# Run an already-built Go provider through the public symlink.
run-go:
    @test -x apps/api-go/out/lmm-api-go || { echo "error: apps/api-go/out/lmm-api-go is missing; run 'just build'" >&2; exit 1; }
    @test -L apps/api-go/out/lmm-api && test "$(readlink apps/api-go/out/lmm-api)" = lmm-api-go || { echo "error: apps/api-go/out/lmm-api is not the provider symlink" >&2; exit 1; }
    exec apps/api-go/out/lmm-api serve

# Run the explicit Rust backend with standardized infrastructure.
run-rust: infra-up
    bun run dev:rust

# Build the frontend and default Go backend as independent artifacts.
build: build-web build-go

# Build the shared web frontend.
build-web:
    VITE_REACT_APP_VERSION="$(git rev-parse --short=12 HEAD)" bun run build:web
    @test -f apps/web/dist/index.html || { echo "error: apps/web/dist/index.html was not produced" >&2; exit 1; }
    bun run --filter @lmm/web bundle:check

# Build the real Go provider and public local symlink independently.
build-go:
    bun run build:go
    @test -x apps/api-go/out/lmm-api-go || { echo "error: real Go provider binary was not produced" >&2; exit 1; }
    @test -L apps/api-go/out/lmm-api && test "$(readlink apps/api-go/out/lmm-api)" = lmm-api-go || { echo "error: public Go provider symlink was not produced" >&2; exit 1; }

# Build the explicit Rust backend.
build-rust:
    bun run build:rust

# Build the default Go production artifact and the optional Rust backend.
build-all: build build-rust

# Test the default Go backend and shared web frontend.
test: test-go test-web

test-go:
    bun run test:go

test-web:
    bun run test:web

test-rust:
    bun run test:rust

# Test both backend implementations and the frontend.
test-all: test test-rust

# Run default Go and web quality gates.
check: format-check lint typecheck test check-deploy

# Verify the native Go build, frontend publication, backup, and deployment contract.
check-deploy:
    cd apps/api-go && go test ./internal/appcli -count=1

format: format-go format-web

format-go:
    bun run format:go

format-web:
    bun run format:web

format-rust:
    bun run format:rust

format-check: format-check-go format-check-web

format-check-go:
    bun run format-check:go

format-check-web:
    bun run format-check:web

format-check-rust:
    bun run format-check:rust

lint: lint-go lint-web

lint-go:
    bun run lint:go

lint-web:
    bun run lint:web

lint-rust:
    bun run lint:rust

typecheck: typecheck-go typecheck-web

typecheck-go:
    bun run typecheck:go

typecheck-web:
    bun run typecheck:web

typecheck-rust:
    bun run typecheck:rust

# Remove generated build and task-runner output only.
clean-generated:
    rm -rf .turbo apps/web/.turbo apps/api-go/out apps/api-rust/target apps/web/dist

# Build the default Go image from a local Dockerfile (if present).
docker: docker-go

docker-go:
    @if [[ ! -f Dockerfile ]]; then \
      echo "error: Dockerfile is not present in this branch; Docker build is unavailable." >&2; \
      echo "Use local build commands instead (for example: just build-go / just build-rust)." >&2; \
      exit 1; \
    fi
    docker build -f Dockerfile -t "lmm-api-go:${LMM_IMAGE_TAG:-local}" .

docker-rust:
    @if [[ ! -f Dockerfile.rust ]]; then \
      echo "error: Dockerfile.rust is not present in this branch; Rust Docker build is unavailable." >&2; \
      echo "Use bun run build:rust if you need a local Rust preview artifact." >&2; \
      exit 1; \
    fi
    docker build -f Dockerfile.rust -t "lmm-api-rs-preview:${LMM_IMAGE_TAG:-local}" .

# Build the default Go production package.
package: package-go

package-go: build
    apps/api-go/out/lmm-api deploy build \
      --repo "$(pwd)" \
      --workspace "$LMM_API_BUILD_WORKSPACE"

# Validate the public AUR package that consumes prebuilt release assets.
test-package-bin:
    bash packaging/aur/test-matrix.sh
    bash packaging/aur/test-bin-makepkg.sh

# Stage an already-created immutable production release plan.
stage-production:
    apps/api-go/out/lmm-api deploy production stage \
      --plan "$LMM_API_RELEASE_PLAN" \
      --plan-sha256 "$LMM_API_RELEASE_PLAN_SHA256" \
      --confirm "$CONFIRM_PRODUCTION"

# Promote an already-staged immutable production release plan.
deploy-production:
    apps/api-go/out/lmm-api deploy production promote \
      --plan "$LMM_API_RELEASE_PLAN" \
      --plan-sha256 "$LMM_API_RELEASE_PLAN_SHA256" \
      --age-identity-file "$LMM_BACKUP_AGE_IDENTITY_FILE" \
      --confirm "$CONFIRM_PRODUCTION"
