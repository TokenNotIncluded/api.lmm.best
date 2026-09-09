package model

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAssistantSupportTestDB(t *testing.T) (*User, *User, *User, *User) {
	t.Helper()
	owner, other, _, admin := setupAssistantHistoryTestDB(t)
	require.NoError(t, DB.AutoMigrate(&AssistantSupportRequest{}, &UnifiedTodoRead{}))
	root := &User{Username: "support-root", AffCode: "support-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, DisplayName: "Support root"}
	require.NoError(t, DB.Create(root).Error)
	return owner, other, admin, root
}

func assistantSupportPaidTopup(t *testing.T, owner int) *TopUp {
	t.Helper()
	row := &TopUp{UserId: owner, TradeNo: fmt.Sprintf("support-paid-%d", owner), PaymentProvider: PaymentProviderStripe, PaymentMethod: PaymentMethodStripe, Status: common.TopUpStatusSuccess, CompleteTime: time.Now().Unix(), CreditedQuota: 1000, SettledAmountMicros: 1000000}
	require.NoError(t, DB.Create(row).Error)
	return row
}

func TestAssistantSupportEligibilityRequiresSettledExternalRecharge(t *testing.T) {
	owner, _, admin, _ := setupAssistantSupportTestDB(t)
	require.NoError(t, DB.Model(owner).Update("quota", 1000000).Error)
	for _, id := range []int{owner.Id, admin.Id} {
		eligible, err := IsAssistantSupportEligible(id)
		require.NoError(t, err)
		assert.False(t, eligible)
	}
	paid := assistantSupportPaidTopup(t, owner.Id)
	eligible, err := IsAssistantSupportEligible(owner.Id)
	require.NoError(t, err)
	assert.True(t, eligible)
	invalid := []map[string]any{
		{"status": common.TopUpStatusPending}, {"complete_time": 0}, {"settled_amount_micros": 0, "money": 0},
		{"refunded_amount_micros": 1}, {"refunded_quota": 1}, {"payment_provider": PaymentProviderBalance},
		{"payment_method": PaymentMethodBalance}, {"payment_provider": "gift"}, {"credited_quota": 0, "amount": 0},
	}
	for _, updates := range invalid {
		require.NoError(t, DB.Model(paid).Updates(updates).Error)
		eligible, err = IsAssistantSupportEligible(owner.Id)
		require.NoError(t, err)
		assert.False(t, eligible, "updates: %v", updates)
		require.NoError(t, DB.Model(paid).Updates(map[string]any{"status": common.TopUpStatusSuccess, "complete_time": time.Now().Unix(), "settled_amount_micros": 1000000, "money": 0, "refunded_amount_micros": 0, "refunded_quota": 0, "payment_provider": PaymentProviderStripe, "payment_method": PaymentMethodStripe, "credited_quota": 1000, "amount": 0}).Error)
	}
	require.NoError(t, DB.Model(owner).Update("status", common.UserStatusDisabled).Error)
	_, err = IsAssistantSupportEligible(owner.Id)
	assert.ErrorIs(t, err, ErrAssistantSupportForbidden)
}

func TestAssistantSupportAppointmentValidationUpgradeAndIsolation(t *testing.T) {
	owner, other, admin, _ := setupAssistantSupportTestDB(t)
	future := time.Now().Add(time.Hour).Unix()
	_, _, err := CreateAssistantSupportRequest(owner.Id, 0, AssistantSupportKindAppointment, "Setup", "UTC", future)
	assert.ErrorIs(t, err, ErrAssistantSupportIneligible)
	assistantSupportPaidTopup(t, owner.Id)
	for _, when := range []int64{0, time.Now().Unix() - 1, time.Now().Add(91 * 24 * time.Hour).Unix()} {
		_, _, err = CreateAssistantSupportRequest(owner.Id, 0, AssistantSupportKindAppointment, "Setup", "UTC", when)
		assert.ErrorIs(t, err, ErrAssistantSupportInvalid)
	}
	appointment, created, err := CreateAssistantSupportRequest(owner.Id, 0, AssistantSupportKindAppointment, "Setup password=secretvalue", "UTC", future)
	require.NoError(t, err)
	assert.True(t, created)
	assert.NotContains(t, appointment.Topic, "secretvalue")
	blocked, err := BlockAssistantAIForSupport(owner.Id, appointment.ConversationId)
	require.NoError(t, err)
	assert.False(t, blocked)
	repeated, created, err := CreateAssistantSupportRequest(owner.Id, 0, AssistantSupportKindAppointment, "another time", "UTC", future+60)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, appointment.Id, repeated.Id)
	assert.Equal(t, future, repeated.ScheduledAt)
	foreign, err := PrepareAssistantConversation(other.Id, 0, "foreign")
	require.NoError(t, err)
	_, _, err = CreateAssistantSupportRequest(owner.Id, foreign.Id, AssistantSupportKindHandoff, "transfer", "", 0)
	assert.ErrorIs(t, err, ErrAssistantConversationNotFound)
	require.NoError(t, DB.Create(&UnifiedTodoRead{UserId: admin.Id, Category: UnifiedTodoCategoryHumanSupport, ItemId: appointment.Id, ReadAt: 1}).Error)
	upgraded, created, err := CreateAssistantSupportRequest(owner.Id, appointment.ConversationId, AssistantSupportKindHandoff, "转人工", "", 0)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, AssistantSupportKindHandoff, upgraded.Kind)
	assert.Zero(t, upgraded.ScheduledAt)
	assert.Empty(t, upgraded.PreferredTime)
	blocked, err = BlockAssistantAIForSupport(owner.Id, appointment.ConversationId)
	require.NoError(t, err)
	assert.True(t, blocked)
	var reads int64
	require.NoError(t, DB.Model(&UnifiedTodoRead{}).Count(&reads).Error)
	assert.Zero(t, reads)
	_, err = RecordAssistantConversationTurnForRequest(owner.Id, appointment.ConversationId, "stale AI user", "stale AI answer")
	assert.ErrorIs(t, err, ErrAssistantSupportAIBlocked)
	_, _, err = RecordAssistantSecurityRefusal(owner.Id, appointment.ConversationId, "stale AI security turn", "stale refusal", AssistantSecurityIncidentCategory)
	assert.ErrorIs(t, err, ErrAssistantSupportAIBlocked)
	_, err = CreateAssistantSecureCard(owner.Id, appointment.ConversationId, AssistantSecureCardTypeAPIKey, "stale card", "secret")
	assert.ErrorIs(t, err, ErrAssistantSupportAIBlocked)
	_, err = GetAssistantSupportMessages(admin.Id, appointment.Id)
	assert.ErrorIs(t, err, ErrAssistantSupportForbidden)
	_, err = GetAssistantSupportRequest(other.Id, appointment.Id)
	assert.ErrorIs(t, err, ErrAssistantSupportForbidden)
}

func TestAssistantSupportAtomicClaimAndCurrentRole(t *testing.T) {
	owner, other, admin, root := setupAssistantSupportTestDB(t)
	request, _, err := CreateAssistantSupportRequest(owner.Id, 0, AssistantSupportKindHandoff, "help", "", 0)
	require.NoError(t, err)
	_, err = AcceptAssistantSupportRequest(other.Id, request.Id)
	assert.ErrorIs(t, err, ErrAssistantSupportForbidden)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, id := range []int{admin.Id, root.Id} {
		wg.Add(1)
		go func(id int) { defer wg.Done(); _, err := AcceptAssistantSupportRequest(id, request.Id); errs <- err }(id)
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		}
	}
	assert.Equal(t, 1, success)
	stored, err := GetActiveAssistantSupportRequest(owner.Id)
	require.NoError(t, err)
	assert.Equal(t, AssistantSupportStatusAccepted, stored.Status)
	loser := admin.Id
	if stored.AssignedAdminId == admin.Id {
		loser = root.Id
	}
	_, err = AcceptAssistantSupportRequest(loser, request.Id)
	assert.ErrorIs(t, err, ErrAssistantSupportConflict)
	_, err = AddAssistantSupportMessage(loser, request.Id, "interfere")
	assert.ErrorIs(t, err, ErrAssistantSupportForbidden)
	_, err = GetAssistantSupportMessages(loser, request.Id)
	assert.ErrorIs(t, err, ErrAssistantSupportForbidden)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", stored.AssignedAdminId).Update("role", common.RoleCommonUser).Error)
	_, err = AddAssistantSupportMessage(stored.AssignedAdminId, request.Id, "after demotion")
	assert.ErrorIs(t, err, ErrAssistantSupportForbidden)
	_, err = CloseAssistantSupportRequest(stored.AssignedAdminId, request.Id, false)
	assert.ErrorIs(t, err, ErrAssistantSupportForbidden)
	self, _, err := CreateAssistantSupportRequest(root.Id, 0, AssistantSupportKindHandoff, "self", "", 0)
	require.NoError(t, err)
	_, err = AcceptAssistantSupportRequest(root.Id, self.Id)
	assert.ErrorIs(t, err, ErrAssistantSupportForbidden)
}

func TestAssistantSupportHistoryHumanIdentityClosureAndBoundedStorage(t *testing.T) {
	owner, _, admin, root := setupAssistantSupportTestDB(t)
	request, _, err := CreateAssistantSupportRequest(owner.Id, 0, AssistantSupportKindHandoff, "help", "", 0)
	require.NoError(t, err)
	_, err = AcceptAssistantSupportRequest(admin.Id, request.Id)
	require.NoError(t, err)
	_, err = AddAssistantSupportMessage(owner.Id, request.Id, "password=supportsecret")
	require.NoError(t, err)
	require.NoError(t, DB.Model(admin).Update("display_name", "helper@example.com").Error)
	human, err := AddAssistantSupportMessage(admin.Id, request.Id, "Please check the endpoint")
	require.NoError(t, err)
	assert.Equal(t, AssistantHistoryRoleHuman, human.Role)
	assert.NotContains(t, human.ActorName, "helper@example.com")
	require.NoError(t, DB.Create(&AssistantHistoryMessage{ConversationId: request.ConversationId, Sequence: 3, Role: AssistantHistoryRoleCard, Content: "never expose card", CreatedAt: 1}).Error)
	messages, err := GetAssistantSupportMessages(admin.Id, request.Id)
	require.NoError(t, err)
	require.Len(t, messages, 2)
	assert.NotContains(t, messages[0].Content, "supportsecret")
	assert.Empty(t, messages[1].Cards)
	closed, err := CloseAssistantSupportRequest(admin.Id, request.Id, false)
	require.NoError(t, err)
	assert.Nil(t, closed.ActiveUserId)
	blocked, err := BlockAssistantAIForSupport(owner.Id, request.ConversationId)
	require.NoError(t, err)
	assert.False(t, blocked)
	contextRows, err := LoadAssistantConversationMessages(owner.Id, request.ConversationId, 20)
	require.NoError(t, err)
	require.Len(t, contextRows, 2)
	assert.Equal(t, AssistantHistoryRoleAssistant, contextRows[1].Role)
	assert.Contains(t, contextRows[1].Content, "[Human technical support]")
	_, err = RecordAssistantConversationTurnForRequest(owner.Id, request.ConversationId, "later private question", "later answer")
	require.NoError(t, err)
	messages, err = GetAssistantSupportMessages(admin.Id, request.Id)
	require.NoError(t, err)
	assert.Len(t, messages, 2)
	later, _, err := CreateAssistantSupportRequest(owner.Id, request.ConversationId, AssistantSupportKindHandoff, "new request", "", 0)
	require.NoError(t, err)
	_, err = AcceptAssistantSupportRequest(root.Id, later.Id)
	require.NoError(t, err)
	_, err = AddAssistantSupportMessage(root.Id, later.Id, "second helper")
	require.NoError(t, err)
	messages, err = GetAssistantSupportMessages(root.Id, later.Id)
	require.NoError(t, err)
	var names []string
	for _, message := range messages {
		if message.Role == AssistantHistoryRoleHuman {
			names = append(names, message.ActorName)
		}
	}
	assert.Equal(t, []string{human.ActorName, "Support root"}, names)
	for i := 0; i < 135; i++ {
		_, err = AddAssistantSupportMessage(owner.Id, later.Id, strings.Repeat("x", 2100))
		require.NoError(t, err)
	}
	var count int64
	require.NoError(t, DB.Model(&AssistantHistoryMessage{}).Where("conversation_id = ?", request.ConversationId).Count(&count).Error)
	assert.LessOrEqual(t, count, int64(assistantHistoryConversationMaxMessages))
	_, err = CloseAssistantSupportRequest(root.Id, later.Id, true)
	assert.ErrorIs(t, err, ErrAssistantSupportForbidden)
	_, err = CloseAssistantSupportRequest(owner.Id, later.Id, true)
	require.NoError(t, err)
	_, err = AddAssistantSupportMessage(owner.Id, later.Id, "after close")
	assert.ErrorIs(t, err, ErrAssistantSupportConflict)
}

func TestAssistantSupportRetentionProtectsActiveAndDeletesClosed(t *testing.T) {
	owner, _, admin, _ := setupAssistantSupportTestDB(t)
	request, _, err := CreateAssistantSupportRequest(owner.Id, 0, AssistantSupportKindHandoff, "help", "", 0)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&AssistantConversation{}).Where("id = ?", request.ConversationId).Update("updated_at", 1).Error)
	cutoffs := AssistantRetentionCutoffs{ActiveBefore: 100, ArchivedBefore: 100, RestrictedBefore: 100}
	result, err := PurgeAssistantConversationsBefore(context.Background(), cutoffs, 10)
	require.NoError(t, err)
	assert.Zero(t, result.Conversations)
	_, err = CloseAssistantSupportRequest(owner.Id, request.Id, true)
	require.NoError(t, err)
	require.NoError(t, DB.Create(&UnifiedTodoRead{UserId: admin.Id, Category: UnifiedTodoCategoryHumanSupport, ItemId: request.Id, ReadAt: 1}).Error)
	result, err = PurgeAssistantConversationsBefore(context.Background(), cutoffs, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, result.Conversations)
	var count int64
	require.NoError(t, DB.Model(&AssistantSupportRequest{}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.Model(&UnifiedTodoRead{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestAssistantSupportRestrictedConversationStillAllowsHandoff(t *testing.T) {
	owner, _, _, _ := setupAssistantSupportTestDB(t)
	id, _, err := RecordAssistantSecurityRefusal(owner.Id, 0, "abuse", "refused", AssistantSecurityIncidentCategory)
	require.NoError(t, err)
	request, _, err := CreateAssistantSupportRequest(owner.Id, id, AssistantSupportKindHandoff, "appeal", "", 0)
	require.NoError(t, err)
	_, err = AddAssistantSupportMessage(owner.Id, request.Id, "please review")
	require.NoError(t, err)
	_, err = CloseAssistantSupportRequest(owner.Id, request.Id, true)
	require.NoError(t, err)
	_, err = PrepareAssistantConversation(owner.Id, id, "AI attempt")
	assert.ErrorIs(t, err, ErrAssistantConversationRestricted)
}

func TestAssistantSupportActiveConstraintAllowsClosedHistoryAndRequeuesDisabledAssignee(t *testing.T) {
	owner, _, admin, root := setupAssistantSupportTestDB(t)
	first, _, err := CreateAssistantSupportRequest(owner.Id, 0, AssistantSupportKindHandoff, "help", "", 0)
	require.NoError(t, err)
	duplicate := *first
	duplicate.Id = 0
	require.Error(t, DB.Create(&duplicate).Error)
	_, err = AcceptAssistantSupportRequest(admin.Id, first.Id)
	require.NoError(t, err)
	require.NoError(t, DB.Model(admin).Update("status", common.UserStatusDisabled).Error)
	pending, err := GetLatestAssistantSupportRequest(owner.Id, first.ConversationId)
	require.NoError(t, err)
	assert.Equal(t, AssistantSupportStatusPending, pending.Status)
	assert.Zero(t, pending.AssignedAdminId)
	_, err = AcceptAssistantSupportRequest(root.Id, first.Id)
	require.NoError(t, err)
	_, err = CloseAssistantSupportRequest(root.Id, first.Id, false)
	require.NoError(t, err)
	second, created, err := CreateAssistantSupportRequest(owner.Id, first.ConversationId, AssistantSupportKindHandoff, "again", "", 0)
	require.NoError(t, err)
	assert.True(t, created)
	assert.NotEqual(t, first.Id, second.Id)
	_, err = CloseAssistantSupportRequest(owner.Id, second.Id, true)
	require.NoError(t, err)
	var rows int64
	require.NoError(t, DB.Model(&AssistantSupportRequest{}).Where("user_id = ?", owner.Id).Count(&rows).Error)
	assert.EqualValues(t, 2, rows)
}

func TestAssistantSupportMissingSchemaFailsClosed(t *testing.T) {
	owner, _, _, _ := setupAssistantSupportTestDB(t)
	conversation, err := PrepareAssistantConversation(owner.Id, 0, "hello")
	require.NoError(t, err)
	require.NoError(t, DB.Migrator().DropTable(&AssistantSupportRequest{}))
	blocked, err := BlockAssistantAIForSupport(owner.Id, conversation.Id)
	require.Error(t, err)
	assert.False(t, blocked)
	_, err = RecordAssistantConversationTurnForRequest(owner.Id, conversation.Id, "unsafe fallback", "must not persist")
	require.Error(t, err)
	var rows int64
	require.NoError(t, DB.Model(&AssistantHistoryMessage{}).Count(&rows).Error)
	assert.Zero(t, rows)
}

func TestAssistantSupportUserDeletionRemovesOwnedRequestsAndRequeuesAssignments(t *testing.T) {
	owner, _, admin, root := setupAssistantSupportTestDB(t)
	migrateUserAssistantData(t)
	request, _, err := CreateAssistantSupportRequest(owner.Id, 0, AssistantSupportKindHandoff, "help", "", 0)
	require.NoError(t, err)
	_, err = AcceptAssistantSupportRequest(admin.Id, request.Id)
	require.NoError(t, err)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return deleteUserAssistantData(tx, admin.Id) }))
	pending, err := GetActiveAssistantSupportRequest(owner.Id)
	require.NoError(t, err)
	assert.Equal(t, AssistantSupportStatusPending, pending.Status)
	assert.Zero(t, pending.AssignedAdminId)
	_, err = AcceptAssistantSupportRequest(root.Id, request.Id)
	require.NoError(t, err)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return deleteUserAssistantData(tx, owner.Id) }))
	var rows int64
	require.NoError(t, DB.Model(&AssistantSupportRequest{}).Where("user_id = ?", owner.Id).Count(&rows).Error)
	assert.Zero(t, rows)
	require.NoError(t, DB.Model(&AssistantConversation{}).Where("user_id = ?", owner.Id).Count(&rows).Error)
	assert.Zero(t, rows)
}

func TestAssistantSupportHistoryGroupsConsecutiveHumanRepliesAndUserDetails(t *testing.T) {
	owner, _, admin, _ := setupAssistantSupportTestDB(t)
	conversationID, err := RecordAssistantConversationTurnForRequest(owner.Id, 0, "Initial AI question", "Initial AI answer")
	require.NoError(t, err)
	request, _, err := CreateAssistantSupportRequest(owner.Id, conversationID, AssistantSupportKindHandoff, "investigate endpoint", "", 0)
	require.NoError(t, err)
	questions := []string{"The endpoint fails", "The response status is 503", "It happens only with streaming"}
	for _, question := range questions {
		_, err = AddAssistantSupportMessage(owner.Id, request.Id, question)
		require.NoError(t, err)
	}
	_, err = AcceptAssistantSupportRequest(admin.Id, request.Id)
	require.NoError(t, err)
	replies := make([]string, 35)
	for index := range replies {
		replies[index] = fmt.Sprintf("[Human technical support] Diagnostic step %d", index+1)
		_, err = AddAssistantSupportMessage(admin.Id, request.Id, fmt.Sprintf("Diagnostic step %d", index+1))
		require.NoError(t, err)
	}
	_, err = CloseAssistantSupportRequest(admin.Id, request.Id, false)
	require.NoError(t, err)
	// A two-message model window still includes the complete human turn, even
	// when its reply run is longer than the old 20-row minimum scan window.
	messages, err := LoadAssistantConversationMessages(owner.Id, conversationID, 2)
	require.NoError(t, err)
	require.Len(t, messages, 2)
	assert.Equal(t, strings.Join(questions, "\n\n"), messages[0].Content)
	assert.Equal(t, strings.Join(replies, "\n\n"), messages[1].Content)
	assert.Equal(t, AssistantHistoryRoleAssistant, messages[1].Role)
	assert.Equal(t, messages[0].Sequence+1, messages[1].Sequence)
	_, err = RecordAssistantConversationTurnForRequest(owner.Id, conversationID, "The fix worked", "Glad it is resolved")
	require.NoError(t, err)
	messages, err = LoadAssistantConversationMessages(owner.Id, conversationID, 6)
	require.NoError(t, err)
	require.Len(t, messages, 6)
	assert.Equal(t, "Initial AI question", messages[0].Content)
	assert.Equal(t, strings.Join(questions, "\n\n"), messages[2].Content)
	assert.Equal(t, strings.Join(replies, "\n\n"), messages[3].Content)
	assert.Equal(t, "Glad it is resolved", messages[5].Content)
}
