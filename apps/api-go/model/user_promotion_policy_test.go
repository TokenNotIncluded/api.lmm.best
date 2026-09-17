package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func preserveRegistrationRewardSettings(t *testing.T) {
	t.Helper()
	oldNewUser := common.QuotaForNewUser
	oldInviter := common.QuotaForInviter
	oldInvitee := common.QuotaForInvitee
	payment := operation_setting.GetPaymentSetting()
	oldCompliance := payment.ComplianceConfirmed
	oldTerms := payment.ComplianceTermsVersion
	common.QuotaForNewUser = 100
	common.QuotaForInviter = 200
	common.QuotaForInvitee = 300
	payment.ComplianceConfirmed = true
	payment.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	t.Cleanup(func() {
		common.QuotaForNewUser = oldNewUser
		common.QuotaForInviter = oldInviter
		common.QuotaForInvitee = oldInvitee
		payment.ComplianceConfirmed = oldCompliance
		payment.ComplianceTermsVersion = oldTerms
	})
}

func TestInsertSuppressesPromotionalCreditsForDisposableEmail(t *testing.T) {
	setupUserUpdateTestState(t)
	preserveRegistrationRewardSettings(t)

	inviter := User{
		Username: "durable-inviter",
		Email:    "inviter@example.com",
		Password: "password",
		AffCode:  "durable-inviter-aff",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(&inviter).Error)

	disposable := &User{
		Username: "throwaway-signup",
		Email:    "person@mailinator.com",
		Password: "password",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, disposable.Insert(inviter.Id))

	var stored User
	require.NoError(t, DB.First(&stored, disposable.Id).Error)
	assert.Zero(t, stored.Quota)
	assert.Zero(t, stored.AffQuota)
	assert.Zero(t, stored.AffCount)

	var storedInviter User
	require.NoError(t, DB.First(&storedInviter, inviter.Id).Error)
	assert.Zero(t, storedInviter.AffQuota)
	assert.Zero(t, storedInviter.AffCount)
}

func TestInsertKeepsRegistrationGiftButDefersReferralReward(t *testing.T) {
	setupUserUpdateTestState(t)
	preserveRegistrationRewardSettings(t)

	inviter := User{
		Username: "durable-inviter-two",
		Email:    "inviter-two@example.com",
		Password: "password",
		AffCode:  "durable-inviter-two-aff",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(&inviter).Error)

	durable := &User{
		Username: "durable-signup",
		Email:    "person@example.com",
		Password: "password",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, durable.Insert(inviter.Id))

	var stored User
	require.NoError(t, DB.First(&stored, durable.Id).Error)
	assert.Equal(t, 100, stored.Quota)
	assert.Equal(t, inviter.Id, stored.InviterId)

	var storedInviter User
	require.NoError(t, DB.First(&storedInviter, inviter.Id).Error)
	assert.Zero(t, storedInviter.AffQuota)
	assert.Equal(t, 1, storedInviter.AffCount)
}

func TestInsertWithTxSuppressesPromotionalCreditsForDisposableOAuthEmail(t *testing.T) {
	setupUserUpdateTestState(t)
	preserveRegistrationRewardSettings(t)

	inviter := User{
		Username: "oauth-durable-inviter",
		Email:    "oauth-inviter@example.com",
		Password: "password",
		AffCode:  "oauth-durable-inviter-aff",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(&inviter).Error)

	disposable := &User{
		Username: "oauth-throwaway-signup",
		Email:    "oauth-person@yopmail.com",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}
	tx := DB.Begin()
	require.NoError(t, tx.Error)
	require.NoError(t, disposable.InsertWithTx(tx, inviter.Id))
	require.NoError(t, tx.Commit().Error)
	disposable.FinalizeOAuthUserCreation(inviter.Id)

	var stored User
	require.NoError(t, DB.First(&stored, disposable.Id).Error)
	assert.Zero(t, stored.Quota)
	assert.Zero(t, stored.AffQuota)

	var storedInviter User
	require.NoError(t, DB.First(&storedInviter, inviter.Id).Error)
	assert.Zero(t, storedInviter.AffQuota)
	assert.Zero(t, storedInviter.AffCount)
}
