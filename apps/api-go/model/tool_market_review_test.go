package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestToolMarketPublisherCannotApproveOwnVersion(t *testing.T) {
	for name, role := range map[string]int{"admin": common.RoleAdminUser, "root": common.RoleRootUser} {
		t.Run(name, func(t *testing.T) {
			db := marketTestDB(t)
			owner := marketTestUser(t, db, "publisher", 0, role)
			reviewer := marketTestUser(t, db, "reviewer", 0, common.RoleAdminUser)
			product, err := SaveToolMarketDraft(owner.Id, "", marketTestDraft(100))
			require.NoError(t, err)
			versionID := product.DraftVersionID
			require.NoError(t, SubmitToolMarketDraft(owner.Id, product.ID, versionID))
			// Trusted validation is already present: the owner restriction must
			// stand independently of remote-validation and administrator checks.
			require.NoError(t, db.Model(&ToolMarketVersion{}).Where("id = ?", versionID).Update("validation_digest", gorm.Expr("digest")).Error)
			require.ErrorIs(t, ReviewToolMarketVersion(owner.Id, product.ID, versionID, true, "Approve my own paid service"), ErrToolMarketDenied)
			var version ToolMarketVersion
			require.NoError(t, db.First(&version, "id = ?", versionID).Error)
			require.Equal(t, "pending", version.Status)
			require.Zero(t, version.ReviewedBy)
			require.NoError(t, db.First(product, "id = ?", product.ID).Error)
			require.Empty(t, product.LiveVersionID)
			var events int64
			require.NoError(t, db.Model(&ToolMarketEvent{}).Where("action = ?", "version.review").Count(&events).Error)
			require.Zero(t, events)
			// An administrator publisher may withdraw a pending submission by
			// rejecting it, but publishing after resubmission needs another admin.
			require.NoError(t, ReviewToolMarketVersion(owner.Id, product.ID, versionID, false, "Withdraw my submission"))
			require.NoError(t, db.First(&version, "id = ?", versionID).Error)
			require.Equal(t, "rejected", version.Status)
			require.NoError(t, SubmitToolMarketDraft(owner.Id, product.ID, versionID))
			require.NoError(t, ReviewToolMarketVersion(reviewer.Id, product.ID, versionID, true, "Independent approval"))
			require.NoError(t, db.First(product, "id = ?", product.ID).Error)
			require.Equal(t, versionID, product.LiveVersionID)
		})
	}
}
