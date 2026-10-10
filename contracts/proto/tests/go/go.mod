module github.com/TokenNotIncluded/api.lmm.best/extensions/protocoltests

go 1.25.0

require (
	github.com/TokenNotIncluded/api.lmm.best/extensions v0.0.0
	github.com/lib/pq v1.10.9
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.11
)

require (
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260706201446-f0a921348800 // indirect
)

replace github.com/TokenNotIncluded/api.lmm.best/extensions => ../../../../apps/lmm-extensions
