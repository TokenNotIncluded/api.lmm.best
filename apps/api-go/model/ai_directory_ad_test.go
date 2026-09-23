/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
package model

import (
	"math"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAIDirectoryAdQuoteRoundsUpAndRejectsInvalidRates(t *testing.T) {
	previous := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = previous })
	common.QuotaPerUnit = 1000.1
	charge, err := AIDirectoryAdChargeQuota(125)
	require.NoError(t, err)
	assert.Equal(t, 1251, charge)
	common.QuotaPerUnit = math.NaN()
	_, err = AIDirectoryAdChargeQuota(100)
	assert.ErrorIs(t, err, ErrWalletQuotaOutOfRange)
}

func directoryAdInput(requestID string, bidCents int64) AIDirectoryAdInput {
	return AIDirectoryAdInput{
		Name: "Example", URL: "https://example.com", Summary: "A useful site",
		Description: "Detailed description", BidCents: bidCents, ExpectedQuota: int(bidCents) * 10, RequestID: requestID,
	}
}

func TestAIDirectoryAdChargesOnceAndRanksByPaidBid(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&AIDirectoryAd{}, &Log{}))
	previous := common.QuotaPerUnit
	common.QuotaPerUnit = 1000
	t.Cleanup(func() { common.QuotaPerUnit = previous })
	owner := User{Username: "ad-owner", Password: "password", AffCode: "ad-owner-aff", Quota: 10_000}
	require.NoError(t, db.Create(&owner).Error)

	first, created, err := CreateAIDirectoryAd(owner.Id, directoryAdInput("directory-test-request-0001", 125), 1000)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, 1250, first.ChargedQuota)
	assert.Equal(t, int64(1000+30*24*60*60), first.ExpiresAt)
	second, created, err := CreateAIDirectoryAd(owner.Id, directoryAdInput("directory-test-request-0002", 300), 1001)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, 3000, second.ChargedQuota)
	third, created, err := CreateAIDirectoryAd(owner.Id, directoryAdInput("directory-test-request-0008", 300), 1002)
	require.NoError(t, err)
	assert.True(t, created)

	replayed, created, err := CreateAIDirectoryAd(owner.Id, directoryAdInput("directory-test-request-0001", 125), 1002)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, first.ID, replayed.ID)
	var wallet User
	require.NoError(t, db.First(&wallet, owner.Id).Error)
	assert.Equal(t, 2750, wallet.Quota)

	ads, more, err := ListActiveAIDirectoryAds(1002, 0, 50)
	require.NoError(t, err)
	assert.False(t, more)
	assert.Equal(t, []int{second.ID, third.ID, first.ID}, []int{ads[0].ID, ads[1].ID, ads[2].ID})
	page, hasNext, err := ListActiveAIDirectoryAds(1002, 0, 1)
	require.NoError(t, err)
	assert.True(t, hasNext)
	assert.Equal(t, second.ID, page[0].ID)
	page, hasNext, err = ListActiveAIDirectoryAds(1002, 1, 1)
	require.NoError(t, err)
	assert.True(t, hasNext)
	assert.Equal(t, third.ID, page[0].ID)
	ads, _, err = ListActiveAIDirectoryAds(first.ExpiresAt, 0, 50)
	require.NoError(t, err)
	assert.Len(t, ads, 2)
	assert.Equal(t, second.ID, ads[0].ID)

	_, _, err = CreateAIDirectoryAd(owner.Id, directoryAdInput("directory-test-request-0001", 200), 1003)
	assert.ErrorIs(t, err, ErrAIDirectoryAdConflict)
}

func TestAIDirectoryAdRejectsLowBidUnsafeURLAndInsufficientFunds(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&AIDirectoryAd{}, &Log{}))
	previous := common.QuotaPerUnit
	common.QuotaPerUnit = 1000
	t.Cleanup(func() { common.QuotaPerUnit = previous })
	owner := User{Username: "ad-small-wallet", Password: "password", AffCode: "ad-small-wallet-aff", Quota: 999}
	require.NoError(t, db.Create(&owner).Error)

	_, _, err := CreateAIDirectoryAd(owner.Id, directoryAdInput("directory-test-request-0003", 99), 1000)
	assert.ErrorIs(t, err, ErrAIDirectoryAdInvalidBid)
	unsafe := directoryAdInput("directory-test-request-0004", 100)
	unsafe.URL = "javascript:alert(1)"
	_, _, err = CreateAIDirectoryAd(owner.Id, unsafe, 1000)
	assert.ErrorIs(t, err, ErrAIDirectoryAdInvalidInput)
	_, _, err = CreateAIDirectoryAd(owner.Id, directoryAdInput("directory-test-request-0005", 100), 1000)
	assert.ErrorIs(t, err, ErrAIDirectoryAdInsufficient)
	stale := directoryAdInput("directory-test-request-0006", 100)
	stale.ExpectedQuota = 999
	_, _, err = CreateAIDirectoryAd(owner.Id, stale, 1000)
	assert.ErrorIs(t, err, ErrAIDirectoryAdQuoteChanged)
	var count int64
	require.NoError(t, db.Model(&AIDirectoryAd{}).Count(&count).Error)
	assert.Zero(t, count)
	var wallet User
	require.NoError(t, db.First(&wallet, owner.Id).Error)
	assert.Equal(t, 999, wallet.Quota)
}

func TestHideAIDirectoryAdRefundsExactlyOnce(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&AIDirectoryAd{}, &Log{}))
	previous := common.QuotaPerUnit
	common.QuotaPerUnit = 1000
	t.Cleanup(func() { common.QuotaPerUnit = previous })
	owner := User{Username: "ad-refund-owner", Password: "password", AffCode: "ad-refund-owner-aff", Quota: 2000}
	require.NoError(t, db.Create(&owner).Error)
	ad, created, err := CreateAIDirectoryAd(owner.Id, directoryAdInput("directory-test-request-0007", 100), 1000)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, 1000, ad.ChargedQuota)

	hidden, refunded, err := HideAIDirectoryAd(ad.ID, 1001)
	require.NoError(t, err)
	assert.True(t, refunded)
	assert.Equal(t, AIDirectoryAdStatusHidden, hidden.Status)
	_, refunded, err = HideAIDirectoryAd(ad.ID, 1002)
	require.NoError(t, err)
	assert.False(t, refunded)
	var wallet User
	require.NoError(t, db.First(&wallet, owner.Id).Error)
	assert.Equal(t, 2000, wallet.Quota)
	ads, _, err := ListActiveAIDirectoryAds(1002, 0, 50)
	require.NoError(t, err)
	assert.Empty(t, ads)
}
