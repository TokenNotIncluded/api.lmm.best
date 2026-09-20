# IO.NET container queries

The deployment controller uses `ListContainers`, `GetContainerDetails` and
`GetContainerLogsRaw`. Their request paths, authentication, response decoding
and log query options are unchanged by the cleanup in issue #399.

The unused `GetContainerJobs`, `GetContainerLogs`, `StreamContainerLogs`,
`RestartContainer`, `StopContainer` and `ExecuteInContainer` methods have been
removed. They had no repository callers or maintained provider contract. This
is a source-level API removal for any out-of-tree consumers of this package;
raw log consumers should use `GetContainerLogsRaw`. There is no replacement
advertised for jobs, streaming, restart, stop or exec. The removed polling loop
must not be treated as a supported streaming protocol.

Tests exercise the retained calls through the existing `HTTPClient` interface:

```sh
cd apps/api-go
go test ./pkg/ionet -count=1
go vet ./pkg/ionet
```

The fixtures check LMM's current client contract; they are not evidence of a
live IO.NET integration test.
