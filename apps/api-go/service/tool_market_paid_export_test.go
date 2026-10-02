package service

import "net/http"

// ReplaceToolMarketRemoteTransportForTest is compiled only into service tests.
// It retains the production URL/header/body boundary, replaces the final
// network hop with a pinned TLS fixture, and cannot be called by a server build.
// Callers must not run in parallel and must stop all HTTP requests before undo.
func ReplaceToolMarketRemoteTransportForTest(transport http.RoundTripper) func() {
	previous := marketRemote
	client := newMarketRemoteHTTPClient()
	client.Transport = marketResponseTransport{base: transport}
	marketRemote = &ToolMarketRemote{client: client, slots: make(chan struct{}, 32)}
	return func() {
		marketRemote = previous
		client.CloseIdleConnections()
	}
}
