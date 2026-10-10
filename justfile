set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

# Fresh-install WIP. No production promotion or automatic database setup.
default:
    @just --list

setup:
    bun install --frozen-lockfile

lmm *args:
    cargo run --manifest-path apps/lmm/Cargo.toml --locked -- {{args}}

build-lmm:
    cargo build --manifest-path apps/lmm/Cargo.toml --locked --release

test-lmm:
    cargo fmt --manifest-path apps/lmm/Cargo.toml --all --check
    cargo clippy --manifest-path apps/lmm/Cargo.toml --locked --all-targets --all-features -- -D warnings
    cargo test --manifest-path apps/lmm/Cargo.toml --locked --all-targets

dev-core:
    bun run dev:core

dev-go:
    bun run dev:go

dev-extensions: dev-go

dev-web:
    bun run dev:web

build: build-core build-go build-web

build-core:
    bun run build:core

build-go:
    bun run build:go

build-extensions: build-go

build-web:
    VITE_REACT_APP_VERSION="$(git rev-parse --short=12 HEAD)" bun run build:web
    bun run --filter @lmm/web bundle:check

run-go: build-go
    exec apps/lmm-extensions/out/lmm-extensions

test: test-core test-go test-web

test-core:
    bun run test:core

test-go:
    bun run test:go

test-extensions: test-go

test-web:
    bun run test:web

check: format-check lint typecheck test check-boundaries

format:
    bun run format:core
    bun run format:go
    bun run format:web

format-check:
    bun run format-check:core
    bun run format-check:go
    bun run format-check:web

lint:
    bun run lint:core
    bun run lint:go
    bun run lint:web

typecheck:
    bun run typecheck:core
    bun run typecheck:go
    bun run typecheck:web

check-boundaries:
    python3 -B scripts/test-core-boundaries.py

check-protocol:
    bash scripts/generate-core-protocol.sh --check

# These commands build images, not deploy or replace existing databases.
docker: docker-core docker-extensions

docker-core:
    docker compose -f deployment/docker/compose.core.yml build core

docker-extensions:
    docker compose -f deployment/docker/compose.extensions.yml build extensions

test-docker:
    python3 -B scripts/test-core-rpc-docker.py

# Remove generated build output only, never database volumes.
clean-generated:
    rm -rf .turbo apps/web/.turbo apps/lmm-extensions/out apps/lmm-core/target apps/web/dist
