package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type assistantGroupConfigSnapshot struct {
	Usable   map[string]string
	Ratios   map[string]float64
	Special  map[string]map[string]string
	Warnings map[string]ratio_setting.GroupWarning
}

// CreateAssistantDefaultKey confirms one opaque server-side draft. Name,
// group, conversation, and warning policy are loaded from the locked flow.
func CreateAssistantDefaultKey(c *gin.Context) {
	if !requireAssistantBrowserSession(c) {
		return
	}
	var input assistantConfirmKeyInput
	if err := decodeStrictAssistantJSON(c, &input); err != nil {
		writeAssistantError(c, http.StatusBadRequest, "ASSISTANT_INVALID_REQUEST", errors.New("invalid key confirmation request"))
		return
	}
	confirmationToken := strings.TrimSpace(input.ConfirmationToken)
	if confirmationToken == "" {
		writeAssistantError(c, http.StatusUnprocessableEntity, "ASSISTANT_CONFIRMATION_REQUIRED", errors.New("an opaque confirmation token is required"))
		return
	}
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		writeAssistantError(c, http.StatusForbidden, "ASSISTANT_SESSION_REQUIRED", errors.New("a current browser login session is required for assistant key confirmation"))
		return
	}
	userID := identity.UserID
	fence, err := model.NewAssistantKeyAuthorizationFence(
		identity.UserID,
		identity.SessionID,
		identity.SessionVersion,
		identity.UserAuthVersion,
		model.CurrentDeveloperAccessPolicy(),
	)
	if err != nil {
		writeAssistantError(c, http.StatusForbidden, "ASSISTANT_SESSION_REQUIRED", errors.New("a current browser login session is required for assistant key confirmation"))
		return
	}
	maxTokens := operation_setting.GetMaxUserTokens()
	token, card, err := model.ConsumeAssistantKeyFlowAndCreateSecureCard(
		confirmationToken,
		fence,
		input.TwoFactorCode,
		maxTokens,
		func(tx *gorm.DB, flow *model.AuthFlow) (*model.AssistantKeyMaterial, error) {
			return buildAssistantKeyMaterialTx(tx, flow, userID)
		},
	)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrAuthFlowInvalid), errors.Is(err, model.ErrAuthFlowExpired), errors.Is(err, model.ErrAuthFlowConsumed), errors.Is(err, model.ErrAssistantKeyAuthorizationChanged), errors.Is(err, errAssistantKeyAccountUnavailable):
			writeAssistantError(c, http.StatusUnprocessableEntity, "ASSISTANT_KEY_CONFIRMATION_INVALID", errors.New("API key confirmation is invalid, expired, already used, or no longer authorized"))
		case errors.Is(err, model.ErrAssistantKeyTwoFactorInvalid):
			writeAssistantError(c, http.StatusUnprocessableEntity, "ASSISTANT_TWO_FACTOR_INVALID", errors.New("a valid current two-factor code is required"))
		case errors.Is(err, errAssistantKeyGroupUnavailable):
			writeAssistantError(c, http.StatusUnprocessableEntity, "ASSISTANT_INVALID_GROUP", errors.New("the selected group is no longer available for this account"))
		case errors.Is(err, errAssistantKeyWarningChanged):
			writeAssistantError(c, http.StatusConflict, "ASSISTANT_GROUP_WARNING_CHANGED", errors.New("the selected group warning changed; prepare the key again"))
		case errors.Is(err, model.ErrAssistantTokenLimit):
			writeAssistantError(c, http.StatusConflict, "ASSISTANT_KEY_LIMIT_REACHED", fmt.Errorf("API key limit reached (%d)", maxTokens))
		default:
			writeAssistantError(c, http.StatusInternalServerError, "ASSISTANT_SECURE_CARD_CREATE_FAILED", errors.New("API key could not be created securely; please try again"))
		}
		return
	}
	model.RecordLog(userID, model.LogTypeSystem, fmt.Sprintf("created API key %d via assistant", token.Id))
	common.ApiSuccess(c, gin.H{
		"id": token.Id, "name": token.Name, "group": token.Group, "expired_time": token.ExpiredTime,
		"card": model.AssistantSecureCardViewForOwner(card), "privacy_notice": model.AssistantHistoryPrivacyNotice,
	})
}

func buildAssistantKeyMaterialTx(tx *gorm.DB, flow *model.AuthFlow, userID int) (*model.AssistantKeyMaterial, error) {
	var draft assistantPreparedKeyDraft
	if flow == nil || json.Unmarshal([]byte(flow.Payload), &draft) != nil || draft.Version != assistantKeyDraftVersion {
		return nil, model.ErrAuthFlowInvalid
	}
	name := strings.TrimSpace(draft.Name)
	if name == "" || utf8.RuneCountInString(name) > 50 {
		return nil, model.ErrAuthFlowInvalid
	}
	snapshot, userGroup, err := loadAssistantGroupConfigSnapshotTx(tx, userID)
	if err != nil {
		return nil, err
	}
	if !snapshot.IsSelectable(userGroup, draft.Group) {
		return nil, errAssistantKeyGroupUnavailable
	}
	currentWarning := snapshot.WarningFor(draft.Group)
	if !assistantWarningsEqual(draft.Warning, currentWarning) {
		return nil, errAssistantKeyWarningChanged
	}
	key, err := common.GenerateKey()
	if err != nil {
		return nil, errors.New("failed to generate API key")
	}
	now := common.GetTimestamp()
	token := &model.Token{
		UserId: userID, Name: name, Key: key, Group: string(draft.Group),
		CreatedTime: now, AccessedTime: now, ExpiredTime: -1, UnlimitedQuota: true,
		ModelLimitsEnabled: false, CrossGroupRetry: false,
	}
	return &model.AssistantKeyMaterial{
		Token: token, ConversationID: draft.ConversationID,
		Summary:       "已创建 API 凭证；仅你可一次性查看和复制",
		SecurePayload: fmt.Sprintf(`{"api_key":%q}`, "sk-"+key),
	}, nil
}

func loadAssistantGroupConfigSnapshotTx(tx *gorm.DB, userID int) (*assistantGroupConfigSnapshot, string, error) {
	var user model.User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id", "group", "status").
		Where("id = ?", userID).
		First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", errAssistantKeyAccountUnavailable
		}
		return nil, "", fmt.Errorf("lock assistant key user: %w", err)
	}
	if user.Status != common.UserStatusEnabled {
		return nil, "", errAssistantKeyAccountUnavailable
	}
	// PostgreSQL row locks protect existing option rows, but not an absent
	// override being inserted concurrently. Hold the same table-level share
	// lock as the Rust backend through credential and secure-card commit.
	if tx.Dialector.Name() == "postgres" {
		if err := tx.Exec("LOCK TABLE options IN SHARE MODE").Error; err != nil {
			return nil, "", err
		}
	}
	keys := []string{
		"UserUsableGroups", "GroupRatio", "group_ratio_setting.group_special_usable_group",
		"group_ratio_setting.group_warnings", "group_ratio_setting",
	}
	var options []model.Option
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("key IN ?", keys).Find(&options).Error; err != nil {
		return nil, "", err
	}
	values := make(map[string]string, len(options))
	for _, option := range options {
		values[option.Key] = option.Value
	}
	usableRaw, usableOK := values["UserUsableGroups"]
	ratioRaw, ratioOK := values["GroupRatio"]
	if !usableOK || !ratioOK {
		return nil, "", errors.New("authoritative group configuration is unavailable")
	}
	snapshot := &assistantGroupConfigSnapshot{}
	if json.Unmarshal([]byte(usableRaw), &snapshot.Usable) != nil || json.Unmarshal([]byte(ratioRaw), &snapshot.Ratios) != nil {
		return nil, "", errors.New("authoritative group configuration is invalid")
	}
	var legacy struct {
		Special  map[string]map[string]string          `json:"group_special_usable_group"`
		Warnings map[string]ratio_setting.GroupWarning `json:"group_warnings"`
	}
	if raw := values["group_ratio_setting"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &legacy); err != nil {
			return nil, "", errors.New("authoritative group settings are invalid")
		}
	}
	snapshot.Special = legacy.Special
	snapshot.Warnings = legacy.Warnings
	if raw := values["group_ratio_setting.group_special_usable_group"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &snapshot.Special); err != nil {
			return nil, "", errors.New("authoritative group overrides are invalid")
		}
	}
	if raw := values["group_ratio_setting.group_warnings"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &snapshot.Warnings); err != nil {
			return nil, "", errors.New("authoritative group warnings are invalid")
		}
	}
	if snapshot.Special == nil {
		snapshot.Special = map[string]map[string]string{}
	}
	if snapshot.Warnings == nil {
		snapshot.Warnings = map[string]ratio_setting.GroupWarning{}
	}
	return snapshot, user.Group, nil
}

func (snapshot *assistantGroupConfigSnapshot) IsSelectable(userGroup string, group realSelectableGroup) bool {
	if snapshot == nil {
		return false
	}
	available := make(map[string]string, len(snapshot.Usable)+1)
	for name, description := range snapshot.Usable {
		available[name] = description
	}
	for name, description := range snapshot.Special[userGroup] {
		switch {
		case strings.HasPrefix(name, "-:"):
			delete(available, strings.TrimPrefix(name, "-:"))
		case strings.HasPrefix(name, "+:"):
			available[strings.TrimPrefix(name, "+:")] = description
		default:
			available[name] = description
		}
	}
	if userGroup != "" {
		if _, exists := available[userGroup]; !exists {
			available[userGroup] = "用户分组"
		}
	}
	_, usable := available[string(group)]
	_, ratioConfigured := snapshot.Ratios[string(group)]
	return usable && ratioConfigured
}

func (snapshot *assistantGroupConfigSnapshot) WarningFor(group realSelectableGroup) *ratio_setting.GroupWarning {
	if snapshot == nil {
		return nil
	}
	name := string(group)
	for configuredGroup, configuredWarning := range snapshot.Warnings {
		if configuredGroup == name || strings.EqualFold(strings.TrimSpace(configuredGroup), name) {
			if !configuredWarning.Enabled {
				return nil
			}
			warning := configuredWarning
			return &warning
		}
	}
	if ratio, exists := snapshot.Ratios[name]; exists && ratio == 0 {
		return &ratio_setting.GroupWarning{
			Enabled: true,
			Message: "This routing group is community-operated. Availability, model coverage, privacy handling, and billing behavior may be less predictable. Do not send secrets or sensitive data. Continue only if you accept these risks.",
			Mode:    "modal", Confirmations: 3,
		}
	}
	return nil
}

func assistantWarningsEqual(left, right *ratio_setting.GroupWarning) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
