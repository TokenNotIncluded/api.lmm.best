package model

import (
	"errors"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAssistantDirectL1GrantTest(t *testing.T) (*User, *AssistantConversation) {
	t.Helper()
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&TopUp{}, &DeveloperAccessRequest{}, &DeveloperAccessRecommendationArchive{},
		&AssistantConversation{}, &AssistantHistoryMessage{}, &AssistantSupportRequest{},
	))
	user := &User{
		Username: "assistant-direct-l1", AffCode: "assistant-direct-l1-aff", Password: "password",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(user).Error)
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

func TestAssistantDirectL1GrantRequiresThreeCompleteAITurnsAndIsIdempotent(t *testing.T) {
	user, conversation := setupAssistantDirectL1GrantTest(t)
	recordAssistantDirectL1Turns(t, user.Id, conversation.Id, 2)
	require.NoError(t, DB.Create(&AssistantHistoryMessage{
		ConversationId: conversation.Id, Sequence: 5, Role: AssistantHistoryRoleUser,
		Content: "unfinished third question", CreatedAt: common.GetTimestamp(),
	}).Error)

	turns, err := CountCompletedAssistantConversationTurns(user.Id, conversation.Id)
	require.NoError(t, err)
	assert.Equal(t, 2, turns)
	_, err = GrantAssistantDeveloperAccess(
		user.Id, conversation.Id,
		"I will use LMM for a concrete coding workflow.",
		"The user described a legitimate coding workflow over three assistant turns.",
	)
	assert.ErrorIs(t, err, ErrAssistantDirectGrantTurnsRequired)

	require.NoError(t, DB.Create(&AssistantHistoryMessage{
		ConversationId: conversation.Id, Sequence: 6, Role: AssistantHistoryRoleAssistant,
		Content: "completed third response", CreatedAt: common.GetTimestamp(),
	}).Error)
	grant, err := GrantAssistantDeveloperAccess(
		user.Id, conversation.Id,
		"I will use LMM for a concrete coding workflow.",
		"The user described a legitimate coding workflow over three assistant turns.",
	)
	require.NoError(t, err)
	require.True(t, grant.Activated)
	assert.Equal(t, 3, grant.CompletedTurns)
	assert.Equal(t, DeveloperAccessRequestApproved, grant.Request.Status)
	assert.Equal(t, DeveloperAccessRequestSourceDirectAI, grant.Request.Source)
	assert.Contains(t, grant.Request.AdminNote, "conversation")

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Positive(t, stored.ConsoleActivatedAt)
	access, err := GetDeveloperAccessStateForUser(&stored)
	require.NoError(t, err)
	assert.True(t, access.Granted)

	repeated, err := GrantAssistantDeveloperAccess(
		user.Id, conversation.Id,
		"A duplicate tool call must not create another grant.",
		"A duplicate assistant call must return the already active audited grant.",
	)
	require.NoError(t, err)
	assert.False(t, repeated.Activated)
	assert.Equal(t, grant.Request.Id, repeated.Request.Id)
	var requests, archives int64
	require.NoError(t, DB.Model(&DeveloperAccessRequest{}).Where("user_id = ?", user.Id).Count(&requests).Error)
	require.NoError(t, DB.Model(&DeveloperAccessRecommendationArchive{}).Where("user_id = ?", user.Id).Count(&archives).Error)
	assert.EqualValues(t, 1, requests)
	assert.EqualValues(t, 1, archives)
}

func TestAssistantDirectL1GrantCannotOverrideAdministrativeLevelOrConversationOwnership(t *testing.T) {
	user, conversation := setupAssistantDirectL1GrantTest(t)
	recordAssistantDirectL1Turns(t, user.Id, conversation.Id, 3)
	levelZero := 0
	require.NoError(t, DB.Model(user).Update("trust_level_override", levelZero).Error)

	_, err := GrantAssistantDeveloperAccess(
		user.Id, conversation.Id,
		"I will use LMM for a concrete coding workflow.",
		"The user described a legitimate coding workflow over three assistant turns.",
	)
	assert.ErrorIs(t, err, ErrAssistantDirectGrantNotL0)

	other := &User{Username: "assistant-direct-other", AffCode: "assistant-direct-other-aff", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(other).Error)
	_, err = GrantAssistantDeveloperAccess(
		other.Id, conversation.Id,
		"I will use LMM for a concrete coding workflow.",
		"The user described a legitimate coding workflow over three assistant turns.",
	)
	assert.True(t, errors.Is(err, ErrAssistantConversationNotFound))
}

func TestAssistantDirectL1GrantConcurrentCallsPostgres(t *testing.T) {
	db := openIsolatedPostgresCacheTestDB(t,
		&User{}, &Token{}, &TopUp{}, &DeveloperAccessRequest{}, &DeveloperAccessRecommendationArchive{},
		&AssistantConversation{}, &AssistantHistoryMessage{}, &AssistantSupportRequest{},
	)
	previousDB, previousLogDB := DB, LOG_DB
	DB, LOG_DB = db, db
	usePostgresDatabaseType(t)
	t.Cleanup(func() { DB, LOG_DB = previousDB, previousLogDB })
	user := &User{Username: "assistant-direct-race", AffCode: "assistant-direct-race-aff", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(user).Error)
	conversation, err := PrepareAssistantConversation(user.Id, 0, "first")
	require.NoError(t, err)
	recordAssistantDirectL1Turns(t, user.Id, conversation.Id, 3)

	results := make(chan *AssistantDeveloperAccessGrant, 2)
	errorsFound := make(chan error, 2)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			<-start
			grant, callErr := GrantAssistantDeveloperAccess(
				user.Id, conversation.Id,
				"I will use LMM for a concrete coding workflow.",
				"The user described a legitimate coding workflow over three assistant turns.",
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
		require.NoError(t, callErr)
	}
	activated := 0
	requestID := 0
	for grant := range results {
		require.NotNil(t, grant)
		if grant.Activated {
			activated++
		}
		if requestID == 0 {
			requestID = grant.Request.Id
		}
		assert.Equal(t, requestID, grant.Request.Id)
	}
	assert.Equal(t, 1, activated)
	var requests, archives int64
	require.NoError(t, db.Model(&DeveloperAccessRequest{}).Where("user_id = ?", user.Id).Count(&requests).Error)
	require.NoError(t, db.Model(&DeveloperAccessRecommendationArchive{}).Where("user_id = ?", user.Id).Count(&archives).Error)
	assert.EqualValues(t, 1, requests)
	assert.EqualValues(t, 1, archives)
}
