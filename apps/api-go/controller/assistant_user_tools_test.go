package controller

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveAssistantUserTargetFiltersHigherRoleCandidates(t *testing.T) {
	db := setupManageUserTestDB(t)
	operator := &model.User{
		Username: "assistant-search-operator", Password: "password", AffCode: "assistant-search-operator-aff",
		Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default",
	}
	manageable := &model.User{
		Username: "shared-assistant-target-low", Password: "password", AffCode: "shared-assistant-target-low-aff",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default",
	}
	higher := &model.User{
		Username: "shared-assistant-target-high", Password: "password", AffCode: "shared-assistant-target-high-aff",
		Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default",
	}
	root := &model.User{
		Username: "assistant-search-root", Password: "password", AffCode: "assistant-search-root-aff",
		Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default",
	}
	for _, user := range []*model.User{operator, manageable, higher, root} {
		require.NoError(t, db.Create(user).Error)
	}

	// The substring matches both a permitted user and a root user.  The
	// lower-level administrator must receive only the permitted identity and
	// should not be forced to disambiguate an invisible match.
	target, result := resolveAssistantUserTarget(nil, operator.Id, map[string]any{
		"identifier": "shared-assistant-target",
	}, false)
	require.NotNil(t, target)
	require.Nil(t, result)
	assert.Equal(t, manageable.Id, target.User.Id)
	assert.Equal(t, manageable.Username, target.User.Username)

	// Root retains the legitimate ability to disambiguate across all roles.
	rootTarget, rootResult := resolveAssistantUserTarget(nil, root.Id, map[string]any{
		"identifier": "shared-assistant-target",
	}, false)
	require.Nil(t, rootTarget)
	assert.Equal(t, "target_ambiguous", rootResult["status"])
	rootCandidates, ok := rootResult["candidates"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, rootCandidates, 2)
	assert.Equal(t, manageable.Id, rootCandidates[0]["id"])
	assert.Equal(t, higher.Id, rootCandidates[1]["id"])
}

func TestAssistantSafeToolInputPreservesConversationTitle(t *testing.T) {
	input := assistantSafeToolInput(`{"title":"配置 API 密钥","secret":"must-not-leak"}`)

	assert.Equal(t, map[string]any{"title": "配置 API 密钥"}, input)
}

func TestAssistantMathToolTraceShowsSafeExpressionResultAndActionableErrors(t *testing.T) {
	success := buildAssistantToolTrace(assistantOpenAIToolCall{
		Function: assistantOpenAIToolCallFunction{
			Name:      "calculate_math",
			Arguments: `{"expression":"6 * 7","variables":{"secret":42}}`,
		},
	}, map[string]any{"ok": true, "result": float64(42)})
	require.NotNil(t, success.Result)
	assert.Equal(t, float64(42), *success.Result)
	assert.Equal(t, map[string]any{"expression": "6 * 7"}, success.Input)
	assert.Empty(t, success.ErrorCode)

	missing := buildAssistantToolTrace(assistantOpenAIToolCall{
		Function: assistantOpenAIToolCallFunction{Name: "calculate_math", Arguments: `{}`},
	}, map[string]any{"ok": false, "error": "a math expression is required"})
	assert.Equal(t, "output-error", missing.Status)
	assert.Equal(t, "missing_math_expression", missing.ErrorCode)
	assert.Nil(t, missing.Result)
}
