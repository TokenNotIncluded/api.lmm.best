package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupL1OnboardingTodoTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	installPaidPolicyCurrencyFixture(t, common.QuotaPerUnit)
	previousDB := DB
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	require.NoError(t, db.AutoMigrate(&User{}, &Token{}, &TopUp{}, &L1OnboardingTodo{}))
	t.Cleanup(func() {
		DB = previousDB
		common.RedisEnabled = previousRedis
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	return db
}

func TestL1OnboardingTodoDoesNotCreateOrExposeChecklistForL0(t *testing.T) {
	db := setupL1OnboardingTodoTestDB(t)
	user := User{Username: "l0-onboarding", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)

	view, err := GetL1OnboardingTodo(user.Id)
	require.NoError(t, err)
	assert.False(t, view.Eligibility.Eligible)
	assert.Equal(t, "unavailable", view.Status)
	assert.Empty(t, view.Steps)
	var count int64
	require.NoError(t, db.Model(&L1OnboardingTodo{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.Zero(t, count)
}

func TestL1OnboardingTodoRequiresOrderedServerVerifiedProofs(t *testing.T) {
	db := setupL1OnboardingTodoTestDB(t)
	levelOne := TrustLevelMinUser + 1
	user := User{Username: "l1-onboarding", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, TrustLevelOverride: &levelOne}
	require.NoError(t, db.Create(&user).Error)

	view, err := GetL1OnboardingTodo(user.Id)
	require.NoError(t, err)
	assert.True(t, view.Eligibility.Eligible)
	assert.Equal(t, L1OnboardingStepCreateAPIKey, view.CurrentStep)

	_, err = ApplyL1OnboardingProof(user.Id, 999, L1OnboardingProof{Step: L1OnboardingStepInstallClient, Client: "cc-switch"}, 100)
	assert.ErrorIs(t, err, ErrL1OnboardingProofRequired)

	token := Token{UserId: user.Id, Key: "onboarding-proof-key", Status: common.TokenStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(&token).Error)
	view, err = GetL1OnboardingTodo(user.Id)
	require.NoError(t, err)
	assert.Equal(t, L1OnboardingStepInstallClient, view.CurrentStep)

	_, err = ApplyL1OnboardingProof(user.Id, token.Id, L1OnboardingProof{Step: L1OnboardingStepConfigureClient, Client: "cc-switch", BaseURL: "https://api.example.test", Group: "default"}, 101)
	assert.ErrorIs(t, err, ErrL1OnboardingOutOfOrder)

	view, err = ApplyL1OnboardingProof(user.Id, token.Id, L1OnboardingProof{Step: L1OnboardingStepInstallClient, Client: "cc-switch"}, 102)
	require.NoError(t, err)
	assert.Equal(t, L1OnboardingStepConfigureClient, view.CurrentStep)

	// Replaying the same proof is idempotent and does not move the checklist.
	replayed, err := ApplyL1OnboardingProof(user.Id, token.Id, L1OnboardingProof{Step: L1OnboardingStepInstallClient, Client: "cc-switch"}, 103)
	require.NoError(t, err)
	assert.Equal(t, view.CurrentStep, replayed.CurrentStep)

	view, err = ApplyL1OnboardingProof(user.Id, token.Id, L1OnboardingProof{Step: L1OnboardingStepConfigureClient, Client: "cc-switch", BaseURL: "https://api.example.test", Group: "default"}, 104)
	require.NoError(t, err)
	assert.Equal(t, L1OnboardingStepFirstSuccessfulResponse, view.CurrentStep)
	assert.Equal(t, "in_progress", view.Status)

	// A request that predates client configuration does not complete the last step.
	user.LastAPIActivityAt = 103
	require.NoError(t, db.Model(&user).Update("last_api_activity_at", user.LastAPIActivityAt).Error)
	view, err = GetL1OnboardingTodo(user.Id)
	require.NoError(t, err)
	assert.Equal(t, L1OnboardingStepFirstSuccessfulResponse, view.CurrentStep)

	user.LastAPIActivityAt = 105
	require.NoError(t, db.Model(&user).Update("last_api_activity_at", user.LastAPIActivityAt).Error)
	view, err = GetL1OnboardingTodo(user.Id)
	require.NoError(t, err)
	assert.Equal(t, L1OnboardingStatusCompleted, view.Status)
	assert.Empty(t, view.CurrentStep)
	assert.NotZero(t, view.CompletedAt)
}

func TestL1OnboardingTodoCannotUseWrongTokenGroupOrCredentialData(t *testing.T) {
	db := setupL1OnboardingTodoTestDB(t)
	levelOne := TrustLevelMinUser + 1
	user := User{Username: "l1-group", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, TrustLevelOverride: &levelOne}
	require.NoError(t, db.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "group-proof-key", Status: common.TokenStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(&token).Error)
	_, err := ApplyL1OnboardingProof(user.Id, token.Id, L1OnboardingProof{Step: L1OnboardingStepInstallClient, Client: "cc-switch"}, 200)
	require.NoError(t, err)
	_, err = ApplyL1OnboardingProof(user.Id, token.Id, L1OnboardingProof{Step: L1OnboardingStepConfigureClient, Client: "cc-switch", BaseURL: "https://user:password@example.test", Group: "other"}, 201)
	assert.ErrorIs(t, err, ErrL1OnboardingInvalidProof)

	serializedBytes, marshalErr := json.Marshal(L1OnboardingProof{Step: L1OnboardingStepConfigureClient, Client: "cc-switch", BaseURL: "https://api.example.test", Group: "default"})
	require.NoError(t, marshalErr)
	assert.NotContains(t, string(serializedBytes), token.Key)
}
