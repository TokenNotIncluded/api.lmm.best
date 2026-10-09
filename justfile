set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

default: dev

# Run the independent LMM CLI development preview.
lmm *args:
    cargo run --manifest-path apps/lmm/Cargo.toml --locked -- {{args}}

# Build the LMM CLI without rebuilding either backend.
build-lmm:
    cargo build --manifest-path apps/lmm/Cargo.toml --locked --release

# Validate LMM CLI policy, discovery and subprocess behavior.
test-lmm:
    cargo fmt --manifest-path apps/lmm/Cargo.toml --all --check
    cargo clippy --manifest-path apps/lmm/Cargo.toml --locked --all-targets --all-features -- -D warnings
    cargo test --manifest-path apps/lmm/Cargo.toml --locked --all-targets

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

# Build and run the default Go provider through the public symlink.
run: build
    exec apps/api-go/out/lmm-api serve

# Run an already-built Go provider through the public symlink.
run-go:
    @test -x apps/api-go/out/lmm-api-go || { echo "error: apps/api-go/out/lmm-api-go is missing; run 'just build'" >&2; exit 1; }
    @test -L apps/api-go/out/lmm-api && test "$(readlink apps/api-go/out/lmm-api)" = lmm-api-go || { echo "error: apps/api-go/out/lmm-api is not the provider symlink" >&2; exit 1; }
    exec apps/api-go/out/lmm-api serve

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
build-core:
    bun run build:core

# Build the default Go production artifact and the WIP Rust core and Go module host.
build-all: build build-core build-extensions

# Test the default Go backend and shared web frontend.
test: test-go test-web

test-go:
    bun run test:go

test-web:
    bun run test:web

test-core:
    bun run test:core

# Test both backend implementations and the frontend.
test-all: test test-core test-extensions

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

format-core:
    bun run format:core

format-check: format-check-go format-check-web

format-check-go:
    bun run format-check:go

format-check-web:
    bun run format-check:web

format-check-core:
    bun run format-check:core

lint: lint-go lint-web

lint-go:
    bun run lint:go

lint-web:
    bun run lint:web

lint-core:
    bun run lint:core

typecheck: typecheck-go typecheck-web

typecheck-go:
    bun run typecheck:go

typecheck-web:
    bun run typecheck:web

typecheck-core:
    bun run typecheck:core

# Remove generated build and task-runner output only.
clean-generated:
    rm -rf .turbo apps/web/.turbo apps/api-go/out apps/core-rust/target apps/extensions-go/out apps/web/dist

# Build the two WIP microservice images independently. See deployment/docker/README.md.
docker: docker-core docker-extensions

docker-core:
    docker compose -f deployment/docker/compose.core.yml build core

docker-extensions:
    docker compose -f deployment/docker/compose.extensions.yml build extensions

dev-core:
    bun run dev:core

dev-extensions:
    bun run dev:extensions

build-extensions:
    bun run build:extensions

test-extensions:
    bun run test:extensions

# Build the default Go production package.
package: package-go

# Reuse an existing operator; bootstrap it only on a fresh checkout.
# The native package command owns the actual frontend and backend builds.
package-go:
    bash scripts/lmm-api-deploy.sh package

# Tag origin/main, sign-publish and deploy the frontend; waits for both runs.
ship-web *tag:
    bash scripts/lmm-api-deploy.sh web ship {{tag}}

# Tag origin/main and sign-publish the frontend without deploying it.
release-web *tag:
    bash scripts/lmm-api-deploy.sh web release {{tag}}

# Dispatch an existing signed frontend release, without building Go or Web.
deploy-web tag:
    bash scripts/lmm-api-deploy.sh web deploy {{quote(tag)}}

# Inspect/wait for an exact deployment run; dispatch alone is not success.
deploy-web-status run_id:
    bash scripts/lmm-api-deploy.sh web status {{quote(run_id)}}

deploy-web-watch run_id:
    bash scripts/lmm-api-deploy.sh web watch {{quote(run_id)}}

# Check workstation deployment paths without server/database access.
test-deploy-entrypoint:
    python3 -B scripts/test-deploy-entrypoint.py -v
    python3 -B scripts/test-web-ship.py -v

# Validate the public AUR package that consumes prebuilt release assets.
test-package-bin:
    bash packaging/aur/test-matrix.sh
    bash packaging/aur/test-bin-makepkg.sh

# Stage an already-created immutable production release plan.
stage-production:
    scripts/lmm-api-deploy.sh production stage \
      --plan "$LMM_API_RELEASE_PLAN" \
      --plan-sha256 "$LMM_API_RELEASE_PLAN_SHA256" \
      --confirm "$CONFIRM_PRODUCTION"

# Promote an already-staged immutable production release plan.
deploy-production:
    scripts/lmm-api-deploy.sh production promote \
      --plan "$LMM_API_RELEASE_PLAN" \
      --plan-sha256 "$LMM_API_RELEASE_PLAN_SHA256" \
      --age-identity-file "$LMM_BACKUP_AGE_IDENTITY_FILE" \
      --confirm "$CONFIRM_PRODUCTION"
