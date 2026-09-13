package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type drawingMCPBearerTransport struct {
	token string
}

func (transport drawingMCPBearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+transport.token)
	return http.DefaultTransport.RoundTrip(clone)
}

func TestDrawingMCPAuthenticationDiscoveryAndConfirmation(t *testing.T) {
	db, user, bountyToken := setupOpenSourceBountyMCPControllerTest(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}, &model.DrawingMCPToken{}, &model.Ability{}, &model.Channel{}))
	key := &model.Token{UserId: user.Id, Key: "drawing-test-key", Group: model.DrawingTokenGroup, Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, db.Create(key).Error)
	token, _, err := model.RotateDrawingMCPToken(user.Id, key.Id)
	require.NoError(t, err)
	previousDrawingEnabled := common.DrawingEnabled
	common.DrawingEnabled = true
	t.Cleanup(func() { common.DrawingEnabled = previousDrawingEnabled })

	server := httptest.NewServer(NewDrawingMCPHandler())
	t.Cleanup(server.Close)

	for _, test := range []struct {
		name  string
		token string
	}{
		{name: "missing bearer"},
		{name: "invalid bearer", token: "lmm_mcp_invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(`{}`))
			require.NoError(t, err)
			request.Header.Set("Content-Type", "application/json")
			if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response, err := http.DefaultClient.Do(request)
			require.NoError(t, err)
			defer response.Body.Close()
			assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
		})
	}
	request, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(`{}`))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+bountyToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	response.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)

	client := mcp.NewClient(&mcp.Implementation{Name: "drawing-mcp-test", Version: "1.0.0"}, &mcp.ClientOptions{
		MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
	})
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             server.URL,
		HTTPClient:           &http.Client{Transport: drawingMCPBearerTransport{token: token}},
		DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	tools, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	assert.Len(t, tools.Tools, 2)
	names := []string{tools.Tools[0].Name, tools.Tools[1].Name}
	assert.ElementsMatch(t, []string{"drawing.list_capabilities", "drawing.generate"}, names)

	capabilities, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "drawing.list_capabilities", Arguments: map[string]any{},
	})
	require.NoError(t, err)
	assert.False(t, capabilities.IsError)

	invalid, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "drawing.generate", Arguments: map[string]any{"prompt": ""},
	})
	require.NoError(t, err)
	assert.True(t, invalid.IsError)
	invalidModel, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "drawing.generate", Arguments: map[string]any{"prompt": "draw a safe test image", "model": "not-an-image-model"},
	})
	require.NoError(t, err)
	assert.True(t, invalidModel.IsError, "an explicit model must not bypass the image catalog")
}

func TestDrawingMCPConfirmationRejectsForgeryWrongPayloadReplayAndDoubleSubmit(t *testing.T) {
	_, user, _ := setupOpenSourceBountyMCPControllerTest(t)
	payload := map[string]any{"prompt": "a real prompt", "group": "image-2", "model": "image-2", "n": 1}
	payloadHash, err := model.OpenSourceBountyMCPPayloadHash(payload)
	require.NoError(t, err)

	newOperation := func(t *testing.T) *model.OpenSourceBountyMCPConfirmedOperation {
		t.Helper()
		state, createErr := model.CreateOpenSourceBountyMCPConfirmation(user.Id, "drawing.generate", payloadHash)
		require.NoError(t, createErr)
		return &model.OpenSourceBountyMCPConfirmedOperation{
			State: state, ToolName: "drawing.generate", PayloadHash: payloadHash,
		}
	}

	forged := newOperation(t)
	forged.State = "mcp_confirm_forged"
	assert.Error(t, drawingMCPConsumeConfirmation(user.Id, forged))

	wrongPayload := newOperation(t)
	wrongPayload.PayloadHash = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	assert.Error(t, drawingMCPConsumeConfirmation(user.Id, wrongPayload))

	replayed := newOperation(t)
	require.NoError(t, drawingMCPConsumeConfirmation(user.Id, replayed))
	assert.Error(t, drawingMCPConsumeConfirmation(user.Id, replayed))

	concurrent := newOperation(t)
	results := make(chan error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	for range 2 {
		go func() {
			defer wait.Done()
			results <- drawingMCPConsumeConfirmation(user.Id, concurrent)
		}()
	}
	wait.Wait()
	close(results)
	successes := 0
	for consumeErr := range results {
		if consumeErr == nil {
			successes++
		}
	}
	assert.Equal(t, 1, successes)
}
