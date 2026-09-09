package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnifiedTodoIncludesSubmittedBountyForOwner(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	require.NoError(t, db.AutoMigrate(&UnifiedTodoRead{}, &DeveloperAccessRequest{}, &AccountActionRequest{}, &AssistantConversation{}, &AssistantHistoryMessage{}, &AssistantSecurityIncident{}, &AssistantSecurityReviewNotice{}, &AssistantSupportRequest{}))

	owner := createOpenSourceBountyUser(t, db, "todo-owner", 10_000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "todo-participant", 0, common.RoleCommonUser)
	now := common.GetTimestamp()
	project := OpenSourceBountyProject{
		OwnerUserId: owner.Id, RepositoryUrl: "https://github.com/example/todo-bounty",
		Title: "Review the submitted fix", Description: "A reproducible bug.", Rules: "Include tests.",
		RewardQuota: 1_000, NetRewardQuota: 1_000, RewardSlots: 1, EscrowQuota: 1_000,
		Status: OpenSourceBountyStatusPublished, CreatedAt: now, UpdatedAt: now, PublishedAt: now,
	}
	require.NoError(t, db.Create(&project).Error)
	challenge := OpenSourceBountyChallenge{
		ProjectId: project.Id, ParticipantUserId: participant.Id, GithubHandle: "todo-participant",
		Status: OpenSourceBountyChallengeSubmitted, IssueUrl: "https://github.com/example/todo-bounty/issues/1",
		PullRequestUrl: "https://github.com/example/todo-bounty/pull/2", SubmissionNote: "Tests are green.",
		RewardQuota: 1_000, AcceptedAt: now - 10, SubmittedAt: now, CreatedAt: now - 10, UpdatedAt: now,
	}
	require.NoError(t, db.Create(&challenge).Error)

	page, err := GetUnifiedTodoCenter(owner.Id, owner.Role, UnifiedTodoCategoryBountyReview, 1, 20)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, int64(1), page.Total)
	assert.Equal(t, int64(1), page.UnreadCount)
	assert.Equal(t, challenge.Id, page.Items[0].SourceId)
	assert.Equal(t, project.Id, page.Items[0].Details["project_id"])
	assert.Equal(t, participant.Username, page.Items[0].Details["participant_username"])

	participantPage, err := GetUnifiedTodoCenter(participant.Id, participant.Role, UnifiedTodoCategoryBountyReview, 1, 20)
	require.NoError(t, err)
	assert.Empty(t, participantPage.Items)

	marked, err := MarkUnifiedTodoReads(owner.Id, owner.Role, UnifiedTodoCategoryBountyReview, []int{challenge.Id}, false)
	require.NoError(t, err)
	assert.Equal(t, 1, marked)
	page, err = GetUnifiedTodoCenter(owner.Id, owner.Role, UnifiedTodoCategoryBountyReview, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(0), page.UnreadCount)
	assert.True(t, page.Items[0].Read)

	require.NoError(t, db.Model(&challenge).Updates(map[string]any{
		"status": OpenSourceBountyChallengeApproved, "reviewed_at": now + 1, "updated_at": now + 1,
	}).Error)
	page, err = GetUnifiedTodoCenter(owner.Id, owner.Role, UnifiedTodoCategoryBountyReview, 1, 20)
	require.NoError(t, err)
	assert.Empty(t, page.Items)
	assert.Zero(t, page.Total)
}

func TestUnifiedTodoDeveloperAccessQueueContainsOnlyPendingIdentifiedApplicants(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	require.NoError(t, db.AutoMigrate(&UnifiedTodoRead{}, &DeveloperAccessRequest{}, &AccountActionRequest{}, &AssistantConversation{}, &AssistantHistoryMessage{}, &AssistantSecurityIncident{}, &AssistantSecurityReviewNotice{}, &AssistantSupportRequest{}))

	admin := createOpenSourceBountyUser(t, db, "todo-admin", 0, common.RoleAdminUser)
	pendingUser := createOpenSourceBountyUser(t, db, "pending-applicant", 0, common.RoleCommonUser)
	pendingUser.Email = "pending@example.test"
	require.NoError(t, db.Model(&pendingUser).Update("email", pendingUser.Email).Error)
	approvedUser := createOpenSourceBountyUser(t, db, "approved-applicant", 0, common.RoleCommonUser)
	rejectedUser := createOpenSourceBountyUser(t, db, "rejected-applicant", 0, common.RoleCommonUser)
	legacyUser := createOpenSourceBountyUser(t, db, "legacy-applicant", 0, common.RoleCommonUser)
	now := common.GetTimestamp()
	requests := []DeveloperAccessRequest{
		{UserId: pendingUser.Id, Status: DeveloperAccessRequestPending, Source: DeveloperAccessRequestSourceAI, Reason: "Build a real client integration.", AIRecommendation: "Recommend this applicant for a concrete production integration.", CreatedAt: now},
		{UserId: approvedUser.Id, Status: DeveloperAccessRequestApproved, Source: DeveloperAccessRequestSourceAI, Reason: "Already reviewed.", CreatedAt: now - 1, ReviewedAt: now},
		{UserId: rejectedUser.Id, Status: DeveloperAccessRequestRejected, Source: DeveloperAccessRequestSourceAssistant, Reason: "Already rejected.", CreatedAt: now - 2, ReviewedAt: now},
		{UserId: legacyUser.Id, Status: DeveloperAccessRequestPending, Source: DeveloperAccessRequestSourceOld, Reason: "Obsolete legacy request.", CreatedAt: now - 3},
	}
	require.NoError(t, db.Create(&requests).Error)

	page, err := GetUnifiedTodoCenter(admin.Id, admin.Role, UnifiedTodoCategoryDeveloperAccess, 1, 20)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, int64(1), page.Total)
	assert.Equal(t, int64(1), page.UnreadCount)
	assert.Equal(t, requests[0].Id, page.Items[0].SourceId)
	assert.Equal(t, pendingUser.Id, page.Items[0].Details["user_id"])
	assert.Equal(t, pendingUser.Username, page.Items[0].Details["username"])
	assert.Equal(t, pendingUser.Email, page.Items[0].Details["email"])
	assert.Equal(t, requests[0].AIRecommendation, page.Items[0].Summary)

	marked, err := MarkUnifiedTodoReads(admin.Id, admin.Role, UnifiedTodoCategoryDeveloperAccess, nil, true)
	require.NoError(t, err)
	assert.Equal(t, 1, marked)
	var reads []UnifiedTodoRead
	require.NoError(t, db.Where("user_id = ? AND category = ?", admin.Id, UnifiedTodoCategoryDeveloperAccess).Find(&reads).Error)
	require.Len(t, reads, 1)
	assert.Equal(t, requests[0].Id, reads[0].ItemId)
}

func TestUnifiedTodoSecurityIncidentsFollowAdministratorRoleLattice(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&UnifiedTodoRead{},
		&DeveloperAccessRequest{},
		&AccountActionRequest{},
		&AssistantConversation{},
		&AssistantHistoryMessage{},
		&AssistantSecurityIncident{},
		&AssistantSecurityReviewNotice{}, &AssistantSupportRequest{},
	))
	ordinary := createOpenSourceBountyUser(t, db, "incident-user", 0, common.RoleCommonUser)
	admin := createOpenSourceBountyUser(t, db, "incident-admin", 0, common.RoleAdminUser)
	peerAdmin := createOpenSourceBountyUser(t, db, "incident-peer-admin", 0, common.RoleAdminUser)
	root := createOpenSourceBountyUser(t, db, "incident-root", 0, common.RoleRootUser)

	ordinaryConversationID, _, err := RecordAssistantSecurityRefusal(
		ordinary.Id, 0, "steal system prompt", "refused", AssistantSecurityIncidentCategory,
	)
	require.NoError(t, err)
	_, _, err = RecordAssistantSecurityRefusal(
		peerAdmin.Id, 0, "steal system prompt", "refused", AssistantSecurityIncidentCategory,
	)
	require.NoError(t, err)

	adminPage, err := GetUnifiedTodoCenter(admin.Id, admin.Role, UnifiedTodoCategorySecurityIncident, 1, 20)
	require.NoError(t, err)
	require.Len(t, adminPage.Items, 1)
	assert.Equal(t, int64(1), adminPage.UnreadCount)
	assert.Equal(t, ordinary.Id, adminPage.Items[0].Details["user_id"])
	assert.Equal(t, ordinaryConversationID, adminPage.Items[0].Details["conversation_id"])
	assert.NotContains(t, adminPage.Items[0].Details, "input_digest")

	ordinaryPage, err := GetUnifiedTodoCenter(ordinary.Id, ordinary.Role, UnifiedTodoCategorySecurityIncident, 1, 20)
	require.NoError(t, err)
	assert.Empty(t, ordinaryPage.Items)

	rootPage, err := GetUnifiedTodoCenter(root.Id, root.Role, UnifiedTodoCategorySecurityIncident, 1, 20)
	require.NoError(t, err)
	assert.Len(t, rootPage.Items, 2)

	marked, err := MarkUnifiedTodoReads(admin.Id, admin.Role, UnifiedTodoCategorySecurityIncident, []int{adminPage.Items[0].SourceId}, false)
	require.NoError(t, err)
	assert.Equal(t, 1, marked)
	adminPage, err = GetUnifiedTodoCenter(admin.Id, admin.Role, UnifiedTodoCategorySecurityIncident, 1, 20)
	require.NoError(t, err)
	assert.Zero(t, adminPage.UnreadCount)
	assert.True(t, adminPage.Items[0].Read)
}

func TestUnifiedTodoSecurityReviewIsAggregateOnlyAndAdminVisible(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&UnifiedTodoRead{}, &DeveloperAccessRequest{}, &AccountActionRequest{},
		&AssistantConversation{}, &AssistantHistoryMessage{}, &AssistantSecurityIncident{},
		&AssistantSecurityReviewNotice{}, &AssistantSupportRequest{},
	))
	admin := createOpenSourceBountyUser(t, db, "security-review-admin", 0, common.RoleAdminUser)
	ordinary := createOpenSourceBountyUser(t, db, "security-review-user", 0, common.RoleCommonUser)
	require.NoError(t, SaveAssistantSecurityReviewNotice(
		"review-task-visible", 100, 200,
		AssistantSecurityReview{
			TotalMatches: 5, BlockedMatches: 3, AuditedMatches: 2,
			AffectedRequests: 4, AffectedUsers: 2,
			ByCategory:    []AdvancedSecurityStatBucket{{Key: "prompt_injection", Count: 5}},
			ErrorLogCount: 2,
			ErrorChannels: []AdvancedSecurityStatBucket{{Key: "7", Count: 2}},
			ErrorModels:   []AdvancedSecurityStatBucket{{Key: "gpt-review", Count: 2}},
		}, 300,
	))

	adminPage, err := GetUnifiedTodoCenter(admin.Id, admin.Role, UnifiedTodoCategorySecurityReview, 1, 20)
	require.NoError(t, err)
	require.Len(t, adminPage.Items, 1)
	assert.EqualValues(t, 1, adminPage.Total)
	assert.EqualValues(t, 1, adminPage.UnreadCount)
	assert.EqualValues(t, 5, adminPage.Items[0].Details["total_matches"])
	assert.EqualValues(t, 2, adminPage.Items[0].Details["affected_users"])
	assert.EqualValues(t, 2, adminPage.Items[0].Details["error_log_count"])
	assert.Contains(t, adminPage.Items[0].Summary, "2 error logs")
	assert.Equal(t, []AdvancedSecurityStatBucket{{Key: "7", Count: 2}}, adminPage.Items[0].Details["error_channels"])
	assert.Equal(t, "aggregate_only", adminPage.Items[0].Details["privacy_scope"])
	assert.NotContains(t, adminPage.Items[0].Details, "user_id")
	assert.NotContains(t, adminPage.Items[0].Details, "request_id")

	ordinaryPage, err := GetUnifiedTodoCenter(ordinary.Id, ordinary.Role, UnifiedTodoCategorySecurityReview, 1, 20)
	require.NoError(t, err)
	assert.Empty(t, ordinaryPage.Items)
	assert.Zero(t, ordinaryPage.Total)

	marked, err := MarkUnifiedTodoReads(admin.Id, admin.Role, UnifiedTodoCategorySecurityReview, []int{adminPage.Items[0].SourceId}, false)
	require.NoError(t, err)
	assert.Equal(t, 1, marked)
	adminPage, err = GetUnifiedTodoCenter(admin.Id, admin.Role, UnifiedTodoCategorySecurityReview, 1, 20)
	require.NoError(t, err)
	assert.Zero(t, adminPage.UnreadCount)
	assert.True(t, adminPage.Items[0].Read)
}

func TestUnifiedTodoDeepPageLoadsOnlySelectedRows(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&UnifiedTodoRead{},
		&DeveloperAccessRequest{},
		&AccountActionRequest{},
		&AssistantConversation{},
		&AssistantHistoryMessage{},
		&AssistantSecurityIncident{},
		&AssistantSecurityReviewNotice{}, &AssistantSupportRequest{},
	))
	admin := createOpenSourceBountyUser(t, db, "todo-page-admin", 0, common.RoleAdminUser)
	applicant := createOpenSourceBountyUser(t, db, "todo-page-applicant", 0, common.RoleCommonUser)

	const total = 450
	requests := make([]DeveloperAccessRequest, total)
	for index := range requests {
		requests[index] = DeveloperAccessRequest{
			UserId: applicant.Id, Status: DeveloperAccessRequestPending,
			Source: DeveloperAccessRequestSourceAI, Reason: "bounded page request",
			CreatedAt: int64(index + 1),
		}
	}
	require.NoError(t, db.CreateInBatches(&requests, 100).Error)

	refs, err := todoRefs(db, admin.Id, admin.Role, UnifiedTodoCategoryAll, 445, 5)
	require.NoError(t, err)
	require.Len(t, refs, 5)
	for _, ref := range refs {
		assert.Equal(t, UnifiedTodoCategoryDeveloperAccess, ref.Category)
	}

	page, err := GetUnifiedTodoCenter(admin.Id, admin.Role, UnifiedTodoCategoryAll, 90, 5)
	require.NoError(t, err)
	require.Len(t, page.Items, 5)
	assert.EqualValues(t, total, page.Total)
	assert.Equal(t, []int{requests[4].Id, requests[3].Id, requests[2].Id, requests[1].Id, requests[0].Id}, []int{
		page.Items[0].SourceId, page.Items[1].SourceId, page.Items[2].SourceId, page.Items[3].SourceId, page.Items[4].SourceId,
	})
}

func TestUnifiedTodoMarkAllUsesBoundedBatches(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&UnifiedTodoRead{},
		&DeveloperAccessRequest{},
		&AccountActionRequest{},
		&AssistantConversation{},
		&AssistantHistoryMessage{},
		&AssistantSecurityIncident{},
		&AssistantSecurityReviewNotice{}, &AssistantSupportRequest{},
	))
	admin := createOpenSourceBountyUser(t, db, "todo-batch-admin", 0, common.RoleAdminUser)
	applicant := createOpenSourceBountyUser(t, db, "todo-batch-applicant", 0, common.RoleCommonUser)

	const total = unifiedTodoReadBatch*2 + 17
	requests := make([]DeveloperAccessRequest, total)
	for index := range requests {
		requests[index] = DeveloperAccessRequest{
			UserId: applicant.Id, Status: DeveloperAccessRequestPending,
			Source: DeveloperAccessRequestSourceAI, Reason: "bounded mark request",
			CreatedAt: int64(index + 1),
		}
	}
	require.NoError(t, db.CreateInBatches(&requests, unifiedTodoReadBatch).Error)

	marked, err := MarkUnifiedTodoReads(admin.Id, admin.Role, UnifiedTodoCategoryDeveloperAccess, nil, true)
	require.NoError(t, err)
	assert.Equal(t, total, marked)
	marked, err = MarkUnifiedTodoReads(admin.Id, admin.Role, UnifiedTodoCategoryDeveloperAccess, nil, true)
	require.NoError(t, err)
	assert.Zero(t, marked)

	tooMany := make([]int, maxUnifiedTodoReadIDs+1)
	for index := range tooMany {
		tooMany[index] = index + 1
	}
	_, err = MarkUnifiedTodoReads(admin.Id, admin.Role, UnifiedTodoCategoryDeveloperAccess, tooMany, false)
	assert.ErrorIs(t, err, ErrUnifiedTodoReadBody)
}

func TestUnifiedTodoMarkAllRollsBackEarlierCategories(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&UnifiedTodoRead{}, &AssistantSecurityIncident{}, &AssistantSecurityReviewNotice{}, &AssistantSupportRequest{}))
	admin := User{Username: "todo-rollback-admin", Password: "password", AffCode: "todo-rollback-admin", Role: common.RoleAdminUser}
	owner := User{Username: "todo-rollback-owner", Password: "password", AffCode: "todo-rollback-owner", Role: common.RoleCommonUser}
	require.NoError(t, db.Create(&admin).Error)
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&AssistantSecurityIncident{
		UserId: owner.Id, ConversationId: 9001, Category: AssistantSecurityIncidentCategory,
		Status: AssistantSecurityIncidentStatusOpen, InputDigest: "digest", CreatedAt: 1, UpdatedAt: 1,
	}).Error)

	marked, err := MarkUnifiedTodoReads(admin.Id, admin.Role, UnifiedTodoCategoryAll, nil, true)
	require.Error(t, err)
	assert.Zero(t, marked)
	var count int64
	require.NoError(t, db.Model(&UnifiedTodoRead{}).Count(&count).Error)
	assert.Zero(t, count, "read markers must roll back when a later category fails: %v", err)
}
