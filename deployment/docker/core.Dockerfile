FROM rust:1.99.0-bookworm AS build
WORKDIR /src/services/core
COPY services/core/Cargo.toml services/core/Cargo.lock services/core/build.rs ./
COPY contracts/proto /src/contracts/proto
COPY services/core/src ./src
COPY services/core/schema ./schema
# Bound formal image compilation independently of runtime container limits.
RUN cargo build --locked --release --jobs 2 --bin lmm-core

FROM scratch AS binaries
COPY --from=build /src/services/core/target/release/lmm-core /lmm-core

FROM debian:bookworm-slim AS runtime
RUN apt-get update && apt-get install --no-install-recommends -y ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /run/lmm-core-rpc \
    && chown 65532:65532 /run/lmm-core-rpc \
    && chmod 0700 /run/lmm-core-rpc
COPY --from=build /src/services/core/target/release/lmm-core /usr/bin/lmm-core
COPY LICENSE NOTICE THIRD-PARTY-LICENSES.md /usr/share/licenses/lmm/
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 \
    CMD curl --fail --silent --max-time 2 http://127.0.0.1:8080/health/live || exit 1
ENTRYPOINT ["/usr/bin/lmm-core"]
CMD ["--config", "/etc/lmm.conf.d/core.toml"]
