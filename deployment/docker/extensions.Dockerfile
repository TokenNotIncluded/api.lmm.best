FROM golang:1.27.2-bookworm AS build
WORKDIR /src
COPY apps/lmm-extensions/ ./
RUN CGO_ENABLED=0 go build -mod=readonly -trimpath -ldflags="-s -w" -o /lmm-extensions ./cmd/extensions

FROM scratch AS binaries
COPY --from=build /lmm-extensions /lmm-extensions

FROM debian:bookworm-slim AS runtime
RUN apt-get update && apt-get install --no-install-recommends -y ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /lmm-extensions /usr/local/bin/lmm-extensions
COPY LICENSE NOTICE THIRD-PARTY-LICENSES.md /usr/share/licenses/lmm/
COPY config/legal/site.example.json /usr/share/lmm/legal/site.example.json
COPY config/legal/templates/ /usr/share/lmm/legal/templates/
USER 65532:65532
EXPOSE 8081
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 \
    CMD curl --fail --silent --max-time 2 http://127.0.0.1:8081/health/live || exit 1
ENTRYPOINT ["/usr/local/bin/lmm-extensions"]
