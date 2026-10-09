package controller

import (
	"context"
	"encoding/json"
	"maps"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func assistantPolicyContentTestContext(t *testing.T, role int) (*gin.Context, model.User) {
	t.Helper()
	db := setupTokenControllerTestDB(t)
	c, user, _ := assistantAutomationTestContext(t, db, role)
	require.NoError(t, db.AutoMigrate(&model.AuthFlow{}))
	legal := *system_setting.GetLegalSettings()
	common.OptionMapRWMutex.RLock()
	options := maps.Clone(common.OptionMap)
	common.OptionMapRWMutex.RUnlock()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, middleware.WaitAdminAudits(ctx))
		*system_setting.GetLegalSettings() = legal
		common.OptionMapRWMutex.Lock()
		common.OptionMap = options
		common.OptionMapRWMutex.Unlock()
	})
	assistantPolicyForTest(t, nil, nil)
	return c, user
}

func seedAssistantPolicy(t *testing.T, key, value string) {
	t.Helper()
	require.NoError(t, model.DB.Create(&model.Option{Key: key, Value: value}).Error)
}

func TestAssistantSitePolicyReadsEveryDocumentAndLanguageFresh(t *testing.T) {
	c, _ := assistantPolicyContentTestContext(t, common.RoleCommonUser)
	for _, doc := range assistantSitePolicyDocuments {
		seedAssistantPolicy(t, "legal."+doc, "中文 "+doc)
		seedAssistantPolicy(t, "legal."+doc+"_en", "English "+doc)
		for _, language := range []string{"zh-CN", "en"} {
			result := executeAssistantGetSitePolicy(c, map[string]any{"document": doc, "language": language})
			require.Equal(t, true, result["ok"])
			assert.Equal(t, language, result["source_language"])
			assert.Equal(t, false, result["language_fallback"])
			assert.Len(t, result["revision"], 64)
			assert.Contains(t, result["content"], doc)
		}
	}
	// Ignore stale in-process legal settings; both public and tool reads see DB.
	system_setting.GetLegalSettings().UserAgreement = "stale cached agreement"
	require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", "legal.user_agreement").Update("value", "fresh agreement").Error)
	result := executeAssistantGetSitePolicy(c, map[string]any{"document": "user_agreement"})
	assert.Equal(t, "fresh agreement", result["content"])
	recorder := httptest.NewRecorder()
	public, _ := gin.CreateTestContext(recorder)
	public.Request = httptest.NewRequest("GET", "/api/user-agreement", nil)
	GetUserAgreement(public)
	require.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "fresh agreement")
	assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
}

func TestAssistantSitePolicyUnicodePagesAndLiteralSearch(t *testing.T) {
	c, _ := assistantPolicyContentTestContext(t, common.RoleCommonUser)
	content := strings.Repeat("退款 <>& \"\n", 1000) + "Last refund."
	seedAssistantPolicy(t, "legal.refund_policy", content)
	first := executeAssistantGetSitePolicy(c, map[string]any{"document": "refund_policy", "language": "en", "limit": 6000.0})
	require.Equal(t, true, first["ok"])
	assert.Equal(t, true, first["language_fallback"])
	assert.Equal(t, false, first["configured"])
	_, err := common.MarshalLimit(first, assistantToolResultMaxBytes)
	require.NoError(t, err)
	second := executeAssistantGetSitePolicy(c, map[string]any{"document": "refund_policy", "language": "en", "offset": float64(first["next_offset"].(int)), "limit": 6000.0})
	assert.Equal(t, content, first["content"].(string)+second["content"].(string))
	result := executeAssistantSearchSitePolicies(c, map[string]any{"query": "退款", "limit": 20.0, "offset": 20.0})
	require.Equal(t, true, result["ok"])
	assert.Equal(t, 1000, result["total"])
	assert.Len(t, result["matches"], 20)
	assert.Equal(t, 40, result["next_offset"])
	_, err = common.MarshalLimit(result, assistantToolResultMaxBytes)
	require.NoError(t, err)
	result = executeAssistantSearchSitePolicies(c, map[string]any{"query": "ReFuNd"})
	assert.Equal(t, 1, result["total"])
	result = executeAssistantSearchSitePolicies(c, map[string]any{"query": ".*"})
	assert.Equal(t, 0, result["total"])
	for _, input := range []map[string]any{{"query": ""}, {"query": "a", "offset": 0.5}, {"query": "a", "limit": 21.0}, {"query": "a", "language": "../en"}, {"query": "a", "document": "../../Option"}} {
		assert.Equal(t, false, executeAssistantSearchSitePolicies(c, input)["ok"])
	}
}

func TestAssistantSitePolicyDoesNotFetchLinksOrInventMissingRefundTerms(t *testing.T) {
	c, _ := assistantPolicyContentTestContext(t, common.RoleCommonUser)
	seedAssistantPolicy(t, "legal.privacy_policy", "https://127.0.0.1/private-policy")
	result := executeAssistantGetSitePolicy(c, map[string]any{"document": "privacy_policy"})
	assert.Equal(t, "external_url", result["format"])
	assert.Equal(t, false, result["external_content_fetched"])
	result = executeAssistantGetSitePolicy(c, map[string]any{"document": "refund_policy"})
	assert.Equal(t, false, result["configured"])
	assert.Equal(t, "", result["content"])
	result = executeAssistantSearchSitePolicies(c, map[string]any{"query": "private"})
	assert.Equal(t, 0, result["total"])
	assert.Len(t, result["skipped_documents"], 3)
}

func assistantPolicyConfirmContext(original *gin.Context, token string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	for key, value := range original.Keys {
		c.Set(key, value)
	}
	body, _ := json.Marshal(map[string]any{"confirmed": true, "confirmation_token": token})
	c.Request = httptest.NewRequest("POST", "/api/assistant/admin/apply", strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, recorder
}

func TestAssistantSitePolicyPreviewRequiresExplicitSingleUseConfirmation(t *testing.T) {
	c, user := assistantPolicyContentTestContext(t, common.RoleRootUser)
	seedAssistantPolicy(t, "legal.refund_policy", "Keep this. Refund within 7 days. Keep this too.")
	read := executeAssistantGetSitePolicy(c, map[string]any{"document": "refund_policy"})
	result := executeAssistantPrepareSitePolicy(c, user.Id, map[string]any{"document": "refund_policy", "language": "zh-CN", "revision": read["revision"], "old_text": "7 days", "new_text": "14 days"})
	require.Equal(t, true, result["ok"], "%v", result)
	assert.Equal(t, false, result["applied"])
	current, err := model.ReadSitePolicies(context.Background())
	require.NoError(t, err)
	assert.Contains(t, current["legal.refund_policy"], "7 days")
	action, _ := c.Get(assistantClientActionKey)
	token := action.(map[string]any)["confirmation_token"].(string)
	encoded, _ := json.Marshal(result)
	assert.NotContains(t, string(encoded), token)
	confirm, recorder := assistantPolicyConfirmContext(c, token)
	ApplyAssistantAdminChange(confirm)
	require.Equal(t, 200, recorder.Code, recorder.Body.String())
	current, err = model.ReadSitePolicies(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Keep this. Refund within 14 days. Keep this too.", current["legal.refund_policy"])
	again, replay := assistantPolicyConfirmContext(c, token)
	ApplyAssistantAdminChange(again)
	assert.Equal(t, 422, replay.Code)
}

func TestAssistantSitePolicyRevocationAndConcurrentEditsFailClosed(t *testing.T) {
	for _, scenario := range []string{"stale_revision", "changed_after_preview", "disabled_tool", "different_session", "demoted"} {
		t.Run(scenario, func(t *testing.T) {
			c, user := assistantPolicyContentTestContext(t, common.RoleRootUser)
			seedAssistantPolicy(t, "legal.user_agreement", "original agreement")
			read := executeAssistantGetSitePolicy(c, map[string]any{"document": "user_agreement"})
			input := map[string]any{"document": "user_agreement", "language": "zh-CN", "revision": read["revision"], "content": "proposed agreement"}
			if scenario == "stale_revision" {
				input["revision"] = strings.Repeat("0", 64)
			}
			result := executeAssistantPrepareSitePolicy(c, user.Id, input)
			if scenario == "stale_revision" {
				assert.Equal(t, "policy_changed", result["status"])
				return
			}
			require.Equal(t, true, result["ok"], "%v", result)
			action, _ := c.Get(assistantClientActionKey)
			token := action.(map[string]any)["confirmation_token"].(string)
			switch scenario {
			case "changed_after_preview":
				require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", "legal.user_agreement").Update("value", "other administrator edit").Error)
			case "disabled_tool":
				assistantPolicyForTest(t, nil, map[string]bool{"prepare_admin_site_policy_change": false})
			case "different_session":
				c.Set("session_id", "not-the-preview-session")
			case "demoted":
				require.NoError(t, model.DB.Model(&user).Update("role", common.RoleAdminUser).Error)
			}
			confirm, recorder := assistantPolicyConfirmContext(c, token)
			ApplyAssistantAdminChange(confirm)
			require.GreaterOrEqual(t, recorder.Code, 400, recorder.Body.String())
			current, err := model.ReadSitePolicies(context.Background())
			require.NoError(t, err)
			assert.NotEqual(t, "proposed agreement", current["legal.user_agreement"])
		})
	}
}

func TestAssistantSitePolicyRejectsAmbiguousOrUnauthorizedEdits(t *testing.T) {
	c, user := assistantPolicyContentTestContext(t, common.RoleRootUser)
	seedAssistantPolicy(t, "legal.privacy_policy", "same same")
	read := executeAssistantGetSitePolicy(c, map[string]any{"document": "privacy_policy"})
	for _, fields := range []map[string]any{
		{"old_text": "same", "new_text": "replacement"}, {"old_text": "missing", "new_text": "replacement"}, {"content": "new", "old_text": "same", "new_text": "new"}, {"content": ""}, {"content": true}, {"content": strings.Repeat("a", 12001)},
	} {
		input := map[string]any{"document": "privacy_policy", "language": "zh-CN", "revision": read["revision"]}
		maps.Copy(input, fields)
		assert.Equal(t, false, executeAssistantPrepareSitePolicy(c, user.Id, input)["ok"])
	}
	require.NoError(t, model.DB.Model(&user).Update("role", common.RoleAdminUser).Error)
	result := executeAssistantPrepareSitePolicy(c, user.Id, map[string]any{"document": "privacy_policy", "language": "zh-CN", "revision": read["revision"], "content": "changed"})
	assert.Equal(t, "forbidden", result["status"])
}
