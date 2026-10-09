FROM rust:1.99.0-bookworm AS build
WORKDIR /src/apps/core-rust
COPY apps/core-rust/Cargo.toml apps/core-rust/Cargo.lock ./
COPY apps/core-rust/src ./src
COPY apps/core-rust/migrations ./migrations
RUN cargo build --locked --release --bins

FROM debian:bookworm-slim
RUN apt-get update && apt-get install --no-install-recommends -y ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /src/apps/core-rust/target/release/lmm-core /usr/local/bin/lmm-core
COPY --from=build /src/apps/core-rust/target/release/lmm-core-admin /usr/local/bin/lmm-core-admin
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 \
    CMD curl --fail --silent --max-time 2 http://127.0.0.1:8080/health/live || exit 1
ENTRYPOINT ["/usr/local/bin/lmm-core"]
