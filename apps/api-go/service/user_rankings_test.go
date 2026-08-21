package service

import (
	"context"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetUserUsageRankingsSnapshotHonorsVisibility(t *testing.T) {
	resetUserUsageRankingCache(t)
	require.NoError(t, model.DB.AutoMigrate(&model.QuotaData{}))
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM quota_data")
		model.DB.Exec("DELETE FROM users")
	})

	anonymous := model.User{
		Id:       101,
		Username: "anonymous-user",
		AffCode:  "anonymous-user-aff",
		Status:   common.UserStatusEnabled,
	}
	public := model.User{
		Id:          102,
		Username:    "public-user",
		AffCode:     "public-user-aff",
		DisplayName: "Public user",
		Status:      common.UserStatusEnabled,
	}
	public.SetSetting(dto.UserSetting{
		UsageLeaderboardVisibility: dto.UsageLeaderboardVisibilityPublic,
	})
	hidden := model.User{
		Id:       103,
		Username: "hidden-user",
		AffCode:  "hidden-user-aff",
		Status:   common.UserStatusEnabled,
	}
	hidden.SetSetting(dto.UserSetting{
		UsageLeaderboardVisibility: dto.UsageLeaderboardVisibilityHidden,
	})
	disabled := model.User{
		Id:       104,
		Username: "disabled-user",
		AffCode:  "disabled-user-aff",
		Status:   common.UserStatusDisabled,
	}
	anonymousTwo := model.User{
		Id:       105,
		Username: "anonymous-user-two",
		AffCode:  "anonymous-user-two-aff",
		Status:   common.UserStatusEnabled,
	}

	require.NoError(t, model.DB.Create(&[]model.User{anonymous, public, hidden, disabled, anonymousTwo}).Error)
	createdAt := time.Now().Add(-time.Hour).Unix()
	require.NoError(t, model.DB.Create(&[]model.QuotaData{
		{UserID: anonymous.Id, CreatedAt: createdAt, Count: 2, TokenUsed: 100},
		{UserID: public.Id, CreatedAt: createdAt, Count: 4, TokenUsed: 300},
		{UserID: hidden.Id, CreatedAt: createdAt, Count: 10, TokenUsed: 1000},
		{UserID: disabled.Id, CreatedAt: createdAt, Count: 8, TokenUsed: 500},
		{UserID: 105, CreatedAt: createdAt, Count: 1, TokenUsed: 50},
	}).Error)

	result, err := GetUserUsageRankingsSnapshot(context.Background(), "week")
	require.NoError(t, err)
	assert.Equal(t, "week", result.Period)
	assert.Equal(t, int64(450), result.TotalTokens)
	assert.Equal(t, int64(7), result.TotalRequests)
	assert.Equal(t, 3, result.ParticipantCount)
	assert.Equal(t, 2, result.AnonymousParticipantCount)
	require.Len(t, result.Users, 3)

	assert.Equal(t, 1, result.Users[0].Rank)
	assert.Equal(t, "Public user", result.Users[0].Name)
	assert.False(t, result.Users[0].Anonymous)
	assert.Equal(t, int64(300), result.Users[0].TotalTokens)
	assert.Equal(t, int64(4), result.Users[0].Requests)
	assert.InDelta(t, 2.0/3.0, result.Users[0].Share, 0.0001)

	assert.Equal(t, 2, result.Users[1].Rank)
	assert.Empty(t, result.Users[1].Name)
	assert.True(t, result.Users[1].Anonymous)
	assert.Equal(t, int64(100), result.Users[1].TotalTokens)
	assert.Equal(t, int64(2), result.Users[1].Requests)
	assert.InDelta(t, 2.0/9.0, result.Users[1].Share, 0.0001)

	assert.Equal(t, 3, result.Users[2].Rank)
	assert.Empty(t, result.Users[2].Name)
	assert.True(t, result.Users[2].Anonymous)
	assert.Equal(t, int64(50), result.Users[2].TotalTokens)
	assert.Equal(t, int64(1), result.Users[2].Requests)
	assert.InDelta(t, 1.0/9.0, result.Users[2].Share, 0.0001)

	publicSettings := public.GetSetting()
	publicSettings.UsageLeaderboardVisibility = dto.UsageLeaderboardVisibilityHidden
	require.NoError(t, model.UpdateUserSetting(public.Id, publicSettings))

	privateResult, err := GetUserUsageRankingsSnapshot(context.Background(), "week")
	require.NoError(t, err)
	assert.NotSame(t, result, privateResult)
	assert.Equal(t, int64(150), privateResult.TotalTokens)
	assert.Equal(t, int64(3), privateResult.TotalRequests)
	assert.Equal(t, 2, privateResult.ParticipantCount)
	assert.Equal(t, 2, privateResult.AnonymousParticipantCount)
	require.Len(t, privateResult.Users, 2)
	assert.NotContains(t, []string{privateResult.Users[0].Name, privateResult.Users[1].Name}, "Public user")

	publicSettings.UsageLeaderboardVisibility = dto.UsageLeaderboardVisibilityPublic
	require.NoError(t, model.UpdateUserSetting(public.Id, publicSettings))
	publicAgain, err := GetUserUsageRankingsSnapshot(context.Background(), "week")
	require.NoError(t, err)
	require.Len(t, publicAgain.Users, 3)
	assert.Equal(t, "Public user", publicAgain.Users[0].Name)

	publicSettings.UsageLeaderboardVisibility = dto.UsageLeaderboardVisibilityHidden
	require.NoError(t, model.UpdateUserSetting(public.Id, publicSettings))
	hiddenAgain, err := GetUserUsageRankingsSnapshot(context.Background(), "week")
	require.NoError(t, err)
	require.Len(t, hiddenAgain.Users, 2)
	assert.NotContains(t, []string{hiddenAgain.Users[0].Name, hiddenAgain.Users[1].Name}, "Public user")
}

func TestGetUserUsageRankingsSnapshotRejectsStaleSharedFlight(t *testing.T) {
	resetUserUsageRankingCache(t)
	require.NoError(t, model.DB.AutoMigrate(&model.QuotaData{}))
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM quota_data")
		model.DB.Exec("DELETE FROM users")
	})

	public := model.User{
		Id:          201,
		Username:    "stale-flight-public-user",
		AffCode:     "stale-flight-public-user-aff",
		DisplayName: "Stale flight public user",
		Status:      common.UserStatusEnabled,
	}
	public.SetSetting(dto.UserSetting{UsageLeaderboardVisibility: dto.UsageLeaderboardVisibilityPublic})
	require.NoError(t, model.DB.Create(&public).Error)
	require.NoError(t, model.DB.Create(&model.QuotaData{
		UserID: public.Id, CreatedAt: time.Now().Add(-time.Hour).Unix(), Count: 1, TokenUsed: 100,
	}).Error)

	oldRevision, err := model.CurrentUserRankingRevision(context.Background())
	require.NoError(t, err)
	staleFlight := userUsageRankingFlightResult{
		revision: oldRevision,
		data: &UserUsageRankingsResponse{
			Period: "week",
			Users:  []RankedUserUsage{{Rank: 1, Name: public.DisplayName, TotalTokens: 100}},
		},
	}
	settings := public.GetSetting()
	settings.UsageLeaderboardVisibility = dto.UsageLeaderboardVisibilityHidden
	require.NoError(t, model.UpdateUserSetting(public.Id, settings))
	current, err := userUsageRankingFlightIsCurrent(context.Background(), staleFlight)
	require.NoError(t, err)
	assert.False(t, current, "a request after privacy revocation must reject an older shared-flight result")

	result, err := GetUserUsageRankingsSnapshot(context.Background(), "week")
	require.NoError(t, err)
	assert.Empty(t, result.Users)
	assert.Zero(t, result.TotalTokens)
	assert.Zero(t, result.ParticipantCount)
}

func TestGetUserUsageRankingsSnapshotCanceledCallerDoesNotCancelSharedBuild(t *testing.T) {
	resetUserUsageRankingCache(t)
	require.NoError(t, model.DB.AutoMigrate(&model.QuotaData{}))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := GetUserUsageRankingsSnapshot(canceled, "month")
	require.ErrorIs(t, err, context.Canceled)
	result, err := GetUserUsageRankingsSnapshot(context.Background(), "month")
	require.NoError(t, err)
	assert.Equal(t, "month", result.Period)
}

func TestGetUserUsageRankingsSnapshotCachesPeriod(t *testing.T) {
	resetUserUsageRankingCache(t)
	ctx := context.Background()
	revision, err := model.CurrentUserRankingRevision(ctx)
	require.NoError(t, err)

	cached := &UserUsageRankingsResponse{Period: "year", UpdatedAt: 123}
	userUsageRankingCacheMu.Lock()
	userUsageRankingCache["year"] = userUsageRankingCacheItem{
		expiresAt: time.Now().Add(time.Minute),
		revision:  revision,
		data:      cached,
	}
	userUsageRankingCacheMu.Unlock()

	result, err := GetUserUsageRankingsSnapshot(ctx, "year")
	require.NoError(t, err)
	assert.Same(t, cached, result)
}

func TestGetUserUsageRankingsSnapshotIgnoresExpiredCache(t *testing.T) {
	resetUserUsageRankingCache(t)
	require.NoError(t, model.DB.AutoMigrate(&model.QuotaData{}))
	ctx := context.Background()
	revision, err := model.CurrentUserRankingRevision(ctx)
	require.NoError(t, err)

	expired := &UserUsageRankingsResponse{Period: "today", UpdatedAt: 123}
	userUsageRankingCacheMu.Lock()
	userUsageRankingCache["today"] = userUsageRankingCacheItem{
		expiresAt: time.Now().Add(-time.Minute),
		revision:  revision,
		data:      expired,
	}
	userUsageRankingCacheMu.Unlock()

	result, err := GetUserUsageRankingsSnapshot(ctx, "today")
	require.NoError(t, err)
	assert.NotSame(t, expired, result)

	userUsageRankingCacheMu.RLock()
	cached := userUsageRankingCache["today"]
	userUsageRankingCacheMu.RUnlock()
	assert.Same(t, result, cached.data)
	assert.True(t, cached.expiresAt.After(time.Now()))
	assert.Equal(t, revision, cached.revision)
}

func resetUserUsageRankingCache(t *testing.T) {
	t.Helper()
	require.NoError(t, model.EnsureUserRankingRevisionState(model.DB))
	require.NoError(t, model.RegisterUserRankingRevisionCallbacks(model.DB))
	userUsageRankingCacheMu.Lock()
	userUsageRankingCache = map[string]userUsageRankingCacheItem{}
	userUsageRankingCacheMu.Unlock()
	for _, period := range []string{"today", "week", "month", "year"} {
		userUsageRankingFlights.Forget(period)
	}
	t.Cleanup(func() {
		userUsageRankingCacheMu.Lock()
		userUsageRankingCache = map[string]userUsageRankingCacheItem{}
		userUsageRankingCacheMu.Unlock()
		for _, period := range []string{"today", "week", "month", "year"} {
			userUsageRankingFlights.Forget(period)
		}
	})
}
