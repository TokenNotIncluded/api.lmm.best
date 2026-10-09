package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnifiedTodoIncludesCurrentModeration(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&UnifiedTodoRead{}, &DeveloperAccessRequest{}, &AccountActionRequest{},
		&AssistantConversation{}, &AssistantHistoryMessage{}, &AssistantSecurityIncident{},
		&AssistantSupportRequest{}, &ModerationNotice{},
	))
	admin := createOpenSourceBountyUser(t, db, "moderation-todo-admin", 0, common.RoleAdminUser)
	now := common.GetTimestamp()
	require.NoError(t, db.Create(&ModerationNotice{
		UserID: admin.Id, JobID: 1, RequestID: "current-moderation", Source: ModerationSourceRelayInput,
		Mode: "tolerant", CategoriesJSON: `["violence"]`, CreatedAt: now, UpdatedAt: now,
	}).Error)
	page, err := GetUnifiedTodoCenter(admin.Id, admin.Role, UnifiedTodoCategoryAll, 1, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Total)
	require.EqualValues(t, 1, page.TotalUnreadCount)
	require.Len(t, page.Items, 1)
	require.Equal(t, UnifiedTodoCategoryModeration, page.Items[0].Category)
	for _, category := range page.Categories {
		require.NotEqual(t, "security_review", category.Key)
	}
	_, err = GetUnifiedTodoCenter(admin.Id, admin.Role, "security_review", 1, 20)
	require.ErrorIs(t, err, ErrUnifiedTodoCategory)
}

func TestUnifiedTodoIncludesSubmittedBountyForOwner(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	require.NoError(t, db.AutoMigrate(&UnifiedTodoRead{}, &DeveloperAccessRequest{}, &AccountActionRequest{}, &AssistantConversation{}, &AssistantHistoryMessage{}, &AssistantSecurityIncident{}, &AssistantSupportRequest{}))

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

func TestUnifiedTodoRetiredApplicationsNeverAppearOrCount(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	require.NoError(t, db.AutoMigrate(&UnifiedTodoRead{}, &DeveloperAccessRequest{}, &AccountActionRequest{}, &AssistantConversation{}, &AssistantHistoryMessage{}, &AssistantSecurityIncident{}, &AssistantSupportRequest{}))
	admin := createOpenSourceBountyUser(t, db, "todo-retired-admin", 0, common.RoleAdminUser)
	user := createOpenSourceBountyUser(t, db, "todo-retired-user", 0, common.RoleCommonUser)
	for _, status := range []string{DeveloperAccessRequestPending, DeveloperAccessRequestApproved, DeveloperAccessRequestRejected} {
		require.NoError(t, db.Create(&DeveloperAccessRequest{UserId: user.Id, Status: status, Source: DeveloperAccessRequestSourceAI, Reason: "retained historical evidence"}).Error)
	}
	for _, viewer := range []User{admin, user} {
		for _, category := range []string{UnifiedTodoCategoryAll, UnifiedTodoCategoryDeveloperAccess} {
			page, err := GetUnifiedTodoCenter(viewer.Id, viewer.Role, category, 1, 20)
			require.NoError(t, err)
			assert.Empty(t, page.Items)
			assert.Zero(t, page.Total)
			assert.Zero(t, page.TotalUnreadCount)
			assert.NotContains(t, page.UnreadByCategory, UnifiedTodoCategoryDeveloperAccess)
		}
		marked, err := MarkUnifiedTodoReads(viewer.Id, viewer.Role, UnifiedTodoCategoryDeveloperAccess, nil, true)
		require.NoError(t, err)
		assert.Zero(t, marked)
	}
	var retained int64
	require.NoError(t, db.Model(&DeveloperAccessRequest{}).Count(&retained).Error)
	assert.EqualValues(t, 3, retained)
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
		&AssistantSupportRequest{},
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

func TestUnifiedTodoDeepPageLoadsOnlySelectedRows(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&UnifiedTodoRead{},
		&DeveloperAccessRequest{},
		&AccountActionRequest{},
		&AssistantConversation{},
		&AssistantHistoryMessage{},
		&AssistantSecurityIncident{},
		&AssistantSupportRequest{},
	))
	admin := createOpenSourceBountyUser(t, db, "todo-page-admin", 0, common.RoleAdminUser)
	applicant := createOpenSourceBountyUser(t, db, "todo-page-applicant", 0, common.RoleCommonUser)

	const total = 450
	requests := make([]AccountActionRequest, total)
	for index := range requests {
		requests[index] = AccountActionRequest{
			TargetUserId: applicant.Id, RequestedByUserId: applicant.Id, Status: AccountActionStatusPending,
			Kind: AccountActionKindAppeal, Reason: "bounded page request",
			CreatedAt: int64(index + 1),
		}
	}
	require.NoError(t, db.CreateInBatches(&requests, 100).Error)

	refs, err := todoRefs(db, admin.Id, admin.Role, UnifiedTodoCategoryAll, 445, 5)
	require.NoError(t, err)
	require.Len(t, refs, 5)
	for _, ref := range refs {
		assert.Equal(t, UnifiedTodoCategoryAccountAction, ref.Category)
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
		&AssistantSupportRequest{},
	))
	admin := createOpenSourceBountyUser(t, db, "todo-batch-admin", 0, common.RoleAdminUser)
	applicant := createOpenSourceBountyUser(t, db, "todo-batch-applicant", 0, common.RoleCommonUser)

	const total = unifiedTodoReadBatch*2 + 17
	requests := make([]AccountActionRequest, total)
	for index := range requests {
		requests[index] = AccountActionRequest{
			TargetUserId: applicant.Id, RequestedByUserId: applicant.Id, Status: AccountActionStatusPending,
			Kind: AccountActionKindAppeal, Reason: "bounded mark request",
			CreatedAt: int64(index + 1),
		}
	}
	require.NoError(t, db.CreateInBatches(&requests, unifiedTodoReadBatch).Error)

	marked, err := MarkUnifiedTodoReads(admin.Id, admin.Role, UnifiedTodoCategoryAccountAction, nil, true)
	require.NoError(t, err)
	assert.Equal(t, total, marked)
	marked, err = MarkUnifiedTodoReads(admin.Id, admin.Role, UnifiedTodoCategoryAccountAction, nil, true)
	require.NoError(t, err)
	assert.Zero(t, marked)

	tooMany := make([]int, maxUnifiedTodoReadIDs+1)
	for index := range tooMany {
		tooMany[index] = index + 1
	}
	_, err = MarkUnifiedTodoReads(admin.Id, admin.Role, UnifiedTodoCategoryAccountAction, tooMany, false)
	assert.ErrorIs(t, err, ErrUnifiedTodoReadBody)
}

func TestUnifiedTodoMarkAllRollsBackEarlierCategories(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&UnifiedTodoRead{}, &AssistantSecurityIncident{}, &AssistantSupportRequest{}))
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
