package model

import (
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantMemoryRecallIsStrictlyOwnerScoped(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&AssistantMemory{}))
	first := User{Username: "memory-first", Password: "password", Role: common.RoleCommonUser, AffCode: "memory-first"}
	second := User{Username: "memory-second", Password: "password", Role: common.RoleCommonUser, AffCode: "memory-second"}
	require.NoError(t, db.Create(&first).Error)
	require.NoError(t, db.Create(&second).Error)

	firstMemory, err := SaveMemory(first.Id, first.Id, MemoryInput{Title: "Coding client", Content: "Uses Hermes for coding work.", Tags: []string{"hermes"}, Source: AssistantMemorySourceAssistant, Enabled: true})
	require.NoError(t, err)
	_, err = SaveMemory(second.Id, second.Id, MemoryInput{Title: "Coding client", Content: "Uses a different private client.", Tags: []string{"private"}, Source: AssistantMemorySourceAssistant, Enabled: true})
	require.NoError(t, err)

	recalled, err := RecallMemories(first.Id, "Hermes", 4)
	require.NoError(t, err)
	require.Len(t, recalled, 1)
	assert.Equal(t, firstMemory.Id, recalled[0].Id)
	assert.NotContains(t, recalled[0].Content, "different")

	secondRecall, err := RecallMemories(second.Id, "Hermes", 4)
	require.NoError(t, err)
	assert.Empty(t, secondRecall)
}

func TestAssistantMemoryRedactsSecretsAndUpdatesSameTitle(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&AssistantMemory{}))
	user := User{Username: "memory-redaction", Password: "password", Role: common.RoleCommonUser}
	require.NoError(t, db.Create(&user).Error)

	first, err := SaveMemory(user.Id, user.Id, MemoryInput{Title: "API setup", Content: "Uses key sk-secret-token-123456789 for testing.", Tags: []string{"api"}, Source: AssistantMemorySourceAssistant, Enabled: true})
	require.NoError(t, err)
	assert.NotContains(t, first.Content, "secret-token")
	second, err := SaveMemory(user.Id, user.Id, MemoryInput{Title: "API setup", Content: "Uses an OpenAI-compatible client.", Tags: []string{"client"}, Source: AssistantMemorySourceAssistant, Enabled: true})
	require.NoError(t, err)
	assert.Equal(t, first.Id, second.Id)

	var count int64
	require.NoError(t, db.Model(&AssistantMemory{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestAssistantMemoryViewsRedactLegacyRows(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&AssistantMemory{}))
	user := User{Username: "memory-legacy-redaction", Password: "password", Role: common.RoleCommonUser, AffCode: "memory-legacy-redaction"}
	require.NoError(t, db.Create(&user).Error)
	legacy := AssistantMemory{
		UserId:  user.Id,
		Title:   "Legacy password: hunter2",
		Content: "Contact old.user@example.com with key=sk_legacy_secret_123456 and card 4111 1111 1111 1111.", // gitleaks:allow
		TagsJSON: `[
  "api_key: sk_legacy_tag_secret",
  "legacy"
]`,
		Source:  AssistantMemorySourceAssistant,
		Enabled: true,
	}
	require.NoError(t, db.Create(&legacy).Error)

	views, err := ListMemories(user.Id, true)
	require.NoError(t, err)
	require.Len(t, views, 1)
	view := views[0]
	serialized := view.Title + "\n" + view.Content + "\n" + strings.Join(view.Tags, ",")
	for _, secret := range []string{"hunter2", "old.user@example.com", "sk_legacy_secret_123456", "sk_legacy_tag_secret", "4111 1111 1111 1111"} {
		assert.NotContains(t, serialized, secret)
	}
	assert.Contains(t, serialized, "[REDACTED]")
	assert.Contains(t, serialized, "[REDACTED_EMAIL]")
	assert.Contains(t, serialized, "[REDACTED_CARD]")
}
