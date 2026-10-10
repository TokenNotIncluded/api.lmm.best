# Same file for glibc/musl; the caller must match the tested ELF ABI to the base.
ARG RUNTIME_BASE
FROM ${RUNTIME_BASE}
COPY --chmod=0555 bin/lmm-core /usr/local/bin/lmm-core
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 CMD ["/usr/local/bin/lmm-healthprobe", "8080"]
ENTRYPOINT ["/usr/local/bin/lmm-core"]
