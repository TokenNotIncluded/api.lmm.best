// Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later
package controller

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneralBountyMCPSchemasExposeConditionalInputs(t *testing.T) {
	for _, name := range []string{"bounties.create_draft", "bounties.update_draft", "open_source_bounties.create_draft"} {
		schema := bountyMCPInputSchema(name)
		require.NotNil(t, schema)
		assert.Equal(t, []any{"general", "open_source"}, schema.Properties["kind"].Enum)
		assert.Equal(t, []any{"individual", "company"}, schema.Properties["publisher_type"].Enum)
		assert.NotContains(t, schema.Required, "repository_url")
		assert.Len(t, schema.AllOf, 2)
		assert.Contains(t, schema.AllOf[1].Then.Required, "repository_url")
		assert.Equal(t, 0.0, *schema.Properties["deadline_at"].Minimum)
		assert.Contains(t, schema.Required, "rules")
		assert.Equal(t, 100.0, *schema.Properties["reward_slots"].Maximum)
	}
	submit := bountyMCPInputSchema("bounties.submit")
	require.NotNil(t, submit)
	assert.Contains(t, submit.Required, "project_id")
	assert.NotContains(t, submit.Required, "issue_url")
	assert.Len(t, submit.AnyOf, 4)
	first, err := json.Marshal(submit)
	require.NoError(t, err)
	for range 20 {
		next, err := json.Marshal(bountyMCPInputSchema("bounties.submit"))
		require.NoError(t, err)
		assert.Equal(t, first, next, "catalog version must not depend on map iteration")
	}
	// No typed-nil interface may suppress SDK inference for the other tools.
	assert.Nil(t, bountyMCPTool("bounties.list", "List", "List", true, false, true).InputSchema)
}

func TestGeneralBountyMCPCreateAcceptAndSubmitWithoutGithub(t *testing.T) {
	db, owner, _ := setupOpenSourceBountyMCPControllerTest(t)
	ctx := context.Background()
	ownerIdentity := &auth.TokenInfo{UserID: strconv.Itoa(owner.Id)}
	result, err := CallBuiltinToolMarketTool(ctx, "open_source_bounties", ownerIdentity, &mcp.CallToolParams{
		Name: "bounties.create_draft", Arguments: map[string]any{
			"kind": "general", "publisher_type": "company", "title": "Create a reference guide",
			"description":  "Write a reference guide with clear examples and source references.",
			"rules":        "Deliver every agreed section and provide a repeatable review procedure.",
			"reward_quota": 1000, "reward_slots": 1,
		},
	})
	require.NoError(t, err)
	require.False(t, result.IsError, "%+v", result)
	var project model.OpenSourceBountyProject
	require.NoError(t, db.Where("owner_user_id = ?", owner.Id).First(&project).Error)
	assert.Equal(t, "general", project.Kind)
	assert.Empty(t, project.RepositoryUrl)
	_, _, err = model.PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	level := model.TrustLevelMinUser + 1
	participant := model.User{Username: "general-mcp-worker", Password: "password", AffCode: "general-mcp-worker", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, TrustLevelOverride: &level}
	require.NoError(t, db.Create(&participant).Error)
	identity := &auth.TokenInfo{UserID: strconv.Itoa(participant.Id)}
	for _, params := range []*mcp.CallToolParams{
		{Name: "bounties.accept", Arguments: map[string]any{"project_id": project.Id}},
		{Name: "bounties.submit", Arguments: map[string]any{"project_id": project.Id, "submission_note": "Completed all sections and verified the agreed examples in the reference guide."}},
	} {
		result, err = CallBuiltinToolMarketTool(ctx, "open_source_bounties", identity, params)
		require.NoError(t, err)
		require.False(t, result.IsError, "%+v", result)
	}
	var challenge model.OpenSourceBountyChallenge
	require.NoError(t, db.Where("project_id = ?", project.Id).First(&challenge).Error)
	assert.Equal(t, model.OpenSourceBountyChallengeSubmitted, challenge.Status)
	assert.Empty(t, challenge.GithubHandle)
	assert.Empty(t, challenge.IssueUrl)
	assert.Empty(t, challenge.PullRequestUrl)
	require.NoError(t, db.First(&participant, participant.Id).Error)
	assert.Zero(t, participant.Quota, "submission must never auto-pay a reward")
}
