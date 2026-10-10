FROM rust:1.99.0-bookworm AS build
WORKDIR /src/apps/lmm-core
COPY apps/lmm-core/Cargo.toml apps/lmm-core/Cargo.lock apps/lmm-core/build.rs ./
COPY contracts/proto /src/contracts/proto
COPY apps/lmm-core/src ./src
COPY apps/lmm-core/schema ./schema
RUN cargo build --locked --release --bins

FROM scratch AS binaries
COPY --from=build /src/apps/lmm-core/target/release/lmm-core /lmm-core
COPY --from=build /src/apps/lmm-core/target/release/lmm-core-admin /lmm-core-admin

FROM debian:bookworm-slim AS runtime
RUN apt-get update && apt-get install --no-install-recommends -y ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /run/lmm-core-rpc \
    && chown 65532:65532 /run/lmm-core-rpc \
    && chmod 0700 /run/lmm-core-rpc
COPY --from=build /src/apps/lmm-core/target/release/lmm-core /usr/local/bin/lmm-core
COPY --from=build /src/apps/lmm-core/target/release/lmm-core-admin /usr/local/bin/lmm-core-admin
COPY LICENSE NOTICE THIRD-PARTY-LICENSES.md /usr/share/licenses/lmm/
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 \
    CMD curl --fail --silent --max-time 2 http://127.0.0.1:8080/health/live || exit 1
ENTRYPOINT ["/usr/local/bin/lmm-core"]
