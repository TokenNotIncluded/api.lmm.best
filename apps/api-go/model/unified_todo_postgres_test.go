package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPostgresUnifiedTodoUsesOneSnapshot(t *testing.T) {
	usePostgresDatabaseType(t)
	db := openIsolatedPostgresCacheTestDB(t,
		&User{}, &DeveloperAccessRequest{}, &UnifiedTodoRead{}, &AccountActionRequest{},
		&AssistantSecurityIncident{}, &AssistantSecurityReviewNotice{}, &AssistantSupportRequest{}, &OpenSourceBountyProject{}, &OpenSourceBountyChallenge{},
		&OpenSourceBountyLedger{},
	)
	previousDB, previousLogDB := DB, LOG_DB
	DB, LOG_DB = db, db
	t.Cleanup(func() { DB, LOG_DB = previousDB, previousLogDB })

	admin := User{Username: "todo-snapshot-admin", Password: "password", AffCode: "todo-snapshot-admin", Role: common.RoleAdminUser}
	applicant := User{Username: "todo-snapshot-applicant", Password: "password", AffCode: "todo-snapshot-applicant", Role: common.RoleCommonUser}
	require.NoError(t, db.Create(&admin).Error)
	require.NoError(t, db.Create(&applicant).Error)
	request := DeveloperAccessRequest{
		UserId: applicant.Id, Status: DeveloperAccessRequestPending,
		Source: DeveloperAccessRequestSourceAI, Reason: "snapshot request", CreatedAt: 1,
	}
	require.NoError(t, db.Create(&request).Error)

	var candidates []unifiedTodoCandidate
	err := todoTx(true, func(tx *gorm.DB) error {
		refs, err := todoRefs(tx, admin.Id, admin.Role, UnifiedTodoCategoryDeveloperAccess, 0, 20)
		if err != nil {
			return err
		}
		require.Len(t, refs, 1)
		if err := db.Model(&DeveloperAccessRequest{}).Where("id = ?", request.Id).
			Update("status", DeveloperAccessRequestApproved).Error; err != nil {
			return err
		}
		candidates, err = loadTodoCandidates(tx, admin.Id, admin.Role, refs)
		return err
	})
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.Equal(t, request.Id, candidates[0].Item.SourceId)

	page, err := GetUnifiedTodoCenter(admin.Id, admin.Role, UnifiedTodoCategoryDeveloperAccess, 1, 20)
	require.NoError(t, err)
	assert.Empty(t, page.Items)
	assert.Zero(t, page.Total)
}
