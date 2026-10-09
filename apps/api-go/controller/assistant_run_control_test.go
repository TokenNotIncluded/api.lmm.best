// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package controller

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func assistantControlCall(name, arguments string) assistantOpenAIToolCall {
	return assistantOpenAIToolCall{ID: "control", Type: "function", Function: assistantOpenAIToolCallFunction{Name: name, Arguments: arguments}}
}

func TestAssistantEfficiencyLoadsOnlyRequestedAndRequiredDefinitions(t *testing.T) {
	assistantPolicyForTest(t, nil, nil)
	c, _ := assistantLoopTestContext(t)
	context := assistantUserContextFromGin(c)
	available := assistantToolDefinitionsForContext(context)
	initial, directory := assistantToolsForAgentStep(c, available, "")
	require.Len(t, initial, 5)
	assert.Contains(t, directory, "get_store_product")
	assert.NotContains(t, directory, "prepare_admin_config_change")
	assert.False(t, assistantPolicyTestContainsTool(initial, "get_store_product"))

	result := executeAssistantTool(c, assistantControlCall("discover_tools", `{"names":["get_store_products","get_store_product"]}`))
	require.Equal(t, true, result["ok"], result)
	assert.Equal(t, 2, result["loaded_count"])
	assert.NotContains(t, result, "parameters", "do not echo definitions in tool results")
	selected, _ := assistantToolsForAgentStep(c, available, "get_model_pricing")
	assert.Len(t, selected, 8)
	for _, name := range []string{"get_store_products", "get_store_product", "get_model_pricing"} {
		assert.True(t, assistantPolicyTestContainsTool(selected, name), name)
	}
	other, _ := assistantLoopTestContext(t)
	otherTools, _ := assistantToolsForAgentStep(other, available, "")
	assert.False(t, assistantPolicyTestContainsTool(otherTools, "get_store_product"), "selection must not cross requests")
	result = executeAssistantTool(c, assistantControlCall("discover_tools", `{"names":["get_store_products"]}`))
	assert.Equal(t, 0, result["loaded_count"])
	assert.False(t, assistantToolMadeProgress("discover_tools", result))
}

func TestAssistantEfficiencyDiscoveryChecksPolicyAndMalformedInput(t *testing.T) {
	assistantPolicyForTest(t, nil, nil)
	for _, input := range []string{`null`, `{}`, `{"names":null}`, `{"names":[]}`, `{"names":[true]}`, `{"names":["calculate_math","calculate_math"]}`, `{"names":["calculate_math"],"extra":true}`, `{"names":["get_admin_server_config"]}`, `{"names":["unknown"]}`} {
		c, _ := assistantLoopTestContext(t)
		result := executeAssistantTool(c, assistantControlCall("discover_tools", input))
		assert.Equal(t, false, result["ok"], input)
		_, loaded := c.Get(assistantLoadedToolsKey)
		assert.False(t, loaded, "invalid requests must not partly load tools")
	}
	c, _ := assistantLoopTestContext(t)
	result := executeAssistantTool(c, assistantControlCall("discover_tools", `{"names":["get_store_products"]}`))
	require.Equal(t, true, result["ok"])
	assistantPolicyForTest(t, nil, map[string]bool{"get_store_products": false})
	available := assistantToolDefinitionsForContext(assistantUserContextFromGin(c))
	selected, directory := assistantToolsForAgentStep(c, available, "get_store_products")
	assert.False(t, assistantPolicyTestContainsTool(selected, "get_store_products"))
	assert.NotContains(t, directory, "get_store_products")
	assert.Equal(t, false, executeAssistantTool(c, assistantControlCall("discover_tools", `{"names":["get_store_products"]}`))["ok"])

	assistantPolicyForTest(t, nil, map[string]bool{"discover_tools": false})
	available = assistantToolDefinitionsForContext(assistantUserContextFromGin(c))
	selected, directory = assistantToolsForAgentStep(c, available, "")
	assert.Equal(t, available, selected, "disabled discovery must not remove other tools")
	assert.Empty(t, directory)
}

func TestAssistantEfficiencyMeasuresDefinitionBytesForEachRole(t *testing.T) {
	assistantPolicyForTest(t, nil, nil)
	for _, context := range []assistantUserContext{
		{AccessLevel: "L0"},
		{AccessLevel: "L1", DeveloperAccessGranted: true},
		{AccessLevel: "ADMIN", AdministratorMode: true},
		{AccessLevel: "ROOT", AdministratorMode: true},
	} {
		c, _ := assistantLoopTestContext(t)
		c.Set(assistantUserContextKey, context)
		available := assistantToolDefinitionsForContext(context)
		selected, directory := assistantToolsForAgentStep(c, available, "")
		full, err := json.Marshal(available)
		require.NoError(t, err)
		reduced, err := json.Marshal(selected)
		require.NoError(t, err)
		manifest, err := json.Marshal(directory)
		require.NoError(t, err)
		bytes := len(reduced) + len(manifest)
		assert.Less(t, bytes, len(full)/2, context.AccessLevel)
		t.Logf("%s: full definitions=%d bytes, initial definitions plus directory=%d bytes (not a token or billing measurement)", context.AccessLevel, len(full), bytes)
	}
}

func TestAssistantEfficiencyClassifiesEveryRegisteredToolExplicitly(t *testing.T) {
	for _, group := range setting.AssistantToolCatalogue() {
		for _, info := range group.Tools {
			require.Contains(t, []string{"read_only", "navigation", "confirmation", "server_guarded"}, setting.AssistantToolEffect(info.Name), info.Name)
			if info.Name == "execute_admin_operation" {
				continue
			} // Operation-specific live check.
			assert.Equal(t, info.Effect == "read_only" || info.Effect == "navigation", assistantToolCallReadOnly(nil, assistantControlCall(info.Name, `{}`)), info.Name)
		}
	}
	for _, fake := range []string{"get_and_delete", "list_and_charge", "calculate_and_write"} {
		assert.False(t, assistantToolCallReadOnly(nil, assistantControlCall(fake, `{}`)))
	}
}

func TestAssistantEfficiencyVisualizationKeepsBrowserDataWithoutModelEcho(t *testing.T) {
	visual := &assistantVisualization{Kind: "chart", Title: "Measured usage", Labels: make([]string, 50), Series: []assistantVisualSeries{{Name: "requests", Values: make([]float64, 50)}}}
	for index := range visual.Labels {
		visual.Labels[index] = strings.Repeat("sample", 5)
	}
	result := map[string]any{"ok": true, "visualization": visual}
	modelResult := assistantModelToolResultJSON("show_chart", result)
	assert.NotContains(t, string(modelResult), "labels")
	assert.Contains(t, string(modelResult), `"displayed":true`)
	assert.Less(t, len(modelResult), len(assistantAgentToolResultJSON(result))/2)
	trace := buildAssistantToolTrace(assistantControlCall("show_chart", `{}`), result)
	assert.Same(t, visual, trace.Visualization)
	failed := map[string]any{"ok": false, "status": "invalid_visualization"}
	assert.Equal(t, assistantAgentToolResultJSON(failed), assistantModelToolResultJSON("show_chart", failed))
}

func TestAssistantEndStopsLaterToolsWithoutAnotherModelTurn(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[stream], func(t *testing.T) {
			assistantPolicyForTest(t, nil, nil)
			c, recorder := assistantLoopTestContext(t)
			var session *assistantStreamSession
			if stream {
				session = newAssistantStreamSession(c.Writer)
				require.NoError(t, session.start())
				require.NoError(t, session.appendContent("Tentative response"))
				c.Set(assistantStreamSessionKey, session)
			}
			turns := 0
			original := relayAssistantAgentTurn
			relayAssistantAgentTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
				turns++
				assert.True(t, assistantPolicyTestContainsTool(request.Tools, "end_conversation"))
				return http.StatusOK, assistantLoopCallBody(t, []assistantOpenAIToolCall{
					assistantLoopMathCall("before", 1),
					assistantControlCall("end_conversation", `{"reason":"completed","message":"结果是 2。本轮已结束。"}`),
					assistantControlCall("remember_memory", `{"content":"This must not be stored"}`),
				}, "Tentative response"), nil
			}
			t.Cleanup(func() { relayAssistantAgentTurn = original })
			runAssistantAgent(c, setting.AssistantSettings{AgentLoopEnabled: true, MaxSteps: 8}, []assistantOpenAIMessage{{Role: "user", Content: "Finish the task"}})
			assert.Equal(t, 1, turns)
			assert.Equal(t, http.StatusOK, recorder.Code)
			assert.Contains(t, recorder.Body.String(), "结果是 2。本轮已结束。")
			assert.NotContains(t, recorder.Body.String(), "remember_memory")
			assert.NotContains(t, recorder.Body.String(), "This must not be stored")
			assert.False(t, c.GetBool("assistant_conversation_restricted"))
			assert.Empty(t, recorder.Header().Get("X-LMM-Assistant-Cache"))
			if stream {
				assert.Equal(t, "结果是 2。本轮已结束。", session.safeContent())
				assert.Equal(t, 1, strings.Count(recorder.Body.String(), "event: done"))
			}
		})
	}
}

func TestAssistantEndRejectsInvalidArgumentsAndDisabledPolicy(t *testing.T) {
	assistantPolicyForTest(t, nil, nil)
	for _, input := range []string{`null`, `{}`, `{"reason":"completed"}`, `{"reason":"ban","message":"Stop"}`, `{"reason":"completed","message":" "}`, `{"reason":"completed","message":"Stop","user_id":42}`, `{"reason":"completed","message":true}`, `{"reason":"completed","message":"` + strings.Repeat("界", 2001) + `"}`} {
		c, _ := assistantLoopTestContext(t)
		result := executeAssistantTool(c, assistantControlCall("end_conversation", input))
		assert.Equal(t, false, result["ok"], input)
		assert.False(t, finishAssistantConversationEnd(c, setting.AssistantSettings{}))
	}
	assistantPolicyForTest(t, nil, map[string]bool{"end_conversation": false})
	c, _ := assistantLoopTestContext(t)
	assert.Equal(t, "tool_disabled", executeAssistantTool(c, assistantControlCall("end_conversation", `{"reason":"user_requested","message":"Stopped."}`))["status"])
	assert.False(t, finishAssistantConversationEnd(c, setting.AssistantSettings{}))
}

func TestAssistantEndSavesHistoryWithoutRestrictingOrArchiving(t *testing.T) {
	assistantPolicyForTest(t, nil, nil)
	db := setupTokenControllerTestDB(t)
	owner := model.User{Id: 42, Username: "end-history", Password: "password", AffCode: "end-history"}
	require.NoError(t, db.Create(&owner).Error)
	conversation, err := model.PrepareAssistantConversation(owner.Id, 0, "End politely")
	require.NoError(t, err)
	c, _ := assistantLoopTestContext(t)
	c.Set("id", owner.Id)
	c.Set(assistantActorUserIDKey, owner.Id)
	c.Set("assistant_history_conversation_id", conversation.Id)
	c.Set("assistant_history_latest_message", "结束")
	c.Set(assistantUserContextKey, assistantUserContext{UserID: owner.Id, AccessLevel: "L0"})
	session := newAssistantStreamSession(c.Writer)
	require.NoError(t, session.start())
	c.Set(assistantStreamSessionKey, session)
	c.Set(assistantConversationEndKey, assistantConversationEnd{Message: "本轮已结束。", Reason: "user_requested"})
	require.True(t, finishAssistantConversationEnd(c, setting.AssistantSettings{}))
	var stored model.AssistantConversation
	require.NoError(t, db.First(&stored, conversation.Id).Error)
	assert.Zero(t, stored.ArchivedAt)
	assert.Zero(t, stored.RestrictedAt)
	var messages []model.AssistantHistoryMessage
	require.NoError(t, db.Where("conversation_id = ?", conversation.Id).Find(&messages).Error)
	require.Len(t, messages, 2)
	assert.Equal(t, "本轮已结束。", messages[1].Content)
	// A later user message can continue the same saved conversation.
	_, err = model.PrepareAssistantConversation(owner.Id, conversation.Id, "继续")
	require.NoError(t, err)
}

func TestAssistantEndCannotReplaceARequiredTool(t *testing.T) {
	assistantPolicyForTest(t, nil, nil)
	c, recorder := assistantLoopTestContext(t)
	context := assistantUserContext{AccessLevel: "L0", Intent: model.AssistantIntentMath, LatestUserRequest: "What is 2 + 2?"}
	c.Set(assistantUserContextKey, context)
	require.Equal(t, "calculate_math", assistantNamedToolChoiceName(assistantToolChoiceForContext(context)))
	original := relayAssistantAgentTurn
	relayAssistantAgentTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
		assert.True(t, assistantPolicyTestContainsTool(request.Tools, "calculate_math"))
		return http.StatusOK, assistantLoopCallBody(t, []assistantOpenAIToolCall{assistantControlCall("end_conversation", `{"reason":"completed","message":"Unverified answer"}`)}, ""), nil
	}
	t.Cleanup(func() { relayAssistantAgentTurn = original })
	runAssistantAgent(c, setting.AssistantSettings{AgentLoopEnabled: true, MaxSteps: 3}, []assistantOpenAIMessage{{Role: "user", Content: context.LatestUserRequest}})
	assert.Equal(t, http.StatusBadGateway, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "ASSISTANT_REQUIRED_TOOL_MISSING")
	_, ended := c.Get(assistantConversationEndKey)
	assert.False(t, ended)
}

func TestAssistantEfficiencyDiscoveryUpdatesNextRequestOnly(t *testing.T) {
	assistantPolicyForTest(t, nil, nil)
	c, recorder := assistantLoopTestContext(t)
	turns := 0
	original := relayAssistantAgentTurn
	relayAssistantAgentTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
		turns++
		if turns == 1 {
			assert.False(t, assistantPolicyTestContainsTool(request.Tools, "get_store_products"))
			return http.StatusOK, assistantLoopCallBody(t, []assistantOpenAIToolCall{assistantControlCall("discover_tools", `{"names":["get_store_products"]}`)}, ""), nil
		}
		assert.True(t, assistantPolicyTestContainsTool(request.Tools, "get_store_products"))
		assert.Equal(t, 1, strings.Count(request.Messages[0].Content, "Available tool directory (names only; not permissions)."))
		assert.NotContains(t, request.Messages[len(request.Messages)-1].Content, "parameters")
		return http.StatusOK, assistantLoopCallBody(t, nil, "Only the definitions were loaded."), nil
	}
	t.Cleanup(func() { relayAssistantAgentTurn = original })
	runAssistantAgent(c, setting.AssistantSettings{AgentLoopEnabled: true, MaxSteps: 4}, []assistantOpenAIMessage{{Role: "user", Content: "Explain available features"}})
	assert.Equal(t, 2, turns)
	assert.Equal(t, http.StatusOK, recorder.Code)
}
