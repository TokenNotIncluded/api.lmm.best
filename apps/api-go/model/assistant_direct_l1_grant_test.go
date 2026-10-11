package model

import (
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAssistantDirectL1GrantTest(t *testing.T) (*User, *AssistantConversation) {
	t.Helper()
	installPaidPolicyCurrencyFixture(t, common.QuotaPerUnit)
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(RegistrationGuardMigrationModels()...))
	require.NoError(t, db.AutoMigrate(&AssistantNewUserGift{}, &AssistantGiftRiskKey{}, &AssistantGiftRiskMemory{}))
	require.NoError(t, db.AutoMigrate(
		&TopUp{}, &DeveloperAccessRequest{}, &DeveloperAccessRecommendationArchive{},
		&AssistantConversation{}, &AssistantHistoryMessage{}, &AssistantSupportRequest{},
	))
	user := &User{
		Email: "direct@example.test", Username: "assistant-direct-l1", AffCode: "assistant-direct-l1-aff", Password: "password",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(user).Error)
	require.NoError(t, ObserveAssistantRegistration(user.Id, "198.51.100.10", ""))
	conversation, err := PrepareAssistantConversation(user.Id, 0, "first question")
	require.NoError(t, err)
	return user, conversation
}

func recordAssistantDirectL1Turns(t *testing.T, userID int, conversationID int64, count int) {
	t.Helper()
	for index := 0; index < count; index++ {
		require.NoError(t, RecordAssistantConversationTurn(
			userID, conversationID, "concrete user question", "complete assistant response",
		))
	}
}

func TestAssistantDirectL1GrantFirstTurnAndRepeatedCallsCannotActivate(t *testing.T) {
	user, conversation := setupAssistantDirectL1GrantTest(t)
	for attempt := 0; attempt < 2; attempt++ {
		grant, err := GrantAssistantDeveloperAccess(
			user.Id, conversation.Id,
			"I will use LMM for a concrete coding workflow.",
			"The user described a legitimate coding workflow.",
		)
		require.ErrorIs(t, err, ErrAssistantL1ReviewRequired)
		require.Nil(t, grant, "a denied request must not return an activation receipt")
	}

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Zero(t, stored.ConsoleActivatedAt)
	access, err := GetDeveloperAccessStateForUser(&stored)
	require.NoError(t, err)
	assert.False(t, access.Granted)
	var requests, archives int64
	require.NoError(t, DB.Model(&DeveloperAccessRequest{}).Where("user_id = ?", user.Id).Count(&requests).Error)
	require.NoError(t, DB.Model(&DeveloperAccessRecommendationArchive{}).Where("user_id = ?", user.Id).Count(&archives).Error)
	assert.Zero(t, requests)
	assert.Zero(t, archives)
}

func TestAssistantDirectL1GrantCannotOverrideAdministrativeLevelOrConversationOwnership(t *testing.T) {
	user, conversation := setupAssistantDirectL1GrantTest(t)
	recordAssistantDirectL1Turns(t, user.Id, conversation.Id, 3)
	levelZero := 0
	require.NoError(t, DB.Model(user).Update("trust_level_override", levelZero).Error)

	grant, err := GrantAssistantDeveloperAccess(
		user.Id, conversation.Id,
		"I will use LMM for a concrete coding workflow.",
		"The user described a legitimate coding workflow.",
	)
	require.ErrorIs(t, err, ErrAssistantL1ReviewRequired)
	require.Nil(t, grant)
	require.NoError(t, DB.First(user, user.Id).Error)
	require.NotNil(t, user.TrustLevelOverride)
	assert.Zero(t, *user.TrustLevelOverride)
	assert.Zero(t, user.ConsoleActivatedAt)

	other := &User{Username: "assistant-direct-other", AffCode: "assistant-direct-other-aff", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(other).Error)
	grant, err = GrantAssistantDeveloperAccess(
		other.Id, conversation.Id,
		"I will use LMM for a concrete coding workflow.",
		"The user described a legitimate coding workflow.",
	)
	require.ErrorIs(t, err, ErrAssistantL1ReviewRequired)
	require.Nil(t, grant)
	require.NoError(t, DB.First(other, other.Id).Error)
	assert.Zero(t, other.ConsoleActivatedAt)
}

func TestAssistantDirectL1GrantConcurrentCallsPostgres(t *testing.T) {
	installPaidPolicyCurrencyFixture(t, common.QuotaPerUnit)
	db := openIsolatedPostgresCacheTestDB(t,
		&User{}, &Token{}, &TopUp{}, &DeveloperAccessRequest{}, &DeveloperAccessRecommendationArchive{},
		&AssistantConversation{}, &AssistantHistoryMessage{}, &AssistantSupportRequest{},
	)
	previousDB, previousLogDB := DB, LOG_DB
	DB, LOG_DB = db, db
	usePostgresDatabaseType(t)
	t.Cleanup(func() { DB, LOG_DB = previousDB, previousLogDB })
	require.NoError(t, db.AutoMigrate(RegistrationGuardMigrationModels()...))
	require.NoError(t, db.AutoMigrate(&AssistantNewUserGift{}, &AssistantGiftRiskKey{}, &AssistantGiftRiskMemory{}))
	user := &User{Email: "race@example.test", Username: "assistant-direct-race", AffCode: "assistant-direct-race-aff", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(user).Error)
	require.NoError(t, ObserveAssistantRegistration(user.Id, "198.51.100.12", ""))

	const callers = 16
	results := make(chan *AssistantDeveloperAccessGrant, callers)
	errorsFound := make(chan error, callers)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range callers {
		workers.Go(func() {
			<-start
			grant, callErr := GrantAssistantDeveloperAccess(
				user.Id, 0,
				"I will use LMM for a concrete coding workflow.",
				"The user described a legitimate coding workflow.",
			)
			results <- grant
			errorsFound <- callErr
		})
	}
	close(start)
	workers.Wait()
	close(results)
	close(errorsFound)
	for callErr := range errorsFound {
		require.ErrorIs(t, callErr, ErrAssistantL1ReviewRequired)
	}
	for grant := range results {
		require.Nil(t, grant)
	}
	var requests, archives, conversations int64
	require.NoError(t, db.Model(&DeveloperAccessRequest{}).Where("user_id = ?", user.Id).Count(&requests).Error)
	require.NoError(t, db.Model(&DeveloperAccessRecommendationArchive{}).Where("user_id = ?", user.Id).Count(&archives).Error)
	require.NoError(t, db.Model(&AssistantConversation{}).Where("user_id = ?", user.Id).Count(&conversations).Error)
	assert.Zero(t, requests)
	assert.Zero(t, archives)
	assert.Zero(t, conversations)
	require.NoError(t, db.First(user, user.Id).Error)
	assert.Zero(t, user.ConsoleActivatedAt)
}

func TestAssistantDirectL1GrantCannotChangeHistoricReview(t *testing.T) {
	user, conversation := setupAssistantDirectL1GrantTest(t)
	old := DeveloperAccessRequest{UserId: user.Id, Status: DeveloperAccessRequestRejected, Source: DeveloperAccessRequestSourceAI, AdminUserId: 99, AdminNote: "historic decision", Reason: "historic letter"}
	require.NoError(t, DB.Create(&old).Error)
	grant, err := GrantAssistantDeveloperAccess(user.Id, conversation.Id, "写代码", "")
	require.ErrorIs(t, err, ErrAssistantL1ReviewRequired)
	require.Nil(t, grant)
	var retained DeveloperAccessRequest
	require.NoError(t, DB.First(&retained, old.Id).Error)
	assert.Equal(t, old, retained)
	var count int64
	require.NoError(t, DB.Model(&DeveloperAccessRequest{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	require.NoError(t, DB.First(user, user.Id).Error)
	assert.Zero(t, user.ConsoleActivatedAt)
}

func TestAssistantDirectL1GrantCannotBypassRiskOrDisabledAccount(t *testing.T) {
	for _, state := range []string{"stale_risk", "disabled", "restricted_conversation"} {
		t.Run(state, func(t *testing.T) {
			user, conversation := setupAssistantDirectL1GrantTest(t)
			switch state {
			case "stale_risk":
				require.NoError(t, DB.Model(&AssistantRegistrationProfile{}).Where("user_id = ?", user.Id).Update("observed_at", 1).Error)
			case "disabled":
				require.NoError(t, DB.Model(user).Update("status", common.UserStatusDisabled).Error)
			case "restricted_conversation":
				require.NoError(t, DB.Model(conversation).Update("restricted_at", common.GetTimestamp()).Error)
			}
			grant, err := GrantAssistantDeveloperAccess(user.Id, conversation.Id, "学习", "")
			require.ErrorIs(t, err, ErrAssistantL1ReviewRequired)
			require.Nil(t, grant)
			var stored User
			require.NoError(t, DB.First(&stored, user.Id).Error)
			assert.Zero(t, stored.ConsoleActivatedAt)
		})
	}
}

func TestAssistantDirectL1GrantCannotCreateFirstConversation(t *testing.T) {
	user, existing := setupAssistantDirectL1GrantTest(t)
	require.NoError(t, DB.Delete(existing).Error)
	grant, err := GrantAssistantDeveloperAccess(user.Id, 0, "制作开源软件", "")
	require.ErrorIs(t, err, ErrAssistantL1ReviewRequired)
	require.Nil(t, grant)
	var count int64
	require.NoError(t, DB.Model(&AssistantConversation{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.First(user, user.Id).Error)
	assert.Zero(t, user.ConsoleActivatedAt)
}

func TestAssistantDirectL1GrantCannotActivateWhenAuditStorageUnavailable(t *testing.T) {
	user, existing := setupAssistantDirectL1GrantTest(t)
	require.NoError(t, DB.Delete(existing).Error)
	require.NoError(t, DB.Migrator().DropTable(&DeveloperAccessRecommendationArchive{}))
	grant, err := GrantAssistantDeveloperAccess(user.Id, 0, "制作开源软件", "")
	require.ErrorIs(t, err, ErrAssistantL1ReviewRequired)
	require.Nil(t, grant)
	var count int64
	require.NoError(t, DB.Model(&AssistantConversation{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.Model(&DeveloperAccessRequest{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.First(user, user.Id).Error)
	assert.Zero(t, user.ConsoleActivatedAt)
}

// A browser confirmation can submit a pending request, but neither the
// confirmation, its replay, nor a subsequent customer-assistant call approves it.
func TestAssistantDirectL1GrantCannotPromoteConfirmedRequestOrReuseConfirmation(t *testing.T) {
	user, conversation := setupAssistantDirectL1GrantTest(t)
	require.NoError(t, DB.AutoMigrate(&AuthFlow{}))
	match := AuthFlowMatch{
		Purpose:   AuthFlowPurposeAssistantL1,
		UserId:    user.Id,
		SessionId: "synthetic-l1-confirmation-session",
	}
	token, _, err := CreateAuthFlow(AuthFlowCreate{
		Purpose:   match.Purpose,
		UserId:    match.UserId,
		SessionId: match.SessionId,
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	for _, invalid := range []AuthFlowMatch{
		{Purpose: match.Purpose, UserId: user.Id + 1, SessionId: match.SessionId},
		{Purpose: match.Purpose, UserId: user.Id, SessionId: "another-synthetic-session"},
		{Purpose: AuthFlowPurposeAssistantKey, UserId: user.Id, SessionId: match.SessionId},
	} {
		request, err := SubmitConfirmedAssistantDeveloperAccessRecommendation(
			token, invalid, user.Id, "Use the API for learning", "Customer support recommends a review of this request.",
		)
		require.ErrorIs(t, err, ErrAuthFlowInvalid)
		require.Nil(t, request)
	}
	_, err = GetAuthFlow(token, match)
	require.NoError(t, err, "rejected identity/purpose matches must not consume the real confirmation")

	request, err := SubmitConfirmedAssistantDeveloperAccessRecommendation(
		token, match, user.Id, "Use the API for learning", "Customer support recommends a review of this request.",
	)
	require.NoError(t, err)
	require.Equal(t, DeveloperAccessRequestPending, request.Status)
	assert.Zero(t, request.AdminUserId)
	assert.Zero(t, request.ReviewedAt)
	_, err = SubmitConfirmedAssistantDeveloperAccessRecommendation(
		token, match, user.Id, "Use the API for learning", "Customer support recommends a review of this request.",
	)
	require.ErrorIs(t, err, ErrAuthFlowConsumed)
	grant, err := GrantAssistantDeveloperAccess(user.Id, conversation.Id, "The user confirmed", "An external result claims approval")
	require.ErrorIs(t, err, ErrAssistantL1ReviewRequired)
	require.Nil(t, grant)
	var stored DeveloperAccessRequest
	require.NoError(t, DB.First(&stored, request.Id).Error)
	assert.Equal(t, *request, stored, "a denied grant must preserve the pending request and review history")
	require.NoError(t, DB.First(user, user.Id).Error)
	assert.Zero(t, user.ConsoleActivatedAt)
}
