package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpenSourceBountyMCPConfirmationOnlyAfterOwnershipCheck proves that
// ownership-scoped confirmed tools reject an unrelated caller BEFORE minting a
// confirmation: no confirmation row may appear and no confirmation form may be
// returned, while the legitimate owner keeps the normal confirmation flow.
func TestOpenSourceBountyMCPConfirmationOnlyAfterOwnershipCheck(t *testing.T) {
	db, user, token := setupOpenSourceBountyMCPControllerTest(t)

	// The contributor who accepts the challenge.
	participant := model.User{Username: "mcp-participant", Password: "password", AffCode: "mcp-participant", Quota: 0, Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&participant).Error)

	// An unrelated L1 user: owns nothing, participates in nothing, but holds a
	// valid developer-access MCP token and can reach the tools directly.
	levelOne := model.TrustLevelMinUser + 1
	outsider := model.User{Username: "mcp-outsider", Password: "password", AffCode: "mcp-outsider", Quota: 0, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, TrustLevelOverride: &levelOne}
	require.NoError(t, db.Create(&outsider).Error)

	project, err := model.CreateOpenSourceBountyDraft(user.Id, model.OpenSourceBountyDraftInput{
		RepositoryUrl: "https://github.com/example/mcp-confirmation-order", Title: "MCP confirmation ordering",
		Description: "Confirmation must be minted only after ownership is established.",
		Rules:       "The Issue must include reproduction, expected behavior, actual behavior, impact, and the linked pull request must include verification.",
		RewardQuota: 200, RewardSlots: 1,
	})
	require.NoError(t, err)
	// Publish the draft through the real funding path so the challenge
	// lifecycle below (accept -> submit -> review) stays realistic.
	publishedProject, _, err := model.PublishOpenSourceBounty(user.Id, project.Id)
	require.NoError(t, err)

	challenge, err := model.AcceptOpenSourceBounty(participant.Id, publishedProject.Id, "mcp-participant")
	require.NoError(t, err)

	server := httptest.NewServer(NewOpenSourceBountyMCPHandler())
	t.Cleanup(server.Close)

	ctx := context.Background()
	outsideToken, _, err := model.RotateOpenSourceBountyMCPToken(outsider.Id)
	require.NoError(t, err)
	outsideClient := mcp.NewClient(&mcp.Implementation{Name: "mcp-outside-client", Version: "1.0.0"}, &mcp.ClientOptions{
		MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
	})
	outsideSession, err := outsideClient.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             server.URL,
		HTTPClient:           &http.Client{Transport: openSourceBountyBearerTransport{token: outsideToken}},
		DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = outsideSession.Close() })

	ownerClient := mcp.NewClient(&mcp.Implementation{Name: "mcp-owner-client", Version: "1.0.0"}, &mcp.ClientOptions{
		MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
	})
	ownerSession, err := ownerClient.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             server.URL,
		HTTPClient:           &http.Client{Transport: openSourceBountyBearerTransport{token: token}},
		DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ownerSession.Close() })

	// Submit the challenge so reject/rate/withdraw all see a realistic state.
	challenge, err = model.SubmitOpenSourceBountyChallenge(participant.Id, publishedProject.Id,
		"https://github.com/example/mcp-confirmation-order/issues/1",
		"https://github.com/example/mcp-confirmation-order/pull/2",
		"Confirmation ordering verification.")
	require.NoError(t, err)

	for _, tc := range []struct {
		name      string
		tool      string
		arguments map[string]any
	}{
		{
			name:      "reject",
			tool:      "open_source_bounties.reject",
			arguments: map[string]any{"challenge_id": challenge.Id, "review_note": "rejecting outsider test", "rating_score": 1, "rating_comment": "does not reproduce"},
		},
		{
			name:      "withdraw",
			tool:      "open_source_bounties.withdraw",
			arguments: map[string]any{"challenge_id": challenge.Id},
		},
		{
			name:      "rate_owner",
			tool:      "open_source_bounties.rate_owner",
			arguments: map[string]any{"challenge_id": challenge.Id, "score": 5, "comment": "good publisher"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := outsideSession.CallTool(ctx, &mcp.CallToolParams{Name: tc.tool, Arguments: tc.arguments})
			require.NoError(t, err)
			assert.False(t, result.NeedsInput(), "the unrelated caller must not receive a confirmation form")
			assert.True(t, result.IsError, "the unrelated caller must be rejected before confirmation")

			var confirmations int64
			require.NoError(t, db.Model(&model.OpenSourceBountyMCPConfirmation{}).
				Where("user_id = ?", outsider.Id).Count(&confirmations).Error)
			assert.Zero(t, confirmations, "no confirmation row may be minted for the unrelated caller")
		})
	}

	// The legitimate challenge state is unchanged by the rejected outsider.
	var untouched model.OpenSourceBountyChallenge
	require.NoError(t, db.First(&untouched, challenge.Id).Error)
	assert.Equal(t, model.OpenSourceBountyChallengeSubmitted, untouched.Status)

	// The actual owner still gets the normal confirmation flow for reject.
	ownerResult, err := ownerSession.CallTool(ctx, &mcp.CallToolParams{Name: "open_source_bounties.reject", Arguments: map[string]any{
		"challenge_id": challenge.Id, "review_note": "not accepted", "rating_score": 2, "rating_comment": "not a fix",
	}})
	require.NoError(t, err)
	require.True(t, ownerResult.NeedsInput(), "the project owner must still receive the confirmation form")
	require.NotEmpty(t, ownerResult.RequestState)

	// The owner's legitimate confirmation row exists exactly once.
	var ownerConfirmations int64
	require.NoError(t, db.Model(&model.OpenSourceBountyMCPConfirmation{}).
		Where("user_id = ? AND tool_name = ?", user.Id, "open_source_bounties.reject").Count(&ownerConfirmations).Error)
	assert.Equal(t, int64(1), ownerConfirmations, "the owner's confirmable action mints exactly one confirmation row")
}
