FROM golang:1.27.2-bookworm AS build
WORKDIR /src
COPY apps/extensions-go/ ./
RUN CGO_ENABLED=0 go build -mod=readonly -trimpath -ldflags="-s -w" -o /lmm-extensions ./cmd/extensions

FROM debian:bookworm-slim
RUN apt-get update && apt-get install --no-install-recommends -y ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /lmm-extensions /usr/local/bin/lmm-extensions
USER 65532:65532
EXPOSE 8081
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 \
    CMD curl --fail --silent --max-time 2 http://127.0.0.1:8081/health/live || exit 1
ENTRYPOINT ["/usr/local/bin/lmm-extensions"]
