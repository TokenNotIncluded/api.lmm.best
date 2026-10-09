package service

import "net/http"

// MCPPublicClientHeaders permits browser-based public clients without giving
// cross-origin scripts access to a user's cookies or browser login session.
func MCPPublicClientHeaders(header http.Header) {
	header.Set("Access-Control-Allow-Origin", "*")
	header.Del("Access-Control-Allow-Credentials")
	header.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	header.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, MCP-Protocol-Version, MCP-Session-Id, Last-Event-ID")
	header.Set("Access-Control-Expose-Headers", "WWW-Authenticate, MCP-Protocol-Version, MCP-Session-Id")
	header.Set("Cache-Control", "no-store")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
}
