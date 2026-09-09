package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHumanSupportTodosUseCurrentStaffAccessAndSharedAssignment(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	require.NoError(t, db.AutoMigrate(&UnifiedTodoRead{}, &DeveloperAccessRequest{}, &AccountActionRequest{},
		&AssistantSecurityIncident{}, &AssistantSecurityReviewNotice{}, &AssistantSupportRequest{}))
	owner := createOpenSourceBountyUser(t, db, "support-owner", 0, common.RoleCommonUser)
	admin := createOpenSourceBountyUser(t, db, "support-admin", 0, common.RoleAdminUser)
	root := createOpenSourceBountyUser(t, db, "support-root", 0, common.RoleRootUser)
	disabled := createOpenSourceBountyUser(t, db, "support-disabled", 0, common.RoleRootUser)
	require.NoError(t, db.Model(&disabled).Update("status", common.UserStatusDisabled).Error)
	request := AssistantSupportRequest{UserId: owner.Id, ConversationId: 42,
		Kind: AssistantSupportKindAppointment, Status: AssistantSupportStatusPending,
		Topic: "Help with configuration", PreferredTime: "Tomorrow UTC", ScheduledAt: common.GetTimestamp() + 3600,
		CreatedAt: common.GetTimestamp(), UpdatedAt: common.GetTimestamp()}
	require.NoError(t, db.Create(&request).Error)

	for _, staff := range []User{admin, root} {
		// A newly promoted administrator must see the queue even if the caller
		// still supplies a stale ordinary-user role.
		page, err := GetUnifiedTodoCenter(staff.Id, common.RoleCommonUser, UnifiedTodoCategoryHumanSupport, 1, 20)
		require.NoError(t, err)
		require.Len(t, page.Items, 1)
		assert.Equal(t, request.Id, page.Items[0].SourceId)
		assert.EqualValues(t, 42, page.Items[0].Details["conversation_id"])
		assert.NotContains(t, page.Items[0].Details, "messages")
		assert.NotContains(t, page.Items[0].Details, "active_user_id")
		assert.EqualValues(t, 1, page.UnreadCount)
	}
	for _, viewer := range []User{owner, disabled} {
		page, err := GetUnifiedTodoCenter(viewer.Id, common.RoleRootUser, UnifiedTodoCategoryHumanSupport, 1, 20)
		require.NoError(t, err)
		assert.Zero(t, page.Total)
		assert.Empty(t, page.Items)
		marked, err := MarkUnifiedTodoReads(viewer.Id, common.RoleRootUser, UnifiedTodoCategoryHumanSupport, []int{request.Id}, false)
		require.NoError(t, err)
		assert.Zero(t, marked)
	}
	marked, err := MarkUnifiedTodoReads(admin.Id, admin.Role, UnifiedTodoCategoryHumanSupport, []int{request.Id}, false)
	require.NoError(t, err)
	assert.Equal(t, 1, marked)
	rootPage, err := GetUnifiedTodoCenter(root.Id, root.Role, UnifiedTodoCategoryHumanSupport, 1, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 1, rootPage.UnreadCount)

	require.NoError(t, db.Model(&request).Updates(map[string]any{
		"status": AssistantSupportStatusAccepted, "assigned_admin_id": admin.Id,
	}).Error)
	rootPage, err = GetUnifiedTodoCenter(root.Id, root.Role, UnifiedTodoCategoryHumanSupport, 1, 20)
	require.NoError(t, err)
	assert.Empty(t, rootPage.Items)
	adminPage, err := GetUnifiedTodoCenter(admin.Id, admin.Role, UnifiedTodoCategoryHumanSupport, 1, 20)
	require.NoError(t, err)
	require.Len(t, adminPage.Items, 1)
	assert.True(t, adminPage.Items[0].Read)

	require.NoError(t, db.Model(&admin).Update("role", common.RoleCommonUser).Error)
	adminPage, err = GetUnifiedTodoCenter(admin.Id, common.RoleRootUser, UnifiedTodoCategoryHumanSupport, 1, 20)
	require.NoError(t, err)
	assert.Empty(t, adminPage.Items)
	marked, err = MarkUnifiedTodoReads(admin.Id, common.RoleRootUser, UnifiedTodoCategoryHumanSupport, nil, true)
	require.NoError(t, err)
	assert.Zero(t, marked)

	var sources int64
	require.NoError(t, db.Model(&AssistantSupportRequest{}).Count(&sources).Error)
	assert.EqualValues(t, 1, sources, "one shared request serves every staff inbox")
}
