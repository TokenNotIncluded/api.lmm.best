package controller

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAssistantAccountProgressDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.TopUp{}, &model.DeveloperAccessRequest{}, &model.L1OnboardingTodo{},
		&model.OpenSourceBountyChallenge{}, &model.AssistantNewUserGift{}, &model.UserOAuthBinding{},
	))
	return db
}

func assistantAccountProgressUser(t *testing.T, db *gorm.DB, l1 bool) *model.User {
	t.Helper()
	user := &model.User{Username: "account-progress", AffCode: "account-progress-aff", Password: "password",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	if l1 {
		level := model.TrustLevelMinUser + 1
		user.TrustLevelOverride = &level
	}
	require.NoError(t, db.Create(user).Error)
	return user
}

func assistantAccountMainStep(t *testing.T, result map[string]any, id string) map[string]any {
	t.Helper()
	steps, ok := result["main_task"].([]map[string]any)
	require.True(t, ok)
	for _, step := range steps {
		if step["id"] == id {
			return step
		}
	}
	t.Fatalf("main task step %q is missing", id)
	return nil
}

func TestAssistantAccountAccessL0HistoricalPendingDoesNotRequireRetiredReview(t *testing.T) {
	db := setupAssistantAccountProgressDB(t)
	user := assistantAccountProgressUser(t, db, false)
	withoutRecord := executeAssistantAccountTool(user.Id)
	require.Equal(t, true, withoutRecord["ok"])
	assert.Equal(t, "L0", withoutRecord["access_level"])
	assert.Contains(t, withoutRecord["next_step"], "get_registration_risk")
	assert.Contains(t, withoutRecord["next_step"], "grant_l1_access")
	assert.NotContains(t, withoutRecord, "onboarding_todo")

	request := model.DeveloperAccessRequest{UserId: user.Id, Status: model.DeveloperAccessRequestPending,
		Source: "legacy", Reason: "Use Codex for Python development", AIRecommendation: "A historical recommendation.", CreatedAt: 100}
	require.NoError(t, db.Create(&request).Error)
	withRecord := executeAssistantAccountTool(user.Id)
	require.Equal(t, true, withRecord["ok"])
	assert.Equal(t, withoutRecord["next_step"], withRecord["next_step"])
	assert.NotContains(t, withRecord["next_step"], "pending administrator review")
	assert.NotContains(t, withRecord["next_step"], "prepare an L1 recommendation")
	assert.Equal(t, true, withRecord["l1_request"].(map[string]any)["historical_read_only"])
	workflow := withRecord["registration_workflow"].(map[string]any)
	assert.Equal(t, false, workflow["recommendation_required"])
	assert.Equal(t, model.AssistantDirectGrantMinCompletedTurns, workflow["minimum_completed_turns"])

	var stored model.DeveloperAccessRequest
	require.NoError(t, db.First(&stored, request.Id).Error)
	assert.Equal(t, request, stored, "reading progress must preserve the historical record")
	denial, blocked := assistantDeveloperCapabilityRequired(user.Id, "usage statistics")
	require.True(t, blocked)
	assert.Equal(t, assistantL0AccessNextStep, denial["next_step"])
}

func TestAssistantAccountAccessSeparatesAPIActivityFromClientProof(t *testing.T) {
	for _, oauth := range []bool{false, true} {
		name := "manual_key"
		if oauth {
			name = "oauth_credential"
		}
		t.Run(name, func(t *testing.T) {
			db := setupAssistantAccountProgressDB(t)
			user := assistantAccountProgressUser(t, db, true)
			require.NoError(t, db.Model(user).Update("last_api_activity_at", 500).Error)
			token := model.Token{UserId: user.Id, Key: "private-progress-key", Status: common.TokenStatusEnabled, Group: "default", OAuthManaged: oauth}
			require.NoError(t, db.Create(&token).Error)
			c, _ := gin.CreateTestContext(nil)
			c.Set(assistantActorUserIDKey, user.Id)
			c.Set("id", user.Id+1000) // A relay billing identity must not replace the signed-in actor.
			result := executeAssistantTool(c, assistantOpenAIToolCall{Function: assistantOpenAIToolCallFunction{Name: "get_account_access", Arguments: `{}`}})
			require.Equal(t, true, result["ok"])
			state := result["onboarding"].(model.OnboardingState)
			assert.True(t, state.CredentialComplete)
			assert.True(t, state.FirstRequestComplete)
			assert.Equal(t, !oauth, state.APIKeyCreated)
			todo := result["onboarding_todo"].(*model.L1OnboardingTodoView)
			assert.Equal(t, model.L1OnboardingStatusInProgress, todo.Status)
			assert.Equal(t, model.L1OnboardingStepInstallClient, todo.CurrentStep)
			assert.Equal(t, "pending", assistantAccountMainStep(t, result, "install_client")["status"])
			assert.Equal(t, "pending", assistantAccountMainStep(t, result, "first_api_call")["status"])
			legacy := assistantAccountMainStep(t, result, "get_recommendation")
			assert.Equal(t, true, legacy["historical_read_only"])
			assert.Equal(t, false, legacy["required_for_l1_access"])
			assert.Equal(t, false, legacy["required_for_main_task"])
			assert.Equal(t, int64(500), result["last_api_activity_at"])
			assert.NotContains(t, result, "last_successful_api_call_at")
			assert.Contains(t, result["onboarding_evidence_note"], "can include billed failed requests")
			assert.Contains(t, result["next_step"], "successful API call alone cannot complete install_client")
			assert.Contains(t, result["next_step"], "reuse the existing credential")
			assert.NotContains(t, result["next_step"], "Setup is complete")
			proof := result["client_proof"].(map[string]any)
			assert.Equal(t, "/api/onboarding/todo/proof", proof["path"])
			assert.Equal(t, model.L1OnboardingStepInstallClient, proof["install_client"].(map[string]any)["request_step"])
			assert.Equal(t, model.L1OnboardingProofInstallClient, proof["install_client"].(map[string]any)["proof_type"])
			assert.Equal(t, []string{"step", "client", "base_url", "group"}, proof["configure_client"].(map[string]any)["required_fields"])
			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), token.Key)
		})
	}
}

func TestAssistantAccountAccessReadsCompletedProofsAndActivityOrdering(t *testing.T) {
	db := setupAssistantAccountProgressDB(t)
	user := assistantAccountProgressUser(t, db, true)
	token := model.Token{UserId: user.Id, Key: "proof-order-key", Status: common.TokenStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(&token).Error)
	_, err := model.ApplyL1OnboardingProof(user.Id, token.Id, model.L1OnboardingProof{Step: model.L1OnboardingStepInstallClient, Client: "Codex"}, 1000)
	require.NoError(t, err)
	installed := executeAssistantAccountTool(user.Id)
	require.Equal(t, true, installed["ok"])
	assert.Equal(t, "completed", assistantAccountMainStep(t, installed, "install_client")["status"])
	assert.Equal(t, model.L1OnboardingStepConfigureClient, installed["onboarding_todo"].(*model.L1OnboardingTodoView).CurrentStep)
	assert.Contains(t, installed["next_step"], "waiting for client_configuration proof")

	_, err = model.ApplyL1OnboardingProof(user.Id, token.Id, model.L1OnboardingProof{
		Step: model.L1OnboardingStepConfigureClient, Client: "Codex", BaseURL: "https://api.example.test/v1", Group: "default",
	}, 1002)
	require.NoError(t, err)
	require.NoError(t, db.Model(user).Update("last_api_activity_at", 1001).Error)
	before := executeAssistantAccountTool(user.Id)
	require.Equal(t, true, before["ok"])
	assert.Equal(t, model.L1OnboardingStepFirstSuccessfulResponse, before["onboarding_todo"].(*model.L1OnboardingTodoView).CurrentStep)
	assert.Equal(t, "pending", assistantAccountMainStep(t, before, "first_api_call")["status"])
	assert.Contains(t, before["next_step"], "an earlier call does not complete it")

	require.NoError(t, db.Model(user).Update("last_api_activity_at", 1003).Error)
	after := executeAssistantAccountTool(user.Id)
	require.Equal(t, true, after["ok"])
	assert.Equal(t, model.L1OnboardingStatusCompleted, after["onboarding_todo"].(*model.L1OnboardingTodoView).Status)
	for _, id := range []string{"install_client", "configure_client", "first_api_call"} {
		assert.Equal(t, "completed", assistantAccountMainStep(t, after, id)["status"])
	}
	assert.Contains(t, after["next_step"], "all required client proofs and post-configuration API activity")
	assert.Contains(t, after["next_step"], "verify the actual client response")
	var count int64
	require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestAssistantAccountAccessUsesFreshActivationFacts(t *testing.T) {
	db := setupAssistantAccountProgressDB(t)
	user := assistantAccountProgressUser(t, db, false)
	cached, err := model.GetUserCache(user.Id)
	require.NoError(t, err)
	assert.Zero(t, cached.ConsoleActivatedAt)
	require.NoError(t, db.Model(user).Update("console_activated_at", 100).Error)
	result := executeAssistantAccountTool(user.Id)
	require.Equal(t, true, result["ok"])
	assert.Equal(t, true, result["developer_access_granted"])
	assert.Equal(t, "L1", result["access_level"])
	assert.Contains(t, result, "onboarding_todo")
}

func TestAssistantAccountAccessReadFailuresAreUnavailable(t *testing.T) {
	for _, table := range []string{"users", "top_ups", "developer_access_requests", "tokens", "l1_onboarding_todos", "assistant_conversations", "assistant_new_user_gifts"} {
		t.Run(table, func(t *testing.T) {
			db := setupAssistantAccountProgressDB(t)
			user := assistantAccountProgressUser(t, db, table != "top_ups")
			require.NoError(t, db.Migrator().DropTable(table))
			result := executeAssistantAccountTool(user.Id)
			assert.Equal(t, false, result["ok"])
			assert.Equal(t, "unavailable", result["status"])
			assert.NotContains(t, result, "onboarding")
			assert.NotContains(t, result, "onboarding_todo")
			assert.NotContains(t, result, "main_task")
			assert.Contains(t, result["next_step"], "Do not claim a milestone is pending or completed")
		})
	}
}

func TestAssistantSelfAccountToolsReturnUSDWalletBalanceIndependentlyOfUsage(t *testing.T) {
	previousUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = previousUnit })
	for _, tc := range []struct {
		name  string
		quota int
		usd   float64
	}{
		{name: "positive_balance_without_usage", quota: 1500000, usd: 3},
		{name: "small_balance", quota: 1, usd: 0.000002},
		{name: "large_balance", quota: 9000000000000, usd: 18000000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupAssistantAccountProgressDB(t)
			user := assistantAccountProgressUser(t, db, true)
			require.NoError(t, db.Model(user).Updates(map[string]any{"quota": tc.quota, "used_quota": 0}).Error)
			for _, name := range []string{"get_account_access", "get_user_overview"} {
				c, _ := gin.CreateTestContext(nil)
				c.Set(assistantActorUserIDKey, user.Id)
				result := executeAssistantTool(c, assistantOpenAIToolCall{Function: assistantOpenAIToolCallFunction{Name: name, Arguments: `{}`}})
				require.Equal(t, true, result["ok"])
				balance := result
				if name == "get_user_overview" {
					assert.Equal(t, "self", result["scope"])
					balance = result["user"].(map[string]any)
					assert.Equal(t, 0, balance["used_quota"])
				}
				assert.Equal(t, tc.quota, balance["wallet_balance_quota"], name)
				assert.Equal(t, float64(500000), balance["quota_per_usd"], name)
				assert.Equal(t, tc.usd, balance["wallet_balance_usd"], name)
				assert.Equal(t, "available", balance["wallet_balance_status"], name)
				assert.Contains(t, balance["wallet_balance_note"], "remaining subscription quota and usage totals", name)
			}
		})
	}
}

func TestAssistantWalletBalanceUnavailableRemainsJSONSerializable(t *testing.T) {
	previousUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = previousUnit })
	for _, unit := range []float64{0, -1, math.NaN(), math.Inf(1), math.SmallestNonzeroFloat64} {
		common.QuotaPerUnit = unit
		fields := assistantWalletBalanceFields(500000)
		assert.Equal(t, "unavailable", fields["wallet_balance_status"])
		assert.Nil(t, fields["wallet_balance_usd"])
		assert.Nil(t, fields["quota_per_usd"])
		_, err := json.Marshal(fields)
		require.NoError(t, err)
	}
}

func TestAssistantAccountProgressAndBalancePrefetchEvenWithAgentLoopDisabled(t *testing.T) {
	for _, message := range []string{"主线任务仍然显示安装客户端", "请查询我的账户余额", "你帮我查一下余额吧"} {
		t.Run(message, func(t *testing.T) {
			db := setupAssistantAccountProgressDB(t)
			user := assistantAccountProgressUser(t, db, true)
			require.NoError(t, db.Model(user).Updates(map[string]any{"quota": 1500000, "used_quota": 0, "last_api_activity_at": 500}).Error)
			require.NoError(t, db.Create(&model.Token{UserId: user.Id, Key: "prefetch-proof-key", Status: common.TokenStatusEnabled, Group: "default"}).Error)
			context := assistantUserContext{UserID: user.Id, AccessLevel: "L1", DeveloperAccessGranted: true,
				LatestUserRequest: message, Intent: model.ClassifyAssistantIntent(message)}
			assert.Equal(t, "get_account_access", assistantNamedToolChoiceName(assistantToolChoiceForContext(context)))
			assert.Equal(t, []string{"get_account_access"}, assistantReadChain(context))
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/chat", nil)
			c.Set(assistantActorUserIDKey, user.Id)
			c.Set(assistantUserContextKey, context)
			originalRelay := relayAssistantAgentTurn
			calls := 0
			relayAssistantAgentTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
				calls++
				requireAssistantPairedReadReceipt(t, request, "get_account_access", true)
				encoded := string(mustAssistantJSON(t, request.Messages))
				assert.Contains(t, encoded, "wallet_balance_usd")
				assert.Contains(t, encoded, "onboarding_todo")
				assert.Contains(t, encoded, "client_heartbeat")
				return http.StatusOK, []byte(`{"choices":[{"message":{"role":"assistant","content":"Live account progress and wallet balance are available."}}]}`), nil
			}
			t.Cleanup(func() { relayAssistantAgentTurn = originalRelay })
			runAssistantAgent(c, setting.AssistantSettings{Model: "account-progress-test-model", AgentLoopEnabled: false, MaxSteps: 1, TimeoutSeconds: 45}, []assistantOpenAIMessage{{Role: "user", Content: message}})
			assert.Equal(t, 1, calls)
			assert.Equal(t, http.StatusOK, recorder.Code)
		})
	}
}
