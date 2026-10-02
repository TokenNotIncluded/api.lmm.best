package controller

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltinToolMarketDefinitionsRetainRegisteredSchemas(t *testing.T) {
	ctx := context.Background()
	definitions, err := BuiltinToolMarketDefinitions(ctx)
	require.NoError(t, err)
	require.Len(t, definitions, 3)
	byKey := make(map[string]BuiltinToolMarketServiceDefinition)
	for _, definition := range definitions {
		byKey[definition.Key] = definition
		assert.NotEmpty(t, definition.Name)
		assert.NotEmpty(t, definition.Description)
		assert.NotEmpty(t, definition.Tools)
	}
	assert.Len(t, byKey["drawing"].Tools, 2)
	assert.Len(t, byKey["open_source_bounties"].Tools, 22)
	for _, legacy := range []struct {
		key    string
		server *mcp.Server
	}{
		{"drawing", newDrawingMCPServer(nil)},
		{"open_source_bounties", newOpenSourceBountyMCPServer()},
	} {
		tools, err := listBuiltinToolMarketServerTools(ctx, legacy.server)
		require.NoError(t, err)
		assert.Equal(t, tools, byKey[legacy.key].Tools, "market schemas must be the original SDK schemas")
	}
	for _, tool := range byKey["drawing"].Tools {
		if tool.Name != "drawing.generate" {
			continue
		}
		schema, ok := tool.InputSchema.(map[string]any)
		require.True(t, ok)
		assert.Contains(t, schema["required"], "prompt")
		assert.NotContains(t, schema["required"], "model")
		assert.True(t, *tool.Annotations.DestructiveHint)
	}
	byKey["drawing"].Tools[0].Description = "untrusted edit"
	byKey["drawing"].Tools[0].InputSchema.(map[string]any)["type"] = "invalid"
	fresh, err := BuiltinToolMarketDefinitions(ctx)
	require.NoError(t, err)
	assert.NotEqual(t, "untrusted edit", fresh[0].Tools[0].Description)
	assert.Equal(t, "object", fresh[0].Tools[0].InputSchema.(map[string]any)["type"])
}

func TestBuiltinToolMarketSDKValidationPrecedesTheHandler(t *testing.T) {
	for _, args := range []any{
		map[string]any{},
		map[string]any{"project_id": "invalid"},
	} {
		result, err := CallBuiltinToolMarketTool(context.Background(), "open_source_bounties", &auth.TokenInfo{UserID: "17"}, &mcp.CallToolParams{
			Name: "open_source_bounties.publish", Arguments: args,
		})
		require.NoError(t, err)
		require.True(t, result.IsError, "invalid schema input must not reach a DB-backed handler")
		require.NotEmpty(t, result.Content)
		message, ok := result.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, message.Text, "validating")
	}
	_, err := CallBuiltinToolMarketTool(context.Background(), "untrusted", &auth.TokenInfo{UserID: "17"}, &mcp.CallToolParams{Name: "open_source_bounties.list"})
	assert.ErrorContains(t, err, "unknown built-in")
	_, err = CallBuiltinToolMarketTool(context.Background(), "open_source_bounties", nil, &mcp.CallToolParams{Name: "open_source_bounties.list"})
	assert.ErrorContains(t, err, "authentication context is missing")
}

func TestBuiltinToolMarketTrustedIdentityIsIsolatedFromArgumentsAndOtherCalls(t *testing.T) {
	type input struct {
		UserID string `json:"user_id,omitempty"`
	}
	type output struct {
		UserID string `json:"user_id"`
	}
	var wait sync.WaitGroup
	for index := range 12 {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			server := mcp.NewServer(&mcp.Implementation{Name: "identity-test", Version: "1"}, nil)
			mcp.AddTool(server, &mcp.Tool{Name: "identity.read"}, func(ctx context.Context, request *mcp.CallToolRequest, _ input) (*mcp.CallToolResult, output, error) {
				if request.Extra == nil || request.Extra.TokenInfo == nil {
					return nil, output{}, fmt.Errorf("missing identity")
				}
				if request.Extra.TokenInfo.Extra["market_builtin"] != true {
					return nil, output{}, fmt.Errorf("missing trusted execution marker")
				}
				return nil, output{UserID: request.Extra.TokenInfo.UserID}, nil
			})
			identity := &auth.TokenInfo{UserID: strconv.Itoa(index + 1), Extra: map[string]any{"original": true}}
			params := &mcp.CallToolParams{Name: "identity.read", Arguments: map[string]any{"user_id": "999"}, Meta: mcp.Meta{"user_id": "999"}}
			result, err := callBuiltinToolMarketServer(context.Background(), server, identity, params)
			if !assert.NoError(t, err) || !assert.False(t, result.IsError) {
				return
			}
			assert.Equal(t, identity.UserID, result.StructuredContent.(map[string]any)["user_id"])
			assert.NotContains(t, identity.Extra, "market_builtin")
			assert.Equal(t, mcp.Meta{"user_id": "999"}, params.Meta)
		}(index)
	}
	wait.Wait()
}

func TestBuiltinToolMarketBountyConfirmationKeepsRealBillingAndReplayProtection(t *testing.T) {
	db, user, _ := setupOpenSourceBountyMCPControllerTest(t)
	project, err := model.CreateOpenSourceBountyDraft(user.Id, model.OpenSourceBountyDraftInput{
		RepositoryUrl: "https://github.com/example/builtin-market", Title: "Fix reproducible market defects",
		Description: "Reproduce a genuine defect and provide a focused fix with verification.",
		Rules:       "Provide reproduction, expected behavior, actual behavior, impact, and verification.",
		RewardQuota: 333, RewardSlots: 3,
	})
	require.NoError(t, err)
	identity := &auth.TokenInfo{UserID: strconv.Itoa(user.Id)}
	args := map[string]any{"project_id": project.Id}
	params := &mcp.CallToolParams{Name: "open_source_bounties.publish", Arguments: args}
	first, err := CallBuiltinToolMarketTool(context.Background(), "open_source_bounties", identity, params)
	require.NoError(t, err)
	require.False(t, first.IsError)
	require.True(t, first.NeedsInput())
	require.NotEmpty(t, first.RequestState)
	confirmation, ok := first.InputRequests["confirmation"].(*mcp.ElicitParams)
	require.True(t, ok)
	assert.Contains(t, confirmation.Message, "999")
	var balance model.User
	require.NoError(t, db.First(&balance, user.Id).Error)
	assert.Equal(t, 10_000, balance.Quota, "asking for confirmation is free and does not fund a bounty")
	assert.Empty(t, params.RequestState, "dispatch must not mutate caller params")

	confirmed := &mcp.CallToolParams{
		Name: "open_source_bounties.publish", Arguments: args, RequestState: first.RequestState,
		InputResponses: mcp.InputResponseMap{"confirmation": &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirmed": true}}},
	}
	wrongUser := &auth.TokenInfo{UserID: strconv.Itoa(user.Id + 1)}
	wrong, err := CallBuiltinToolMarketTool(context.Background(), "open_source_bounties", wrongUser, confirmed)
	require.NoError(t, err)
	assert.True(t, wrong.IsError, "confirmation state must remain bound to its authenticated user")
	second, err := CallBuiltinToolMarketTool(context.Background(), "open_source_bounties", identity, confirmed)
	require.NoError(t, err)
	require.False(t, second.IsError)
	require.False(t, second.NeedsInput())
	require.NoError(t, db.First(&balance, user.Id).Error)
	assert.Equal(t, 9_001, balance.Quota, "free dispatch must retain the bounty's real funding debit")
	replay, err := CallBuiltinToolMarketTool(context.Background(), "open_source_bounties", identity, confirmed)
	require.NoError(t, err)
	require.False(t, replay.IsError)
	require.NoError(t, db.First(&balance, user.Id).Error)
	assert.Equal(t, 9_001, balance.Quota, "confirmed replay must not fund the bounty again")
}

func TestBuiltinToolMarketCancellationReachesTheHandler(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	server := mcp.NewServer(&mcp.Implementation{Name: "cancellation-test", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "pending.call"}, func(ctx context.Context, request *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		close(started)
		select {
		case <-ctx.Done():
			close(cancelled)
			return nil, nil, ctx.Err()
		case <-stop:
			return nil, nil, fmt.Errorf("test cleanup")
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		_, err := callBuiltinToolMarketServer(ctx, server, &auth.TokenInfo{UserID: "17"}, &mcp.CallToolParams{Name: "pending.call"})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("built-in handler did not start")
	}
	cancel()
	select {
	case err := <-done:
		assert.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("built-in call did not finish after cancellation")
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("built-in handler did not receive request cancellation")
	}
}

func TestBuiltinToolMarketDrawingIdentityAllowsUnboundMarketAccountOnly(t *testing.T) {
	for _, test := range []struct {
		name  string
		extra map[string]any
		keyID int
		err   bool
	}{
		{name: "market account", extra: map[string]any{"market_builtin": true}},
		{name: "market bound key", extra: map[string]any{"market_builtin": true, "api_key_id": 23}, keyID: 23},
		{name: "invalid market bound key", extra: map[string]any{"market_builtin": true, "api_key_id": 0}, err: true},
		{name: "old endpoint without key", extra: map[string]any{}, err: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			id, err := drawingMCPAPIKeyID(&mcp.CallToolRequest{Extra: &mcp.RequestExtra{TokenInfo: &auth.TokenInfo{UserID: "17", Extra: test.extra}}})
			if test.err {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, test.keyID, id)
			}
		})
	}
}
