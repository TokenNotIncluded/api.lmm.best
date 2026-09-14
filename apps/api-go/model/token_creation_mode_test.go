package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenCreationModeFiltersPaginationAndSearch(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	const userID = 71
	tokens := []Token{
		{UserId: userID, Key: "manual-key", Name: "shared manual", CreationSource: TokenCreationSourceManual},
		{UserId: userID, Key: "drawing-key", Name: "shared drawing", CreationSource: TokenCreationSourceDrawingMCP},
		{UserId: userID, Key: "assistant-key", Name: "shared assistant", CreationSource: TokenCreationSourceAssistant},
		{UserId: 72, Key: "other-user-key", Name: "shared other", CreationSource: TokenCreationSourceDrawingMCP},
	}
	for index := range tokens {
		require.NoError(t, db.Create(&tokens[index]).Error)
	}

	manual, err := GetUserTokensByCreationMode(userID, 0, 10, TokenCreationModeManual)
	require.NoError(t, err)
	require.Len(t, manual, 1)
	assert.Equal(t, "manual-key", manual[0].Key)

	automatic, err := GetUserTokensByCreationMode(userID, 0, 1, TokenCreationModeAutomatic)
	require.NoError(t, err)
	require.Len(t, automatic, 1)
	assert.Equal(t, "assistant-key", automatic[0].Key)

	automaticCount, err := CountUserTokensByCreationMode(userID, TokenCreationModeAutomatic)
	require.NoError(t, err)
	assert.EqualValues(t, 2, automaticCount)

	results, total, err := SearchUserTokensByCreationMode(
		userID, "%shared%", "", 0, 10, TokenCreationModeAutomatic,
	)
	require.NoError(t, err)
	assert.Len(t, results, 2)
	assert.EqualValues(t, 2, total)

	_, err = GetUserTokensByCreationMode(userID, 0, 10, "browser-forged-source")
	assert.Error(t, err)
}

func TestBackfillTokenCreationSourcesRepairsOnlyLegacyDrawingKeys(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	tokens := []Token{
		{UserId: 81, Key: "legacy-drawing", Name: "drawing-image-2", Group: DrawingTokenGroup, UnlimitedQuota: true, ExpiredTime: -1, CreationSource: TokenCreationSourceManual},
		{UserId: 81, Key: "same-name-other-group", Name: "drawing-image-2", Group: "default", UnlimitedQuota: true, ExpiredTime: -1, CreationSource: TokenCreationSourceManual},
		{UserId: 81, Key: "custom-name-same-group", Name: "my-drawing-key", Group: DrawingTokenGroup, UnlimitedQuota: true, ExpiredTime: -1, CreationSource: TokenCreationSourceManual},
		{UserId: 81, Key: "oauth-drawing", Name: "drawing-image-2", Group: DrawingTokenGroup, UnlimitedQuota: true, ExpiredTime: -1, OAuthManaged: true, CreationSource: TokenCreationSourceManual},
	}
	for index := range tokens {
		require.NoError(t, db.Create(&tokens[index]).Error)
	}

	require.NoError(t, backfillTokenCreationSources(db))
	require.NoError(t, backfillTokenCreationSources(db), "the data migration must be idempotent")

	var stored []Token
	require.NoError(t, db.Order("id ASC").Find(&stored).Error)
	require.Len(t, stored, 4)
	assert.Equal(t, TokenCreationSourceDrawingMCP, stored[0].CreationSource)
	assert.Equal(t, TokenCreationSourceManual, stored[1].CreationSource)
	assert.Equal(t, TokenCreationSourceManual, stored[2].CreationSource)
	assert.Equal(t, TokenCreationSourceManual, stored[3].CreationSource)

	automatic, err := GetUserTokensByCreationMode(81, 0, 10, TokenCreationModeAutomatic)
	require.NoError(t, err)
	require.Len(t, automatic, 1)
	assert.Equal(t, "legacy-drawing", automatic[0].Key)
}
