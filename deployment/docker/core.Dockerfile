FROM rust:1.99.0-bookworm@sha256:114c7a4425406451c2866b6aafe69fe29b1b298832db1277d411ac73c82d04d6 AS build
WORKDIR /src/services/core
COPY services/core/Cargo.toml services/core/Cargo.lock services/core/build.rs ./
COPY contracts/proto /src/contracts/proto
COPY services/core/src ./src
COPY services/core/schema ./schema
# Bound formal image compilation independently of runtime container limits.
RUN AWS_LC_SYS_USE_SYSTEM=0 cargo build --locked --release --jobs 2 --bin lmm-core

FROM scratch AS binaries
COPY --from=build /src/services/core/target/release/lmm-core /lmm-core

FROM debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587 AS runtime
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
