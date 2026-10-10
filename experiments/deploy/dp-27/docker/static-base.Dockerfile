# Builder-only. Static Go or independently validated static musl artifacts only.
FROM scratch
COPY runtime-assets/etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY runtime-assets/usr/share/zoneinfo/ /usr/share/zoneinfo/
COPY --chmod=0555 runtime-assets/lmm-healthprobe /usr/local/bin/lmm-healthprobe
COPY --chown=65532:65532 --chmod=0700 runtime-assets/empty-rpc/ /run/lmm-core-rpc/
ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt TZ=Etc/UTC
USER 65532:65532
