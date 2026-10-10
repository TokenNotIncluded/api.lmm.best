package model

import (
	"context"
	"errors"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func withInviteRegistrationOption(t *testing.T, value string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	previous := common.OptionMap
	common.OptionMap = map[string]string{operation_setting.InviteRegistrationEnabledOptionKey: value}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previous
		common.OptionMapRWMutex.Unlock()
	})
}

func TestInviteRegistrationOptionRequiresExactBooleanAndCanBeDisabled(t *testing.T) {
	setupPriceLockTest(t)
	settings := operation_setting.GetDeveloperAccessSetting()
	previousSettings := *settings
	t.Cleanup(func() { *settings = previousSettings })
	key := operation_setting.InviteRegistrationEnabledOptionKey

	require.False(t, inviteRegistrationActivationEnabled(), "an absent option defaults to disabled")
	require.NoError(t, UpdateOption(key, "true"))
	require.True(t, settings.InviteRegistrationEnabled)
	require.True(t, inviteRegistrationActivationEnabled())
	for _, invalid := range []string{"", "1", "TRUE", "yes", " true "} {
		require.Error(t, UpdateOption(key, invalid))
		require.Equal(t, "true", persistedPriceOption(t, key))
		require.True(t, inviteRegistrationActivationEnabled(), "invalid saves must not change the valid option")
	}
	require.Error(t, UpdateOptionsBulk(map[string]string{key: "false", "ReferralPenaltyPercent": "101"}))
	require.True(t, inviteRegistrationActivationEnabled(), "an invalid bulk save must not partially disable access")

	require.NoError(t, UpdateOption(key, "false"))
	require.False(t, settings.InviteRegistrationEnabled)
	require.False(t, inviteRegistrationActivationEnabled())
	require.NoError(t, UpdateOption(key, "true"))
	require.NoError(t, DB.Where("key = ?", key).Delete(&Option{}).Error)
	_, err := RefreshOptionsSnapshot(context.Background())
	require.NoError(t, err)
	require.False(t, inviteRegistrationActivationEnabled(), "a deleted option must not leave stale authorization enabled")
}

func TestInviteRegistrationMalformedPublishedOptionFailsClosed(t *testing.T) {
	for _, value := range []string{"", "false", "1", "TRUE", "garbage"} {
		t.Run(value, func(t *testing.T) {
			withInviteRegistrationOption(t, value)
			require.False(t, inviteRegistrationActivationEnabled())
		})
	}
}

func TestInviteRegistrationActivationIsDurableAndOnlyAppliesAtCreation(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	preserveRegistrationRewardSettings(t)
	withDeveloperAccessSetting(t, false, 1)
	withInviteRegistrationOption(t, "false")
	inviter := User{Username: "invite-activation-inviter", AffCode: "invite-l1", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&inviter).Error)
	existing := User{Username: "old-invited-l0", AffCode: "old-l0", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return existing.InsertWithTx(tx, inviter.Id) }))
	require.Zero(t, existing.ConsoleActivatedAt)

	common.OptionMapRWMutex.Lock()
	common.OptionMap[operation_setting.InviteRegistrationEnabledOptionKey] = "true"
	common.OptionMapRWMutex.Unlock()
	// Turning the switch on does not reinterpret an older invitation as a grant.
	require.NoError(t, db.First(&existing, existing.Id).Error)
	oldAccess, err := GetFreshUserAccessSnapshot(&existing)
	require.NoError(t, err)
	require.False(t, oldAccess.DeveloperAccess.Granted)
	require.Equal(t, TrustLevelMinUser, oldAccess.TrustLevel.Level)

	invited := User{Username: "new-invited-l1", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return invited.InsertWithTx(tx, inviter.Id) }))
	require.NoError(t, db.First(&invited, invited.Id).Error)
	require.Equal(t, inviter.Id, invited.InviterId)
	require.Positive(t, invited.ConsoleActivatedAt)
	require.Nil(t, invited.TrustLevelOverride, "activation must not cap future progression with an override")
	require.Equal(t, 100, invited.Quota, "only the existing registration gift is credited")
	access, err := GetFreshUserAccessSnapshot(&invited)
	require.NoError(t, err)
	require.True(t, access.DeveloperAccess.Granted)
	require.False(t, access.PaidActivationComplete, "an invitation must not manufacture payment history")
	require.Equal(t, TrustLevelMinUser+1, access.TrustLevel.Level)
	require.Zero(t, access.TrustLevel.PaidAmount)

	common.OptionMapRWMutex.Lock()
	common.OptionMap[operation_setting.InviteRegistrationEnabledOptionKey] = "false"
	common.OptionMapRWMutex.Unlock()
	access, err = GetFreshUserAccessSnapshot(&invited)
	require.NoError(t, err)
	require.True(t, access.DeveloperAccess.Granted, "disabling future invitation grants must not revoke an existing activation")
	later := User{Username: "later-invited-l0", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return later.InsertWithTx(tx, inviter.Id) }))
	require.Zero(t, later.ConsoleActivatedAt)

	// Invitations establish the L1 floor without changing paid progression.
	require.NoError(t, db.Create(creditedTopUp(invited.Id, "invited-l2-payment", 100)).Error)
	access, err = GetFreshUserAccessSnapshot(&invited)
	require.NoError(t, err)
	require.Equal(t, TrustLevelMinUser+2, access.TrustLevel.Level)
	var payments int64
	require.NoError(t, db.Model(&TopUp{}).Where("user_id = ?", existing.Id).Count(&payments).Error)
	require.Zero(t, payments)
}

func TestInviteRegistrationRollbackDoesNotPersistActivationOrInviterCounter(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	preserveRegistrationRewardSettings(t)
	withInviteRegistrationOption(t, "true")
	inviter := User{Username: "rollback-inviter", AffCode: "rollback-aff", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&inviter).Error)
	invitee := User{Username: "rollback-invitee", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	failedBinding := errors.New("oauth binding or initial credential failed")
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := invitee.InsertWithTx(tx, inviter.Id); err != nil {
			return err
		}
		require.Positive(t, invitee.ConsoleActivatedAt)
		return failedBinding
	})
	require.ErrorIs(t, err, failedBinding)
	var count int64
	require.NoError(t, db.Model(&User{}).Where("username = ?", invitee.Username).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.First(&inviter, inviter.Id).Error)
	require.Zero(t, inviter.AffCount)
}
