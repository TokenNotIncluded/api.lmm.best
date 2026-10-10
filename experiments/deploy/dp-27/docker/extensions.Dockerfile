ARG RUNTIME_BASE
FROM ${RUNTIME_BASE}
COPY --chmod=0555 bin/lmm-extensions /usr/local/bin/lmm-extensions
USER 65532:65532
EXPOSE 8081
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 CMD ["/usr/local/bin/lmm-healthprobe", "8081"]
ENTRYPOINT ["/usr/local/bin/lmm-extensions"]
