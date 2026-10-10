# Builder-only. OS_BASE must be a digest-pinned glibc image.
# runtime-assets is assembled and checked on the builder; no package installs here.
ARG OS_BASE
FROM ${OS_BASE}
COPY runtime-assets/etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY runtime-assets/usr/share/zoneinfo/ /usr/share/zoneinfo/
COPY --chmod=0555 runtime-assets/lmm-healthprobe /usr/local/bin/lmm-healthprobe
RUN mkdir -p /run/lmm-core-rpc /tmp && chown 65532:65532 /run/lmm-core-rpc \
    && chmod 0700 /run/lmm-core-rpc && chmod 1777 /tmp
ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt TZ=Etc/UTC
USER 65532:65532
