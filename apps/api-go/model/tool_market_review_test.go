package model

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestToolMarketAdministratorCanApproveOwnValidatedVersion(t *testing.T) {
	for name, role := range map[string]int{"admin": common.RoleAdminUser, "root": common.RoleRootUser} {
		t.Run(name, func(t *testing.T) {
			db := marketTestDB(t)
			owner := marketTestUser(t, db, "publisher", 0, role)
			product, err := SaveToolMarketDraft(owner.Id, "", marketTestDraft(100))
			require.NoError(t, err)
			versionID := product.DraftVersionID
			require.NoError(t, SubmitToolMarketDraft(owner.Id, product.ID, versionID))
			// Administrative self-approval does not bypass trusted validation.
			require.ErrorIs(t, ReviewToolMarketVersion(owner.Id, product.ID, versionID, true, "Publish my service"), ErrToolMarketDenied)
			require.NoError(t, db.Model(&ToolMarketVersion{}).Where("id = ?", versionID).Update("validation_digest", "stale").Error)
			require.ErrorIs(t, ReviewToolMarketVersion(owner.Id, product.ID, versionID, true, "Publish my service"), ErrToolMarketDenied)
			require.NoError(t, db.Model(&ToolMarketVersion{}).Where("id = ?", versionID).Update("validation_digest", gorm.Expr("digest")).Error)
			require.NoError(t, ReviewToolMarketVersion(owner.Id, product.ID, versionID, false, "Withdraw my submission"))
			require.NoError(t, SubmitToolMarketDraft(owner.Id, product.ID, versionID))
			require.NoError(t, ReviewToolMarketVersion(owner.Id, product.ID, versionID, true, "Publish my service"))
			var version ToolMarketVersion
			require.NoError(t, db.First(&version, "id = ?", versionID).Error)
			require.Equal(t, "published", version.Status)
			require.Equal(t, owner.Id, version.ReviewedBy)
			require.NoError(t, db.First(product, "id = ?", product.ID).Error)
			require.Equal(t, versionID, product.LiveVersionID)
			require.Empty(t, product.DraftVersionID)
			var events int64
			require.NoError(t, db.Model(&ToolMarketEvent{}).Where("actor_id = ? AND action = ?", owner.Id, "version.review").Count(&events).Error)
			require.EqualValues(t, 2, events)
		})
	}
}

func TestToolMarketPublisherReviewRequiresEnabledAdministrator(t *testing.T) {
	for _, scenario := range []string{"ordinary", "disabled", "demoted"} {
		t.Run(scenario, func(t *testing.T) {
			db := marketTestDB(t)
			role := common.RoleAdminUser
			if scenario == "ordinary" {
				role = common.RoleCommonUser
			}
			owner := marketTestUser(t, db, "publisher", 0, role)
			product, err := SaveToolMarketDraft(owner.Id, "", marketTestDraft(100))
			require.NoError(t, err)
			versionID := product.DraftVersionID
			require.NoError(t, SubmitToolMarketDraft(owner.Id, product.ID, versionID))
			require.NoError(t, db.Model(&ToolMarketVersion{}).Where("id = ?", versionID).Update("validation_digest", gorm.Expr("digest")).Error)
			if scenario == "disabled" {
				require.NoError(t, db.Model(&User{}).Where("id = ?", owner.Id).Update("status", common.UserStatusDisabled).Error)
			}
			if scenario == "demoted" {
				require.NoError(t, db.Model(&User{}).Where("id = ?", owner.Id).Update("role", common.RoleCommonUser).Error)
			}
			for _, approve := range []bool{true, false} {
				require.ErrorIs(t, ReviewToolMarketVersion(owner.Id, product.ID, versionID, approve, "Review my service"), ErrToolMarketDenied)
			}
			var version ToolMarketVersion
			require.NoError(t, db.First(&version, "id = ?", versionID).Error)
			require.Equal(t, "pending", version.Status)
			require.Zero(t, version.ReviewedBy)
			require.NoError(t, db.First(product, "id = ?", product.ID).Error)
			require.Empty(t, product.LiveVersionID)
		})
	}
}
