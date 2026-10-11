package controller

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauthserver"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func drawingOAuthCredentials(t *testing.T, integration *service.OAuthIntegration, userID int, groups ...string) *oauthserver.TokenResponse {
	t.Helper()
	ctx := context.Background()
	verifier := strings.Repeat("v", 43)
	challenge := sha256.Sum256([]byte(verifier))
	binding := strings.Repeat("b", 43)
	scopes := []string{service.OAuthMCPDrawingScope}
	for _, group := range groups {
		scopes = append(scopes, service.OAuthGroupScope(group))
	}
	query := url.Values{
		"response_type": {"code"}, "client_id": {service.OAuthPiClientID},
		"redirect_uri": {service.OAuthNativeRedirect}, "resource": {integration.Resource},
		"scope": {strings.Join(scopes, " ")}, "state": {strings.Repeat("s", 32)},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"},
	}
	pending, err := integration.Core.BeginAuthorization(ctx, query.Encode(), binding)
	require.NoError(t, err)
	consent, err := integration.Core.TrustedPrepareConsent(ctx, pending.Transaction, binding, int64(userID))
	require.NoError(t, err)
	approved, err := integration.Core.TrustedApprove(ctx, pending.Transaction, binding, consent.Secret)
	require.NoError(t, err)
	redirect, err := url.Parse(approved.RedirectURI)
	require.NoError(t, err)
	credentials, err := integration.Core.Exchange(ctx, url.Values{
		"grant_type": {"authorization_code"}, "client_id": {service.OAuthPiClientID},
		"redirect_uri": {service.OAuthNativeRedirect}, "resource": {integration.Resource},
		"code": {redirect.Query().Get("code")}, "code_verifier": {verifier},
	}.Encode(), oauthserver.SenderBinding{})
	require.NoError(t, err)
	return credentials
}

func TestDrawingMCPOAuthGrantBoundsEndToEnd(t *testing.T) {
	fixture := newDrawingParityFixture(t, common.GetTrustQuota(), http.StatusOK)
	db := fixture.db
	require.NoError(t, db.AutoMigrate(&model.OpenSourceBountyMCPConfirmation{}, &model.OpenSourceBountyMCPOperation{}))
	require.NoError(t, model.MigrateOAuthBilling(db))
	setAssistantKeyOption(t, db, "UserUsableGroups", `{"default":"Chat","image-2":"Drawing","image-alt":"Other drawing"}`)
	setAssistantKeyOption(t, db, "GroupRatio", `{"default":1,"image-2":1,"image-alt":1}`)
	alternative := fixture.channel
	alternative.Id = 0
	alternative.Name, alternative.Group = "other local drawing group", "image-alt"
	require.NoError(t, db.Create(&alternative).Error)
	require.NoError(t, alternative.AddAbilities(nil))
	model.InvalidatePricingCache()

	require.Nil(t, service.CurrentOAuthIntegration())
	integration, err := service.ConfigureOAuthIntegration(db, service.OAuthServerConfig{
		Enabled: true, Issuer: "https://drawing-oauth.example.test", Groups: []string{"default", "image-2", "image-alt"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = service.ConfigureOAuthIntegration(nil, service.OAuthServerConfig{}) })
	toolMarketBuiltinDrawingRelay.Lock()
	previousRelay := toolMarketBuiltinDrawingRelay.handler
	toolMarketBuiltinDrawingRelay.Unlock()
	t.Cleanup(func() { SetToolMarketBuiltinDrawingRelay(previousRelay) })
	server := httptest.NewServer(NewDrawingMCPHandler())
	t.Cleanup(server.Close)
	connect := func(credentials *oauthserver.TokenResponse) *mcp.ClientSession {
		client := mcp.NewClient(&mcp.Implementation{Name: "drawing-oauth-grant-test", Version: "1"}, &mcp.ClientOptions{
			MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
		})
		session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
			Endpoint: server.URL, HTTPClient: &http.Client{Transport: drawingMCPBearerTransport{token: credentials.AccessToken}}, DisableStandaloneSSE: true,
		}, nil)
		require.NoError(t, err)
		t.Cleanup(func() { _ = session.Close() })
		return session
	}
	limited := connect(drawingOAuthCredentials(t, integration, fixture.user.Id, "default", "image-alt"))
	capabilities, err := limited.CallTool(context.Background(), &mcp.CallToolParams{Name: "drawing.list_capabilities", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, capabilities.IsError)
	encoded, err := json.Marshal(capabilities.StructuredContent)
	require.NoError(t, err)
	var catalog struct {
		Data struct {
			Groups []drawingMCPGroup `json:"groups"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(encoded, &catalog))
	var names []string
	for _, group := range catalog.Data.Groups {
		names = append(names, group.Name)
	}

	// Every denied spelling must stop before confirmation persistence, key
	// creation/resolution, provider execution, or quota consumption.
	for _, group := range []string{"image-2", " image-2 ", "IMAGE-ALT", "auto", service.OAuthGroupID("image-alt"), service.OAuthGroupScope("image-alt")} {
		result, err := limited.CallTool(context.Background(), &mcp.CallToolParams{Name: "drawing.generate", Arguments: map[string]any{
			"prompt": "local authorization fixture", "group": group, "model": drawingParityModel,
		}})
		require.NoError(t, err)
		require.True(t, result.IsError, "group %q", group)
		require.Empty(t, result.InputRequests)
	}
	require.ElementsMatch(t, []string{"default", "image-alt"}, names)
	var confirmations, keys int64
	require.NoError(t, db.Model(&model.OpenSourceBountyMCPConfirmation{}).Count(&confirmations).Error)
	require.Zero(t, confirmations)
	require.NoError(t, db.Model(&model.Token{}).Count(&keys).Error)
	require.EqualValues(t, 1, keys)
	require.Zero(t, fixture.upstream.Load())

	for _, group := range []string{"", " image-alt "} {
		result, err := limited.CallTool(context.Background(), &mcp.CallToolParams{Name: "drawing.generate", Arguments: map[string]any{
			"prompt": "local authorized alternative", "group": group, "model": drawingParityModel,
		}})
		require.NoError(t, err)
		require.False(t, result.IsError)
		require.True(t, result.NeedsInput())
		confirmation, ok := result.InputRequests["confirmation"].(*mcp.ElicitParams)
		require.True(t, ok)
		require.Contains(t, confirmation.Message, `group "image-alt"`)
	}
	require.Zero(t, fixture.upstream.Load())

	// Ordinary image-2 grants retain their default, explicit confirmation and
	// real key-backed local relay billing.
	credentials := drawingOAuthCredentials(t, integration, fixture.user.Id, "default", "image-2")
	allowed := connect(credentials)
	arguments := map[string]any{"prompt": "local authorized drawing", "model": drawingParityModel}
	pending, err := allowed.CallTool(context.Background(), &mcp.CallToolParams{Name: "drawing.generate", Arguments: arguments})
	require.NoError(t, err)
	require.False(t, pending.IsError)
	require.True(t, pending.NeedsInput())
	confirmation, ok := pending.InputRequests["confirmation"].(*mcp.ElicitParams)
	require.True(t, ok)
	require.Contains(t, confirmation.Message, `group "image-2"`)
	completed, err := allowed.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "drawing.generate", Arguments: arguments, RequestState: pending.RequestState,
		InputResponses: mcp.InputResponseMap{"confirmation": &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirmed": true}}},
	})
	require.NoError(t, err)
	require.False(t, completed.IsError)
	require.EqualValues(t, 1, fixture.upstream.Load())
	var user model.User
	require.NoError(t, db.First(&user, fixture.user.Id).Error)
	require.Less(t, user.Quota, fixture.user.Quota)
	// The handler still revalidates the bearer on every call.
	require.NoError(t, integration.Core.Revoke(context.Background(), url.Values{"client_id": {service.OAuthPiClientID}, "token": {credentials.AccessToken}}.Encode()))
	_, err = allowed.CallTool(context.Background(), &mcp.CallToolParams{Name: "drawing.generate", Arguments: arguments})
	require.Error(t, err)
	require.EqualValues(t, 1, fixture.upstream.Load())
}

func TestDrawingMCPOAuthGrantMetadataFailsClosed(t *testing.T) {
	fixture := newDrawingParityFixture(t, common.GetTrustQuota(), http.StatusOK)
	require.NoError(t, fixture.db.AutoMigrate(&model.OpenSourceBountyMCPConfirmation{}, &model.OpenSourceBountyMCPOperation{}))
	relay := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("denied grant reached relay") })
	for name, extra := range map[string]map[string]any{
		"missing":           {"oauth": true},
		"empty":             {"oauth": true, "granted_groups": []string{}},
		"null":              {"oauth": true, "granted_groups": nil},
		"string":            {"oauth": true, "granted_groups": "image-2"},
		"untyped array":     {"oauth": true, "granted_groups": []any{"image-2"}},
		"empty member":      {"oauth": true, "granted_groups": []string{"image-2", ""}},
		"whitespace member": {"oauth": true, "granted_groups": []string{" image-2 "}},
		"control member":    {"oauth": true, "granted_groups": []string{"image-2", "bad\x00group"}},
		"oversized member":  {"oauth": true, "granted_groups": []string{strings.Repeat("x", 65)}},
		"auto member":       {"oauth": true, "granted_groups": []string{"auto"}},
		"malformed marker":  {"oauth": "true", "granted_groups": []string{"image-2"}},
	} {
		t.Run(name, func(t *testing.T) {
			for _, tool := range []string{"drawing.generate", "drawing.list_capabilities"} {
				args := map[string]any{}
				if tool == "drawing.generate" {
					args["prompt"], args["group"] = "local metadata fixture", "image-2"
				}
				result, err := callBuiltinToolMarketServer(context.Background(), newDrawingMCPServer(relay), &auth.TokenInfo{UserID: strconv.Itoa(fixture.user.Id), Extra: extra}, &mcp.CallToolParams{Name: tool, Arguments: args})
				require.NoError(t, err)
				require.True(t, result.IsError)
				require.Empty(t, result.InputRequests)
			}
		})
	}
	result, err := callBuiltinToolMarketServer(context.Background(), newDrawingMCPServer(relay), &auth.TokenInfo{UserID: strconv.Itoa(fixture.user.Id), Extra: map[string]any{
		"oauth": true, "granted_groups": []string{"default"},
	}}, &mcp.CallToolParams{Name: "drawing.generate", Arguments: map[string]any{"prompt": "chat-only grant must not default to drawing"}})
	require.NoError(t, err)
	require.True(t, result.IsError)
	var count int64
	require.NoError(t, fixture.db.Model(&model.OpenSourceBountyMCPConfirmation{}).Count(&count).Error)
	require.Zero(t, count)
	require.Zero(t, fixture.upstream.Load())
}
