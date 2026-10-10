package model

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestProfileShareAggregateSettingsRoundTripIndependentPermissionsAndRevocation(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&ProfileShare{}, &QuotaData{}))
	share, err := EnableProfileShare(101)
	require.NoError(t, err)
	require.False(t, share.AggregateUsageEnabled)
	require.Nil(t, share.LinkedProfiles)
	enabled, disabled := true, false
	tokens := int64(0)
	profiles := []ProfileLinkedProfile{{Provider: "chatgpt", URL: "https://chatgpt.com/u/owner", Snapshot: &ProfileUsageSnapshot{Tokens: &tokens, Period: "all", ObservedAt: "2026-10-03T18:46:00Z", Approximate: true, Source: "Owner snapshot"}}}
	updated, err := SetProfileShareSettings(share, nil, &enabled, &profiles)
	require.NoError(t, err)
	require.False(t, updated.ModelUsageEnabled)
	require.True(t, updated.AggregateUsageEnabled)
	require.Equal(t, share.Token, updated.Token)
	require.Equal(t, profiles, updated.LinkedProfiles)
	require.Nil(t, updated.LinkedProfiles[0].Snapshot.Requests)
	updated, err = SetProfileShareSettings(share, &enabled, nil, nil)
	require.NoError(t, err)
	require.True(t, updated.ModelUsageEnabled)
	require.True(t, updated.AggregateUsageEnabled)
	require.Equal(t, profiles, updated.LinkedProfiles)
	updated, err = SetProfileShareSettings(share, nil, &disabled, nil)
	require.NoError(t, err)
	require.True(t, updated.ModelUsageEnabled)
	require.False(t, updated.AggregateUsageEnabled)
	require.Equal(t, profiles, updated.LinkedProfiles)
	require.NoError(t, DisableProfileShare(share.UserID))
	fresh, err := EnableProfileShare(share.UserID)
	require.NoError(t, err)
	require.NotEqual(t, share.Token, fresh.Token)
	_, err = SetProfileShareSettings(share, &enabled, &enabled, &profiles)
	require.Error(t, err)
	fresh, err = GetProfileShare(share.UserID)
	require.NoError(t, err)
	require.False(t, fresh.AggregateUsageEnabled)
	require.False(t, fresh.ModelUsageEnabled)
	require.Empty(t, fresh.LinkedProfiles)
	require.NoError(t, db.Create(&QuotaData{UserID: 101, CreatedAt: 100, TokenUsed: 42, Count: 2}).Error)
	require.NoError(t, db.Create(&QuotaData{UserID: 101, CreatedAt: 101, TokenUsed: -10, Count: -1}).Error)
	require.NoError(t, db.Create(&QuotaData{UserID: 101, CreatedAt: 1000, TokenUsed: 500, Count: 5}).Error)
	require.NoError(t, db.Create(&QuotaData{UserID: 102, CreatedAt: 100, TokenUsed: 1000, Count: 9}).Error)
	usage, err := GetProfileShareAggregateUsage(101, 0, 200)
	require.NoError(t, err)
	require.Equal(t, ProfileShareUsage{Tokens: 42, Requests: 2}, usage)
}
