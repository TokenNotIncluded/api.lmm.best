package model

import "gorm.io/gorm"

// lockAssistantOwner serializes private assistant writes with account
// deletion. A scoped lookup deliberately rejects soft-deleted owners.
func lockAssistantOwner(tx *gorm.DB, userID int) error {
	if tx == nil || userID <= 0 {
		return gorm.ErrInvalidData
	}
	var owner User
	return lockForUpdate(tx).Select("id").Where("id = ?", userID).First(&owner).Error
}

// deleteUserAssistantData removes private assistant state inside the caller's
// user-deletion transaction. Aggregate review and preset statistics are kept
// because they contain no user or conversation identifier.
func deleteUserAssistantData(tx *gorm.DB, userID int) error {
	if tx == nil || userID <= 0 {
		return gorm.ErrInvalidData
	}
	conversations := tx.Model(&AssistantConversation{}).Select("id").Where("user_id = ?", userID)
	incidents := tx.Model(&AssistantSecurityIncident{}).Select("id").
		Where("user_id = ? OR conversation_id IN (?)", userID, conversations)
	requests := tx.Model(&DeveloperAccessRequest{}).Select("id").Where("user_id = ?", userID)

	supportIDs := tx.Model(&AssistantSupportRequest{}).Select("id").Where("user_id = ?", userID)
	if err := tx.Where("category = ? AND item_id IN (?)", UnifiedTodoCategoryHumanSupport, supportIDs).Delete(&UnifiedTodoRead{}).Error; err != nil {
		return err
	}
	if err := tx.Where("user_id = ?", userID).Delete(&AssistantSupportRequest{}).Error; err != nil {
		return err
	}
	assignedIDs := tx.Model(&AssistantSupportRequest{}).Select("id").Where("assigned_admin_id = ? AND active_user_id IS NOT NULL", userID)
	if err := tx.Where("category = ? AND item_id IN (?)", UnifiedTodoCategoryHumanSupport, assignedIDs).Delete(&UnifiedTodoRead{}).Error; err != nil {
		return err
	}
	if err := tx.Model(&AssistantSupportRequest{}).Where("assigned_admin_id = ? AND active_user_id IS NOT NULL", userID).Updates(map[string]any{"status": AssistantSupportStatusPending, "assigned_admin_id": 0, "assigned_admin_name": "", "accepted_at": 0}).Error; err != nil {
		return err
	}

	deletes := []struct {
		model any
		where string
		args  []any
	}{
		{&UnifiedTodoRead{}, "user_id = ?", []any{userID}},
		{&UnifiedTodoRead{}, "category = ? AND item_id IN (?)", []any{UnifiedTodoCategorySecurityIncident, incidents}},
		{&UnifiedTodoRead{}, "category = ? AND item_id IN (?)", []any{UnifiedTodoCategoryDeveloperAccess, requests}},
		{&PromptConversationRef{}, "conversation_id IN (?)", []any{conversations}},
		{&PromptConversionRef{}, "request_id IN (?)", []any{requests}},
		{&AssistantSecureCard{}, "owner_user_id = ? OR conversation_id IN (?)", []any{userID, conversations}},
		{&AssistantHistoryMessage{}, "conversation_id IN (?)", []any{conversations}},
		{&AssistantSecurityIncident{}, "user_id = ? OR conversation_id IN (?)", []any{userID, conversations}},
		{&AssistantConversation{}, "user_id = ?", []any{userID}},
		{&AssistantLead{}, "user_id = ?", []any{userID}},
		{&AssistantMemory{}, "user_id = ?", []any{userID}},
		{&AssistantUserProfile{}, "user_id = ?", []any{userID}},
		{&AssistantUserProfileAudit{}, "user_id = ?", []any{userID}},
		{&AssistantNewUserGift{}, "user_id = ?", []any{userID}},
		{&AdvancedSecurityEvent{}, "user_id = ?", []any{userID}},
		{&DeveloperAccessRequest{}, "user_id = ?", []any{userID}},
		{&AccountActionRequest{}, "target_user_id = ? OR requested_by_user_id = ?", []any{userID, userID}},
		{&L1OnboardingTodo{}, "user_id = ?", []any{userID}},
	}
	for _, deletion := range deletes {
		if err := tx.Unscoped().Where(deletion.where, deletion.args...).Delete(deletion.model).Error; err != nil {
			return err
		}
	}
	// These tables were added after the original assistant lifecycle fixtures;
	// tolerate an older test/installation schema while deleting them whenever
	// the current migration has created them.
	if tx.Migrator().HasTable(&AssistantWeeklyDiscount{}) {
		if err := tx.Unscoped().Where("user_id = ?", userID).Delete(&AssistantWeeklyDiscount{}).Error; err != nil {
			return err
		}
	}
	if tx.Migrator().HasTable(&DiscountCode{}) {
		if err := tx.Unscoped().Where("owner_user_id = ?", userID).Delete(&DiscountCode{}).Error; err != nil {
			return err
		}
	}
	// A deleted administrator must not remain as the apparent resolver of
	// another user's support request.
	for _, record := range []any{&AssistantLead{}, &DeveloperAccessRequest{}, &AccountActionRequest{}} {
		if err := tx.Model(record).Where("admin_user_id = ?", userID).Update("admin_user_id", 0).Error; err != nil {
			return err
		}
	}
	for _, record := range []any{&AssistantMemory{}, &AssistantUserProfile{}} {
		if err := tx.Model(record).Where("updated_by = ?", userID).Update("updated_by", 0).Error; err != nil {
			return err
		}
	}
	return nil
}
