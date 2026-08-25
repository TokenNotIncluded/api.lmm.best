package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type assistantPreparedActionEnvelope struct {
	Success bool                       `json:"success"`
	Data    assistantPreparedKeyAction `json:"data"`
}

func setAssistantKeyOption(t *testing.T, db *gorm.DB, key, value string) {
	t.Helper()
	require.NoError(t, db.Save(&model.Option{Key: key, Value: value}).Error)
	switch key {
	case "UserUsableGroups":
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(value))
	case "GroupRatio":
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(value))
	case "group_ratio_setting.group_special_usable_group":
		var groups map[string]map[string]string
		require.NoError(t, json.Unmarshal([]byte(value), &groups))
		current := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup
		current.Clear()
		current.AddAll(groups)
	case "group_ratio_setting.group_warnings":
		require.NoError(t, ratio_setting.UpdateGroupWarningsByJSONString(value))
	default:
		t.Fatalf("unsupported assistant key option %q", key)
	}
}

func configureAssistantKeyGroups(t *testing.T, db *gorm.DB) {
	t.Helper()
	originalUsable, err := json.Marshal(setting.GetUserUsableGroupsCopy())
	require.NoError(t, err)
	originalRatios, err := json.Marshal(ratio_setting.GetGroupRatioCopy())
	require.NoError(t, err)
	originalSpecial, err := json.Marshal(ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.ReadAll())
	require.NoError(t, err)
	originalWarnings, err := json.Marshal(ratio_setting.GetGroupWarningsCopy())
	require.NoError(t, err)

	require.NoError(t, db.AutoMigrate(&model.Option{}))
	setAssistantKeyOption(t, db, "UserUsableGroups", `{"default":"默认分组","vip":"VIP"}`)
	setAssistantKeyOption(t, db, "GroupRatio", `{"default":1,"vip":1}`)
	setAssistantKeyOption(t, db, "group_ratio_setting.group_special_usable_group", `{}`)
	setAssistantKeyOption(t, db, "group_ratio_setting.group_warnings", `{}`)
	t.Cleanup(func() {
		setAssistantKeyOption(t, db, "UserUsableGroups", string(originalUsable))
		setAssistantKeyOption(t, db, "GroupRatio", string(originalRatios))
		setAssistantKeyOption(t, db, "group_ratio_setting.group_special_usable_group", string(originalSpecial))
		setAssistantKeyOption(t, db, "group_ratio_setting.group_warnings", string(originalWarnings))
	})
}

func createAssistantKeyFixture(t *testing.T, username string) (*gorm.DB, model.User) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.Option{}, &model.AuthFlow{}, &model.TwoFA{}, &model.TwoFABackupCode{}, &model.TopUp{}, &model.UserSession{}, &model.Log{},
		&model.AssistantConversation{}, &model.AssistantHistoryMessage{}, &model.AssistantSecureCard{},
	))
	configureAssistantKeyGroups(t, db)
	user := model.User{
		Username: username, Password: "password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", ConsoleActivatedAt: 1, AuthVersion: 1,
	}
	require.NoError(t, db.Create(&user).Error)
	return db, user
}

func createAssistantKeyTestContext(t *testing.T, username string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	_, user := createAssistantKeyFixture(t, username)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Set("id", user.Id)
	context.Set("session_id", "assistant-key-test-session")
	return context, response
}

func assistantKeyContext(t *testing.T, method, path, body string, userID int, sessionID string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	if model.DB != nil && userID > 0 && sessionID != "" {
		require.NoError(t, model.DB.FirstOrCreate(&model.UserSession{
			SID: sessionID, UserID: userID, Version: 1, UserAuthVersion: 1,
			Status: model.UserSessionStatusActive, RefreshHash: strings.Repeat("a", 64),
			LoginMethod: "password", CreatedAt: time.Now().Unix(), LastActiveAt: time.Now().Unix(),
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		}, "sid = ?", sessionID).Error)
	}
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Set("id", userID)
	context.Set("session_id", sessionID)
	context.Set("session_version", int64(1))
	context.Set("auth_version", int64(1))
	return context, response
}

func prepareAssistantKeyForTest(t *testing.T, user model.User, sessionID, name, group string) assistantPreparedKeyAction {
	t.Helper()
	context, response := assistantKeyContext(
		t,
		http.MethodPost,
		"/api/assistant/tools/prepare-key",
		fmt.Sprintf(`{"name":%q,"group":%q,"conversation_id":0,"group_warning_confirmations":0}`, name, group),
		user.Id,
		sessionID,
	)
	PrepareAssistantDefaultKey(context)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var envelope assistantPreparedActionEnvelope
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.True(t, envelope.Success)
	require.NotEmpty(t, envelope.Data.ConfirmationToken)
	return envelope.Data
}

func confirmAssistantKeyForTest(t *testing.T, userID int, sessionID, token, extra string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	body := fmt.Sprintf(`{"confirmation_token":%q%s}`, token, extra)
	context, response := assistantKeyContext(t, http.MethodPost, "/api/assistant/tools/create-key", body, userID, sessionID)
	CreateAssistantDefaultKey(context)
	return response, response.Body.String()
}

func TestAssistantKeyGroupOptionsExcludeVirtualAutoEvenWhenConfigured(t *testing.T) {
	db, user := createAssistantKeyFixture(t, "assistant-real-groups")
	setAssistantKeyOption(t, db, "UserUsableGroups", `{"auto":"must not leak","default":"Default"}`)
	setAssistantKeyOption(t, db, "GroupRatio", `{"auto":1,"default":1}`)
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["default"]`))
	var persisted model.Option
	require.NoError(t, db.First(&persisted, "key = ?", "UserUsableGroups").Error)

	options := getAssistantKeyGroupOptions(user.Group)
	require.Len(t, options, 1)
	assert.Equal(t, "default", options[0].ID)
	assert.False(t, service.IsUserSelectableGroup(user.Group, "auto") && options[0].ID == "auto")

	context, _ := assistantKeyContext(t, http.MethodPost, "/api/assistant/chat", "", user.Id, "assistant-real-groups-session")
	result := executeAssistantCreateKeyRequestTool(context, user.Id, map[string]any{"name": "key"})
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), `"id":"auto"`)
}

func TestAssistantKeyConfirmationRequiresOnlyOpaqueToken(t *testing.T) {
	_, user := createAssistantKeyFixture(t, "assistant-opaque-token")
	missing, missingBody := confirmAssistantKeyForTest(t, user.Id, "opaque-session", "", "")
	assert.Equal(t, http.StatusUnprocessableEntity, missing.Code)
	assert.Contains(t, missingBody, "ASSISTANT_CONFIRMATION_REQUIRED")

	action := prepareAssistantKeyForTest(t, user, "opaque-session", "opaque", "default")
	tampered, tamperedBody := confirmAssistantKeyForTest(
		t,
		user.Id,
		"opaque-session",
		action.ConfirmationToken,
		`,"name":"tampered","group":"auto"`,
	)
	assert.Equal(t, http.StatusBadRequest, tampered.Code)
	assert.Contains(t, tamperedBody, "ASSISTANT_INVALID_REQUEST")

	valid, _ := confirmAssistantKeyForTest(t, user.Id, "opaque-session", action.ConfirmationToken, "")
	assert.Equal(t, http.StatusOK, valid.Code, valid.Body.String())
}

func TestAssistantKeyConfirmationIsSessionBoundOneTimeAndOpaque(t *testing.T) {
	db, user := createAssistantKeyFixture(t, "assistant-key-transaction")
	action := prepareAssistantKeyForTest(t, user, "key-session", "transactional", "default")

	wrongSession, _ := confirmAssistantKeyForTest(t, user.Id, "other-session", action.ConfirmationToken, "")
	assert.Equal(t, http.StatusUnprocessableEntity, wrongSession.Code)

	confirmed, confirmedBody := confirmAssistantKeyForTest(t, user.Id, "key-session", action.ConfirmationToken, "")
	require.Equal(t, http.StatusOK, confirmed.Code, confirmedBody)
	assert.NotContains(t, confirmedBody, "sk-")
	var response struct {
		Data struct {
			ID   int `json:"id"`
			Card struct {
				ID string `json:"id"`
			} `json:"card"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(confirmed.Body.Bytes(), &response))
	require.Positive(t, response.Data.ID)
	require.NotEmpty(t, response.Data.Card.ID)
	var card model.AssistantSecureCard
	require.NoError(t, db.First(&card, "id = ?", response.Data.Card.ID).Error)
	assert.NotContains(t, card.Ciphertext, "sk-")
	revealed, _, err := model.RevealAssistantSecureCard(user.Id, response.Data.Card.ID)
	require.NoError(t, err)
	payload, err := model.AssistantSecureCardPayload(revealed)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(payload["api_key"], "sk-"))

	replayed, replayBody := confirmAssistantKeyForTest(t, user.Id, "key-session", action.ConfirmationToken, "")
	assert.Equal(t, http.StatusUnprocessableEntity, replayed.Code)
	assert.Contains(t, replayBody, "ASSISTANT_KEY_CONFIRMATION_INVALID")
	var tokenCount int64
	require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", user.Id).Count(&tokenCount).Error)
	assert.EqualValues(t, 1, tokenCount)
}

func TestAssistantKeyConfirmationRejectsStaleAuthoritativeStateAndRollsBack(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(t *testing.T, db *gorm.DB, user model.User)
		code       string
		wantStatus int
	}{
		{
			name: "selectable group removed",
			mutate: func(t *testing.T, db *gorm.DB, _ model.User) {
				setAssistantKeyOption(t, db, "UserUsableGroups", `{"default":"Default"}`)
			},
			code: "ASSISTANT_INVALID_GROUP",
		},
		{
			name: "ratio removed",
			mutate: func(t *testing.T, db *gorm.DB, _ model.User) {
				setAssistantKeyOption(t, db, "GroupRatio", `{"default":1}`)
			},
			code: "ASSISTANT_INVALID_GROUP",
		},
		{
			name: "current user group override changed",
			mutate: func(t *testing.T, db *gorm.DB, user model.User) {
				setAssistantKeyOption(t, db, "group_ratio_setting.group_special_usable_group", `{"restricted":{"-:vip":""}}`)
				require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Update("group", "restricted").Error)
			},
			code: "ASSISTANT_INVALID_GROUP",
		},
		{
			name: "account disabled after prepare",
			mutate: func(t *testing.T, db *gorm.DB, user model.User) {
				require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Update("status", common.UserStatusDisabled).Error)
			},
			code:       "ASSISTANT_KEY_CONFIRMATION_INVALID",
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name: "authoritative option read fails",
			mutate: func(t *testing.T, db *gorm.DB, _ model.User) {
				require.NoError(t, db.Exec("ALTER TABLE options RENAME TO options_unavailable").Error)
				t.Cleanup(func() {
					require.NoError(t, db.Exec("ALTER TABLE options_unavailable RENAME TO options").Error)
				})
			},
			code:       "ASSISTANT_SECURE_CARD_CREATE_FAILED",
			wantStatus: http.StatusInternalServerError,
		},
		{
			name: "warning revision changed",
			mutate: func(t *testing.T, db *gorm.DB, _ model.User) {
				setAssistantKeyOption(t, db, "group_ratio_setting.group_warnings", `{"vip":{"enabled":true,"message":"New warning","mode":"modal","confirmations":1}}`)
			},
			code: "ASSISTANT_GROUP_WARNING_CHANGED",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, user := createAssistantKeyFixture(t, "assistant-stale-"+strings.ReplaceAll(test.name, " ", "-"))
			action := prepareAssistantKeyForTest(t, user, "stale-session", "stale", "vip")
			test.mutate(t, db, user)
			response, body := confirmAssistantKeyForTest(t, user.Id, "stale-session", action.ConfirmationToken, "")
			if test.wantStatus == 0 {
				assert.Contains(t, []int{http.StatusConflict, http.StatusUnprocessableEntity}, response.Code)
			} else {
				assert.Equal(t, test.wantStatus, response.Code)
			}
			assert.Contains(t, body, test.code)
			var tokenCount, cardCount int64
			require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", user.Id).Count(&tokenCount).Error)
			require.NoError(t, db.Model(&model.AssistantSecureCard{}).Where("owner_user_id = ?", user.Id).Count(&cardCount).Error)
			assert.Zero(t, tokenCount)
			assert.Zero(t, cardCount)
			var flow model.AuthFlow
			require.NoError(t, db.Where("purpose = ? AND user_id = ?", model.AuthFlowPurposeAssistantKey, user.Id).First(&flow).Error)
			assert.Nil(t, flow.ConsumedAt, "failed confirmation must roll flow consumption back")
		})
	}
}

func TestAssistantKeyConfirmationDatabaseFailureRemainsInternal(t *testing.T) {
	db, user := createAssistantKeyFixture(t, "assistant-key-db-error")
	action := prepareAssistantKeyForTest(t, user, "db-error-session", "db-error", "default")
	requestBody := fmt.Sprintf(`{"confirmation_token":%q}`, action.ConfirmationToken)
	context, response := assistantKeyContext(t, http.MethodPost, "/api/assistant/tools/create-key", requestBody, user.Id, "db-error-session")
	require.NoError(t, db.Exec("ALTER TABLE user_sessions RENAME TO user_sessions_unavailable").Error)
	t.Cleanup(func() {
		require.NoError(t, db.Exec("ALTER TABLE user_sessions_unavailable RENAME TO user_sessions").Error)
	})

	CreateAssistantDefaultKey(context)
	body := response.Body.String()
	assert.Equal(t, http.StatusInternalServerError, response.Code, body)
	assert.NotContains(t, body, "ASSISTANT_KEY_CONFIRMATION_INVALID")
	var flow model.AuthFlow
	require.NoError(t, db.Where("session_id = ?", "db-error-session").First(&flow).Error)
	assert.Nil(t, flow.ConsumedAt)
	var tokenCount, cardCount int64
	require.NoError(t, db.Model(&model.Token{}).Count(&tokenCount).Error)
	require.NoError(t, db.Model(&model.AssistantSecureCard{}).Count(&cardCount).Error)
	assert.Zero(t, tokenCount)
	assert.Zero(t, cardCount)
}

func TestAssistantKeyConfirmationRejectsForgedAutoAndExpiredDrafts(t *testing.T) {
	db, user := createAssistantKeyFixture(t, "assistant-forged-draft")
	for _, test := range []struct {
		name      string
		payload   string
		expiresAt time.Time
		code      string
	}{
		{
			name:      "forged auto",
			payload:   `{"version":1,"name":"forged","group":"auto","conversation_id":0,"warning":null}`,
			expiresAt: time.Now().Add(time.Minute),
			code:      "ASSISTANT_KEY_CONFIRMATION_INVALID",
		},
		{
			name:      "expired",
			payload:   `{"version":1,"name":"expired","group":"default","conversation_id":0,"warning":null}`,
			expiresAt: time.Now().Add(time.Minute),
			code:      "ASSISTANT_KEY_CONFIRMATION_INVALID",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			rawToken, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
				Purpose: model.AuthFlowPurposeAssistantKey, UserId: user.Id,
				SessionId: "forged-session", Payload: test.payload, ExpiresAt: test.expiresAt,
			})
			require.NoError(t, err)
			if test.name == "expired" {
				require.NoError(t, db.Model(&model.AuthFlow{}).Where("purpose = ? AND user_id = ?", model.AuthFlowPurposeAssistantKey, user.Id).Update("expires_at", time.Now().Add(-time.Minute)).Error)
			}
			response, body := confirmAssistantKeyForTest(t, user.Id, "forged-session", rawToken, "")
			assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
			assert.Contains(t, body, test.code)
		})
	}
	var tokenCount int64
	require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", user.Id).Count(&tokenCount).Error)
	assert.Zero(t, tokenCount)
}
