package credittransition

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"time"
)

type Binding struct {
	Format                 string `json:"format"`
	TransitionID           string `json:"transition_id"`
	TransitionIntentSHA256 string `json:"transition_intent_sha256"`
	PrepareConfigSHA256    string `json:"prepare_config_sha256"`
	ProviderSHA256         string `json:"provider_sha256"`
	TargetCreditsPerUSD    int64  `json:"target_credits_per_usd"`
}

func (config Config) Binding(digest string) Binding {
	return Binding{Format: Format, TransitionID: config.TransitionID, TransitionIntentSHA256: config.TransitionIntentSHA256,
		PrepareConfigSHA256: digest, ProviderSHA256: config.ProviderSHA256, TargetCreditsPerUSD: config.TargetCreditsPerUSD}
}

// Handler has no dependency on the business router, cache, authentication,
// callbacks or background workers. A valid health response means only that the
// sealed pre-financial preparation is still intact.
func Handler(config Config, digest, version string, verify func(context.Context) error) http.Handler {
	binding := config.Binding(digest)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		if request.Method != http.MethodGet || (request.URL.Path != "/api/status" && request.URL.Path != "/api/livez") || !localProbe(request) {
			writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(MaintenanceBody(config.TransitionID)))
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
		defer cancel()
		ready := verify != nil && verify(ctx) == nil
		writer.Header().Set("Content-Type", "application/json")
		if !ready {
			writer.WriteHeader(http.StatusServiceUnavailable)
		}
		response := map[string]any{"success": ready, "ready": ready, "maintenance": true, "business_enabled": false,
			"data": map[string]any{"version": version, "credit_transition": binding}}
		if request.URL.Path == "/api/livez" {
			response["live"] = ready
		}
		_ = json.NewEncoder(writer).Encode(response)
	})
}

func localProbe(request *http.Request) bool {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return false
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || !addr.IsLoopback() {
		return false
	}
	host = request.Host
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	if host != "127.0.0.1" && host != "::1" && host != "localhost" {
		return false
	}
	return request.Header.Get("Forwarded") == "" && request.Header.Get("X-Forwarded-Host") == "" && request.Header.Get("X-Forwarded-For") == ""
}
