# One binary and the same stable runtime base. No core source or core executable.
ARG RUNTIME_BASE
FROM ${RUNTIME_BASE}
COPY --chmod=0555 bin/lmm-core-admin /usr/local/bin/lmm-core-admin
USER 65532:65532
HEALTHCHECK NONE
ENTRYPOINT ["/usr/local/bin/lmm-core-admin"]
