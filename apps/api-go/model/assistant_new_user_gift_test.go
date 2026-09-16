package model

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAssistantGiftTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := DB
	previousRedis := common.RedisEnabled
	previousQuotaPerUnit := common.QuotaPerUnit
	previousLocalAcceptance := LocalAcceptanceDeveloperAccessEnabled()
	common.RedisEnabled = false
	common.QuotaPerUnit = 500_000
	SetLocalAcceptanceDeveloperAccess(false)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	require.NoError(t, db.AutoMigrate(RegistrationGuardMigrationModels()...))
	require.NoError(t, db.AutoMigrate(&User{}, &TopUp{}, &AssistantNewUserGift{}, &AssistantGiftRiskKey{}, &AssistantGiftRiskMemory{}))
	t.Cleanup(func() {
		DB = previousDB
		common.RedisEnabled = previousRedis
		common.QuotaPerUnit = previousQuotaPerUnit
		SetLocalAcceptanceDeveloperAccess(previousLocalAcceptance)
	})
	return db
}

func newAssistantGiftUser(t *testing.T, db *gorm.DB, username, email string) User {
	t.Helper()
	level := TrustLevelMinUser
	user := User{
		Username: username, Email: email, Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, CreatedAt: time.Now().Add(-24 * time.Hour).Unix(),
		TrustLevelOverride: &level, AffCode: username + "-aff",
	}
	require.NoError(t, db.Create(&user).Error)
	if canonicalAssistantGiftEmail(email) != "" {
		require.NoError(t, ObserveAssistantRegistration(user.Id, "198.51.100.10", ""))
	}
	return user
}

func TestAssistantNewUserGiftIsOneTimeAndClaimIsIdempotent(t *testing.T) {
	db := setupAssistantGiftTestDB(t)
	user := newAssistantGiftUser(t, db, "gift-user", "gift@example.com")

	gift, created, err := DecideAssistantNewUserGift(user.Id, 7, 525, "Clear and constructive project details.", 2, 40, "198.51.100.10")
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, AssistantGiftOffered, gift.Status)
	assert.Equal(t, 2_625_000, gift.Quota)

	again, created, err := DecideAssistantNewUserGift(user.Id, 8, 1000, "A retry must not replace the first decision.", 3, 60, "198.51.100.11")
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, gift.Id, again.Id)
	assert.Equal(t, 525, again.AmountCents)

	claimed, alreadyClaimed, err := ClaimAssistantNewUserGift(user.Id)
	require.NoError(t, err)
	assert.False(t, alreadyClaimed)
	assert.Equal(t, AssistantGiftClaimed, claimed.Status)
	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, gift.Quota, stored.Quota)

	_, alreadyClaimed, err = ClaimAssistantNewUserGift(user.Id)
	require.NoError(t, err)
	assert.True(t, alreadyClaimed)
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, gift.Quota, stored.Quota)
}

func TestAssistantGiftRiskKeySurvivesCryptoSecretRotation(t *testing.T) {
	db := setupAssistantGiftTestDB(t)
	previousCryptoSecret := common.CryptoSecret
	t.Cleanup(func() { common.CryptoSecret = previousCryptoSecret })
	common.CryptoSecret = "gift-test-instance-a"

	first := newAssistantGiftUser(t, db, "gift-rotation-first", "rotation.first+one@gmail.com")
	_, _, err := DecideAssistantNewUserGift(first.Id, 1, 100, "A legitimate first conversation.", 2, 40, "198.51.100.30")
	require.NoError(t, err)

	// A restart or a second instance may have a different process-local
	// CryptoSecret. The persisted installation key must keep the global
	// identity ledger stable across that boundary.
	common.CryptoSecret = "gift-test-instance-b"
	alias := newAssistantGiftUser(t, db, "gift-rotation-alias", "rotationfirst+two@googlemail.com")
	_, _, err = DecideAssistantNewUserGift(alias.Id, 2, 100, "The same mailbox under another alias.", 2, 40, "198.51.100.31")
	assert.ErrorIs(t, err, ErrAssistantGiftAbuse)
	assert.Equal(t, "identity_already_used", AssistantGiftErrorCode(err))

	var keys []AssistantGiftRiskKey
	require.NoError(t, db.Find(&keys).Error)
	require.Len(t, keys, 1)
	assert.Equal(t, assistantGiftRiskKeyID, keys[0].Id)
	assert.Equal(t, "gift-test-instance-a", keys[0].Secret)
}

func TestAssistantGiftOpportunitySurvivesL1AndOlderAccounts(t *testing.T) {
	db := setupAssistantGiftTestDB(t)
	levelOne := TrustLevelMinUser + 1
	user := newAssistantGiftUser(t, db, "l1-gift-user", "l1-gift@example.com")
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]any{
		"created_at":           time.Now().Add(-365 * 24 * time.Hour).Unix(),
		"trust_level_override": levelOne,
	}).Error)

	gift, created, err := DecideAssistantNewUserGift(user.Id, 9, 100, "An L1 user with an unused welcome-gift opportunity.", 2, 52, "198.51.100.22")
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, AssistantGiftOffered, gift.Status)
}

func TestAssistantNewUserGiftClaimDoesNotConsumeGiftForDisabledUser(t *testing.T) {
	db := setupAssistantGiftTestDB(t)
	user := newAssistantGiftUser(t, db, "disabled-gift-user", "disabled-gift@example.com")
	gift, created, err := DecideAssistantNewUserGift(user.Id, 10, 250, "A legitimate use before the account was disabled.", 2, 40, "198.51.100.24")
	require.NoError(t, err)
	assert.True(t, created)

	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("status", common.UserStatusDisabled).Error)
	_, alreadyClaimed, err := ClaimAssistantNewUserGift(user.Id)
	assert.ErrorIs(t, err, ErrAssistantGiftUnavailable)
	assert.False(t, alreadyClaimed)

	var storedGift AssistantNewUserGift
	require.NoError(t, db.First(&storedGift, gift.Id).Error)
	assert.Equal(t, AssistantGiftOffered, storedGift.Status)
	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Zero(t, stored.Quota)
}

func TestAssistantNewUserGiftRejectsIneligibleOrShallowDecisions(t *testing.T) {
	db := setupAssistantGiftTestDB(t)
	user := newAssistantGiftUser(t, db, "shallow-gift-user", "shallow@example.com")
	_, _, err := DecideAssistantNewUserGift(user.Id, 0, 100, "Too early.", 1, 100, "198.51.100.20")
	assert.ErrorIs(t, err, ErrAssistantGiftInvalid)
	assert.Equal(t, "insufficient_conversation", AssistantGiftErrorCode(err))
	shortReasonUser := newAssistantGiftUser(t, db, "short-reason-user", "short-reason@example.com")
	_, _, err = DecideAssistantNewUserGift(shortReasonUser.Id, 1, 100, "please", 2, 24, "198.51.100.25")
	assert.ErrorIs(t, err, ErrAssistantGiftInvalid)
	assert.Equal(t, "invalid_decision", AssistantGiftErrorCode(err))

	concise := newAssistantGiftUser(t, db, "concise-gift-user", "concise@example.com")
	// Two substantive turns are sufficient even when the language uses fewer
	// than the old, arbitrary 24-rune aggregate threshold.
	gift, created, err := DecideAssistantNewUserGift(concise.Id, 1, 100, "软件开发与编程辅助。", 2, 24, "198.51.100.23")
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, AssistantGiftOffered, gift.Status)

	disposable := newAssistantGiftUser(t, db, "disposable-gift-user", "disposable@mailinator.com")
	_, _, err = DecideAssistantNewUserGift(disposable.Id, 0, 100, "Disposable accounts are not rewarded.", 2, 40, "198.51.100.21")
	assert.ErrorIs(t, err, ErrAssistantGiftIneligible)
	assert.Equal(t, "account_not_eligible", AssistantGiftErrorCode(err))

	zero, created, err := DecideAssistantNewUserGift(user.Id, 0, 0, "No gift was earned in this conversation.", 2, 40, "198.51.100.20")
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, AssistantGiftDeclined, zero.Status)
	_, _, err = ClaimAssistantNewUserGift(user.Id)
	assert.ErrorIs(t, err, ErrAssistantGiftUnavailable)
}

func TestAssistantGiftGlobalRiskMemoryBlocksAliasesAndBulkNetworks(t *testing.T) {
	db := setupAssistantGiftTestDB(t)
	first := newAssistantGiftUser(t, db, "gift-alias-first", "first.last+one@gmail.com")
	alias := newAssistantGiftUser(t, db, "gift-alias-second", "firstlast+two@googlemail.com")

	_, _, err := DecideAssistantNewUserGift(first.Id, 1, 100, "A legitimate first conversation.", 2, 40, "203.0.113.10")
	require.NoError(t, err)
	_, _, err = DecideAssistantNewUserGift(alias.Id, 2, 100, "The same mailbox under another alias.", 2, 40, "203.0.113.11")
	assert.ErrorIs(t, err, ErrAssistantGiftAbuse)
	assert.Equal(t, "identity_already_used", AssistantGiftErrorCode(err))

	for index := 0; index < assistantGiftIPLimit; index++ {
		user := newAssistantGiftUser(t, db, fmt.Sprintf("gift-network-%d", index), fmt.Sprintf("network-%d@example.com", index))
		_, _, err = DecideAssistantNewUserGift(user.Id, int64(index+10), 100, "A distinct account sharing one network.", 2, 40, "203.0.113.20")
		require.NoError(t, err)
	}
	blocked := newAssistantGiftUser(t, db, "gift-network-blocked", "network-blocked@example.com")
	_, _, err = DecideAssistantNewUserGift(blocked.Id, 20, 100, "A fourth decision from one network.", 2, 40, "203.0.113.20")
	assert.ErrorIs(t, err, ErrAssistantGiftAbuse)
	assert.Equal(t, "network_limit_reached", AssistantGiftErrorCode(err))

	var memories []AssistantGiftRiskMemory
	require.NoError(t, db.Find(&memories).Error)
	for _, memory := range memories {
		assert.Len(t, memory.KeyHash, 64)
		assert.NotContains(t, memory.KeyHash, "example.com")
		assert.NotContains(t, memory.KeyHash, "203.0.113")
	}
}

func TestPurgeAssistantGiftNetworkRiskBeforeIsBoundedAndPreservesIdentity(t *testing.T) {
	db := setupAssistantGiftTestDB(t)
	require.NoError(t, db.Create(&[]AssistantGiftRiskMemory{
		{KeyHash: "old-network-1", Kind: assistantGiftRiskNetwork, DecisionCount: 1, WindowStartedAt: 1, UpdatedAt: 1},
		{KeyHash: "old-network-2", Kind: assistantGiftRiskNetwork, DecisionCount: 2, WindowStartedAt: 2, UpdatedAt: 2},
		{KeyHash: "new-network", Kind: assistantGiftRiskNetwork, DecisionCount: 1, WindowStartedAt: 11, UpdatedAt: 11},
		{KeyHash: "old-identity", Kind: assistantGiftRiskIdentity, DecisionCount: 1, WindowStartedAt: 1, UpdatedAt: 1},
	}).Error)

	removed, err := PurgeAssistantGiftNetworkRiskBefore(context.Background(), 10, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 1, removed)
	removed, err = PurgeAssistantGiftNetworkRiskBefore(context.Background(), 10, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 1, removed)
	removed, err = PurgeAssistantGiftNetworkRiskBefore(context.Background(), 10, 1)
	require.NoError(t, err)
	assert.Zero(t, removed)

	var memories []AssistantGiftRiskMemory
	require.NoError(t, db.Find(&memories).Error)
	require.Len(t, memories, 2)
	byKey := make(map[string]AssistantGiftRiskMemory, len(memories))
	for _, memory := range memories {
		byKey[memory.KeyHash] = memory
	}
	assert.Equal(t, assistantGiftRiskIdentity, byKey["old-identity"].Kind)
	assert.Equal(t, assistantGiftRiskNetwork, byKey["new-network"].Kind)
}
