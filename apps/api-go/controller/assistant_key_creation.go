package controller

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

const (
	assistantKeyConfirmationTTL = 10 * time.Minute
	assistantKeyDraftVersion    = 1
)

var (
	errAssistantKeyAccountUnavailable = errors.New("assistant key account is no longer active")
	errAssistantKeyGroupUnavailable   = errors.New("assistant key group is not currently selectable")
	errAssistantKeyWarningChanged     = errors.New("assistant key group warning changed after preparation")
)

type realSelectableGroup string

func parseRealSelectableGroup(raw string) (realSelectableGroup, error) {
	group := strings.TrimSpace(raw)
	if group == "" || group == "auto" || utf8.RuneCountInString(group) > 64 {
		return "", errAssistantKeyGroupUnavailable
	}
	return realSelectableGroup(group), nil
}

func currentRealSelectableGroup(userGroup, raw string) (realSelectableGroup, error) {
	group, err := parseRealSelectableGroup(raw)
	if err != nil || !service.IsUserSelectableGroup(userGroup, string(group)) {
		return "", errAssistantKeyGroupUnavailable
	}
	return group, nil
}

func (group *realSelectableGroup) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return errAssistantKeyGroupUnavailable
	}
	parsed, err := parseRealSelectableGroup(raw)
	if err != nil {
		return err
	}
	*group = parsed
	return nil
}

type assistantPreparedKeyDraft struct {
	Version        int                         `json:"version"`
	Name           string                      `json:"name"`
	Group          realSelectableGroup         `json:"group"`
	ConversationID int64                       `json:"conversation_id"`
	Warning        *ratio_setting.GroupWarning `json:"warning"`
}

type assistantPrepareKeyInput struct {
	Name                      string `json:"name"`
	Group                     string `json:"group"`
	ConversationID            int64  `json:"conversation_id"`
	GroupWarningConfirmations int    `json:"group_warning_confirmations"`
}

type assistantConfirmKeyInput struct {
	ConfirmationToken string `json:"confirmation_token"`
	TwoFactorCode     string `json:"two_factor_code"`
}

type assistantKeyGroupOption struct {
	ID          string                      `json:"id"`
	Description string                      `json:"description"`
	Warning     *ratio_setting.GroupWarning `json:"warning,omitempty"`
}

type assistantPreparedKeyAction struct {
	Type                 string `json:"type"`
	ConfirmationToken    string `json:"confirmation_token"`
	RequiresConfirmation bool   `json:"requires_confirmation"`
	ExpiresInSeconds     int    `json:"expires_in_seconds"`
	Name                 string `json:"name"`
	Group                string `json:"group"`
	ConversationID       int64  `json:"conversation_id"`
	UIPath               string `json:"ui_path"`
}

func decodeStrictAssistantJSON(c *gin.Context, destination any) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}
