package model

import (
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestProfileShareOptInUsageAndRevocation(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&ProfileShare{}, &QuotaData{}))
	owner := User{Username: "badge-owner", Password: "password", AffCode: "badge-owner-aff", Status: common.UserStatusEnabled}
	other := User{Username: "other-owner", Password: "password", AffCode: "other-owner-aff", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&other).Error)

	missing, err := GetProfileShare(owner.Id)
	require.NoError(t, err)
	require.Nil(t, missing)

	share, err := EnableProfileShare(owner.Id)
	require.NoError(t, err)
	require.Len(t, share.Token, 48)
	again, err := EnableProfileShare(owner.Id)
	require.NoError(t, err)
	require.Equal(t, share.Token, again.Token)

	require.NoError(t, db.Create(&QuotaData{UserID: owner.Id, CreatedAt: 100, TokenUsed: 120, Count: 2}).Error)
	require.NoError(t, db.Create(&QuotaData{UserID: owner.Id, CreatedAt: 200, TokenUsed: 50, Count: 1}).Error)
	require.NoError(t, db.Create(&QuotaData{UserID: other.Id, CreatedAt: 200, TokenUsed: 999, Count: 8}).Error)
	usage, err := GetProfileShareUsage(owner.Id, 150)
	require.NoError(t, err)
	require.Equal(t, ProfileShareUsage{Tokens: 50, Requests: 1}, usage)
	start := time.Now().UTC().Truncate(24*time.Hour).Unix() - 370*86400
	require.NoError(t, db.Create(&QuotaData{UserID: owner.Id, CreatedAt: start + 120, TokenUsed: 25, Count: 1}).Error)
	dayRows, err := GetProfileShareYearDays(owner.Id, start, start+370*86400)
	require.NoError(t, err)
	require.Equal(t, []ProfileShareDay{{Day: start, Tokens: 25}}, dayRows)

	publicOwner, err := GetProfileShareOwner(share.Token)
	require.NoError(t, err)
	require.Equal(t, owner.Id, publicOwner.Id)

	require.NoError(t, DisableProfileShare(owner.Id))
	_, err = GetProfileShareOwner(share.Token)
	require.Error(t, err)
	newShare, err := EnableProfileShare(owner.Id)
	require.NoError(t, err)
	require.NotEqual(t, share.Token, newShare.Token)

	require.NoError(t, db.Model(&owner).Update("status", 2).Error)
	_, err = GetProfileShareOwner(newShare.Token)
	require.Error(t, err)
}
