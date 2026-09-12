package setting

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	AssistantL1AutoReviewEnabledOptionKey       = "AssistantL1AutoReviewEnabled"
	AssistantL1AutoReviewModelOptionKey         = "AssistantL1AutoReviewModel"
	AssistantL1AutoReviewGroupOptionKey         = "AssistantL1AutoReviewGroup"
	AssistantL1AutoReviewPromptOptionKey        = "AssistantL1AutoReviewPrompt"
	AssistantL1AutoReviewMinConfidenceOptionKey = "AssistantL1AutoReviewMinConfidence"
	DefaultAssistantL1AutoReviewMinConfidence   = 0.98
)

// AssistantL1AutoReviewSettings is separate from chat and history/security
// reviews. An empty route or policy never falls back to either of those agents.
// It is comparable so a completed review can be fenced against configuration
// changes before any user privileges or review notes are written.
type AssistantL1AutoReviewSettings struct {
	Enabled       bool
	Model         string
	Group         string
	Prompt        string
	MinConfidence float64
	UserIDs       string
}

func DefaultAssistantL1AutoReviewSettings() AssistantL1AutoReviewSettings {
	return AssistantL1AutoReviewSettings{MinConfidence: DefaultAssistantL1AutoReviewMinConfidence}
}

func IsAssistantL1AutoReviewOption(key string) bool {
	switch key {
	case AssistantL1AutoReviewEnabledOptionKey, AssistantL1AutoReviewModelOptionKey,
		AssistantL1AutoReviewGroupOptionKey, AssistantL1AutoReviewPromptOptionKey,
		AssistantL1AutoReviewMinConfidenceOptionKey, AssistantL1AutoApprovalUserIDsOptionKey:
		return true
	}
	return false
}

func (settings AssistantL1AutoReviewSettings) OptionValues() map[string]string {
	return map[string]string{
		AssistantL1AutoReviewEnabledOptionKey:       strconv.FormatBool(settings.Enabled),
		AssistantL1AutoReviewModelOptionKey:         settings.Model,
		AssistantL1AutoReviewGroupOptionKey:         settings.Group,
		AssistantL1AutoReviewPromptOptionKey:        settings.Prompt,
		AssistantL1AutoReviewMinConfidenceOptionKey: strconv.FormatFloat(settings.MinConfidence, 'f', -1, 64),
		AssistantL1AutoApprovalUserIDsOptionKey:     settings.UserIDs,
	}
}

// ParseAssistantL1AutoReviewSettings validates individual fields without
// requiring a complete route: a disabled reviewer may be configured in stages.
func ParseAssistantL1AutoReviewSettings(settings AssistantL1AutoReviewSettings, values map[string]string) (AssistantL1AutoReviewSettings, error) {
	for key, value := range values {
		value = strings.TrimSpace(value)
		switch key {
		case AssistantL1AutoReviewEnabledOptionKey:
			if value != "true" && value != "false" {
				return settings, errors.New("L1 automatic review enabled must be true or false")
			}
			settings.Enabled = value == "true"
		case AssistantL1AutoReviewModelOptionKey:
			if utf8.RuneCountInString(value) > 128 {
				return settings, errors.New("L1 automatic review model must be at most 128 characters")
			}
			settings.Model = value
		case AssistantL1AutoReviewGroupOptionKey:
			if utf8.RuneCountInString(value) > 64 {
				return settings, errors.New("L1 automatic review group must be at most 64 characters")
			}
			settings.Group = value
		case AssistantL1AutoReviewPromptOptionKey:
			if utf8.RuneCountInString(value) > 8000 {
				return settings, errors.New("L1 automatic review prompt must be at most 8000 characters")
			}
			settings.Prompt = value
		case AssistantL1AutoReviewMinConfidenceOptionKey:
			confidence, err := strconv.ParseFloat(value, 64)
			if err != nil || math.IsNaN(confidence) || math.IsInf(confidence, 0) || confidence < 0 || confidence > 1 {
				return settings, errors.New("L1 automatic review minimum confidence must be a finite number between 0 and 1")
			}
			settings.MinConfidence = confidence
		case AssistantL1AutoApprovalUserIDsOptionKey:
			ids, err := NormalizeAssistantL1AutoApprovalUserIDs(value)
			if err != nil {
				return settings, err
			}
			settings.UserIDs = ids
		}
	}
	return settings, nil
}

func (settings AssistantL1AutoReviewSettings) Ready() bool {
	if !settings.Enabled || strings.TrimSpace(settings.Model) == "" || strings.TrimSpace(settings.Group) == "" || strings.TrimSpace(settings.Prompt) == "" {
		return false
	}
	_, err := ParseAssistantL1AutoReviewSettings(settings, settings.OptionValues())
	return err == nil
}

func (settings AssistantL1AutoReviewSettings) UserAllowed(userID int) bool {
	if !settings.Ready() || userID <= 0 {
		return false
	}
	if settings.UserIDs == "" {
		return true
	}
	for _, id := range strings.Split(settings.UserIDs, ",") {
		if id == strconv.Itoa(userID) {
			return true
		}
	}
	return false
}

func GetAssistantL1AutoReviewSettings() AssistantL1AutoReviewSettings {
	assistantSettingsMutex.RLock()
	defer assistantSettingsMutex.RUnlock()
	return assistantL1AutoReviewSettingsLocked()
}

func assistantL1AutoReviewSettingsLocked() AssistantL1AutoReviewSettings {
	settings := assistantSettings.L1AutoReview
	settings.UserIDs = assistantSettings.L1AutoApprovalUserIDs
	return settings
}

func UpdateAssistantL1AutoReviewOption(key, value string) error {
	return UpdateAssistantL1AutoReviewOptions(map[string]string{key: value})
}

// UpdateAssistantL1AutoReviewOptions publishes the entire validated snapshot
// together, including the legacy trial-user allowlist.
func UpdateAssistantL1AutoReviewOptions(values map[string]string) error {
	assistantSettingsMutex.Lock()
	defer assistantSettingsMutex.Unlock()
	settings, err := ParseAssistantL1AutoReviewSettings(assistantL1AutoReviewSettingsLocked(), values)
	if err != nil {
		// Malformed persisted configuration must not leave an older reviewer on.
		assistantSettings.L1AutoReview.Enabled = false
		return err
	}
	assistantSettings.L1AutoReview = settings
	assistantSettings.L1AutoApprovalUserIDs = settings.UserIDs
	return nil
}

// WithAssistantL1AutoReviewSettings holds a read fence through the final DB
// transaction, so an in-process configuration update cannot race a decision.
func WithAssistantL1AutoReviewSettings(expected AssistantL1AutoReviewSettings, apply func() error) error {
	assistantSettingsMutex.RLock()
	defer assistantSettingsMutex.RUnlock()
	if current := assistantL1AutoReviewSettingsLocked(); current != expected || !current.Ready() {
		return errors.New("L1 automatic review configuration changed")
	}
	return apply()
}
