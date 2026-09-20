# On-demand Go diagnostics

`ENABLE_PPROF=true` enables the existing pprof HTTP endpoints. The defaults stay
`PPROF_BIND_ADDRESS=127.0.0.1` and `PPROF_PORT=8005`; only the exact loopback
addresses `127.0.0.1` and `::1` are accepted. The endpoint mux and request-header
timeout are unchanged.

The old automatic high-CPU sampler (`common.Monitor`) has been removed. Enabling
pprof no longer polls CPU usage or creates timestamped files under `./pprof`.
This removes the unchecked CPU sample/panic path, unbounded profile accumulation
and file descriptor leak on failed profile starts. Out-of-tree callers of the
removed Go helper must use the on-demand HTTP interface instead. Existing
profile files are not deleted by the application.

For a deliberate local capture, choose the output location and permissions:

```sh
umask 077
curl --fail 'http://127.0.0.1:8005/debug/pprof/profile?seconds=10' -o cpu.pprof
go tool pprof cpu.pprof
```

Profiles can contain operational details. Store or remove them deliberately;
do not expose the diagnostic port publicly. There is no automatic retention
policy because the application no longer creates these profile files.

The diagnostic server now belongs to the application's runtime-loop registry.
On cancellation, request contexts are canceled, allowing pprof CPU/trace captures
to stop. HTTP shutdown gets a five-second drain budget (below the ten-second
loop budget), then force-closes connections if necessary. The serving goroutine
is joined and the listener is closed on cancellation or a serving error. This
also applies to startup paths that stop the runtime registry before returning.
Pyroscope configuration and the application's normal HTTP listener are unchanged.

The regression suite covers request cancellation, idle/listening sockets,
interruption of a real long-running CPU profile, bounded forced closure,
listen/serve errors and canceled startup. It does not exercise a production
process or production profile data.
