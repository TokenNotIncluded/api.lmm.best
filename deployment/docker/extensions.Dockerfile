FROM golang:1.27.2-bookworm@sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61 AS build
WORKDIR /src
COPY services/extensions/ ./
RUN CGO_ENABLED=0 go build -mod=readonly -trimpath -ldflags="-s -w" -o /lmm-extensions ./cmd/extensions

FROM scratch AS binaries
COPY --from=build /lmm-extensions /lmm-extensions

FROM debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587 AS runtime
RUN apt-get update && apt-get install --no-install-recommends -y ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /lmm-extensions /usr/bin/lmm-extensions
COPY LICENSE NOTICE THIRD-PARTY-LICENSES.md /usr/share/licenses/lmm/
COPY config/legal/site.example.toml /usr/share/lmm/legal/site.example.toml
COPY config/legal/templates/ /usr/share/lmm/legal/templates/
USER 65532:65532
EXPOSE 8081
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 \
    CMD curl --fail --silent --max-time 2 http://127.0.0.1:8081/health/live || exit 1
ENTRYPOINT ["/usr/bin/lmm-extensions"]
CMD ["--config", "/etc/lmm.conf.d/extensions.toml"]
