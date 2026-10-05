package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	appconstant "github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
)

// privateSafetyIdentityEnabled uses authenticated account/request metadata;
// body fields, model names, channel IDs and credentials never choose identity.
func privateSafetyIdentityEnabled(info *relaycommon.RelayInfo) bool {
	if info == nil || info.UserId <= 0 || info.ChannelMeta == nil || info.ChannelType != appconstant.ChannelTypeOpenAI {
		return false
	}
	settings := setting.GetModerationSettings()
	if !settings.SafetyIdentifierEnabled || (!info.IsAssistant && !settings.Enabled) || (info.IsAssistant && !settings.AssistantEnabled) {
		return false
	}
	_, _, policy, configured := setting.ResolveModerationRequestPolicy(settings, info.UserGroup, info.UsingGroup, info.IsAssistant)
	return configured && (policy.Mode == setting.ModerationModeTolerant || policy.Mode == setting.ModerationModeStrict)
}

// officialSafetyIdentityOrigin checks the final destination, not a channel
// label or configurable base URL. Compact, moderation and realtime requests
// deliberately do not receive this Chat Completions/Responses body field.
func officialSafetyIdentityOrigin(upstreamURL, effectiveHost string) bool {
	u, err := url.Parse(upstreamURL)
	if err != nil || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "wss") ||
		!strings.EqualFold(u.Hostname(), "api.openai.com") || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	if effectiveHost != "" && !strings.EqualFold(effectiveHost, "api.openai.com") && !strings.EqualFold(effectiveHost, "api.openai.com:443") {
		return false
	}
	return u.EscapedPath() == "/v1/chat/completions" || u.EscapedPath() == "/v1/responses"
}

// ApplyOpenAIPrivateSafetyIdentifier runs after conversion and all parameter
// overrides, including raw pass-through. When identity initialization fails,
// remove any supplied identifier and continue the request without one. The
// optional feature must neither trust a client identity nor block API calls.
func ApplyOpenAIPrivateSafetyIdentifier(ctx context.Context, info *relaycommon.RelayInfo, upstreamURL, effectiveHost string, body []byte) []byte {
	if !privateSafetyIdentityEnabled(info) || !officialSafetyIdentityOrigin(upstreamURL, effectiveHost) {
		return body
	}
	return applyPrivateSafetyIdentity(ctx, info.UserId, body)
}

func applyPrivateSafetyIdentity(ctx context.Context, userID int, body []byte) []byte {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return body // Preserve malformed/non-object requests for existing validation.
	}
	delete(fields, "safety_identifier")
	if identifier, err := model.OpenAIPrivateSafetyIdentifier(ctx, userID); err == nil {
		fields["safety_identifier"], _ = json.Marshal(identifier)
	}
	patched, err := json.Marshal(fields)
	if err != nil {
		return body
	}
	return patched
}

type safetyIdentityReadCloser struct {
	io.Reader
	io.Closer
}

type safetyIdentityReadError struct{ err error }

func (reader safetyIdentityReadError) Read([]byte) (int, error) { return 0, reader.err }

// ApplyOpenAIPrivateSafetyIdentifierToRequest is the final HTTP transport
// boundary. It also protects direct/raw request paths and makes the rewritten
// body replayable with accurate length for transport/compatibility retries.
func ApplyOpenAIPrivateSafetyIdentifierToRequest(req *http.Request, info *relaycommon.RelayInfo) {
	if req == nil || req.URL == nil || req.Body == nil || req.Method != http.MethodPost ||
		!privateSafetyIdentityEnabled(info) || !officialSafetyIdentityOrigin(req.URL.String(), req.Host) {
		return
	}
	oldBody := req.Body
	body, err := io.ReadAll(oldBody)
	if err != nil {
		// Restore consumed bytes and the original reader/close behavior. Optional
		// identity processing does not add a new failure path to the request.
		req.Body = safetyIdentityReadCloser{Reader: io.MultiReader(bytes.NewReader(body), safetyIdentityReadError{err}, oldBody), Closer: oldBody}
		return
	}
	// Keep the original policy decision for this send: a concurrent settings
	// refresh must not restore a client-supplied identity between two reads.
	patched := applyPrivateSafetyIdentity(req.Context(), info.UserId, body)
	_ = oldBody.Close()
	req.Body = io.NopCloser(bytes.NewReader(patched))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(patched)), nil }
	req.ContentLength = int64(len(patched))
	req.Header.Del("Content-Length")
}
