package controller

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantKeyPostgresTwoFactorFailureAndBackupConsumptionAreAuthoritative(t *testing.T) {
	harness := openAssistantKeyPostgresHarness(t)
	backupHash, err := common.Password2Hash("ABCD-EFGH")
	require.NoError(t, err)
	require.NoError(t, harness.db.Create(&model.TwoFA{UserId: 7, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true}).Error)
	require.NoError(t, harness.db.Create(&model.TwoFABackupCode{UserId: 7, CodeHash: backupHash, IsUsed: false}).Error)
	flow := createAssistantKeyPostgresFlow(t, "two-factor-session", nil)

	err = consumeAssistantKeyPostgresFlowWithTwoFactor(flow, "two-factor-session", "")
	require.ErrorIs(t, err, model.ErrAssistantKeyTwoFactorInvalid)
	assert.EqualValues(t, 1, countAssistantKeyPostgresRows(t, harness.db, "two_fas"))
	assert.EqualValues(t, 1, countAssistantKeyPostgresRows(t, harness.db, "two_fa_backup_codes"))
	assert.EqualValues(t, 0, countAssistantKeyPostgresRows(t, harness.db, "tokens"))
	assert.EqualValues(t, 0, countAssistantKeyPostgresRows(t, harness.db, "assistant_secure_cards"))

	require.NoError(t, consumeAssistantKeyPostgresFlowWithTwoFactor(flow, "two-factor-session", "ABCD-EFGH"))
	assert.EqualValues(t, 1, countAssistantKeyPostgresRows(t, harness.db, "tokens"))
	assert.EqualValues(t, 1, countAssistantKeyPostgresRows(t, harness.db, "assistant_secure_cards"))
	var usedCount int64
	require.NoError(t, harness.db.Raw("SELECT COUNT(*) FROM two_fa_backup_codes WHERE user_id = 7 AND is_used = TRUE").Scan(&usedCount).Error)
	assert.EqualValues(t, 1, usedCount)
}
