package controller

import (
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type assistantKeyPostgresSecurityMutation struct {
	name          string
	sessionID     string
	twoFactorCode string
	seed          func(t *testing.T, db *gorm.DB)
	mutate        func(t *testing.T, tx *gorm.DB, sessionID string)
	want          error
}

func runAssistantKeyPostgresSecurityMutation(t *testing.T, test assistantKeyPostgresSecurityMutation) {
	harness := openAssistantKeyPostgresHarness(t)
	if test.seed != nil {
		test.seed(t, harness.db)
	}
	flowToken := createAssistantKeyPostgresFlow(t, test.sessionID, nil)

	mutation := harness.db.Begin()
	require.NoError(t, mutation.Error)
	committed := false
	defer func() {
		if !committed {
			require.NoError(t, mutation.Rollback().Error)
		}
	}()
	var blockerPID int
	require.NoError(t, mutation.Raw("SELECT pg_backend_pid()").Scan(&blockerPID).Error)
	var userID int
	require.NoError(t, mutation.Raw("SELECT id FROM users WHERE id = ? FOR UPDATE", 7).Scan(&userID).Error)
	require.Equal(t, 7, userID)
	test.mutate(t, mutation, test.sessionID)

	confirmation := make(chan error, 1)
	go func() {
		confirmation <- consumeAssistantKeyPostgresFlowWithTwoFactor(
			flowToken,
			test.sessionID,
			test.twoFactorCode,
		)
	}()
	waitForAssistantKeyPostgresBlockedBy(t, harness.db, blockerPID)
	require.NoError(t, mutation.Commit().Error)
	committed = true

	select {
	case err := <-confirmation:
		require.ErrorIs(t, err, test.want)
	case <-time.After(5 * time.Second):
		t.Fatal("assistant-key confirmation did not finish after the security mutation committed")
	}

	var flow model.AuthFlow
	require.NoError(t, harness.db.Where("session_id = ?", test.sessionID).First(&flow).Error)
	assert.Nil(t, flow.ConsumedAt)
	assert.EqualValues(t, 0, countAssistantKeyPostgresRows(t, harness.db, "tokens"))
	assert.EqualValues(t, 0, countAssistantKeyPostgresRows(t, harness.db, "assistant_secure_cards"))
}

func TestAssistantKeyPostgresCommitTimeAuthorizationFenceRejectsCompletedSecurityMutations(t *testing.T) {
	cases := []assistantKeyPostgresSecurityMutation{
		{
			name:      "disabled account",
			sessionID: "fence-disabled-account",
			mutate: func(t *testing.T, tx *gorm.DB, _ string) {
				require.NoError(t, tx.Model(&model.User{}).Where("id = ?", 7).Update("status", 0).Error)
			},
			want: model.ErrAssistantKeyAuthorizationChanged,
		},
		{
			name:      "revoked L1 access",
			sessionID: "fence-revoked-l1",
			mutate: func(t *testing.T, tx *gorm.DB, _ string) {
				require.NoError(t, tx.Model(&model.User{}).Where("id = ?", 7).Update("trust_level_override", 0).Error)
			},
			want: model.ErrAssistantKeyAuthorizationChanged,
		},
		{
			name:      "revoked session",
			sessionID: "fence-revoked-session",
			mutate: func(t *testing.T, tx *gorm.DB, sessionID string) {
				require.NoError(t, tx.Model(&model.UserSession{}).
					Where("sid = ? AND user_id = ?", sessionID, 7).
					Updates(map[string]any{
						"status":     model.UserSessionStatusRevoked,
						"revoked_at": time.Now().Unix(),
					}).Error)
			},
			want: model.ErrAssistantKeyAuthorizationChanged,
		},
		{
			name:      "bumped session version",
			sessionID: "fence-session-version",
			mutate: func(t *testing.T, tx *gorm.DB, sessionID string) {
				require.NoError(t, tx.Exec(
					"UPDATE user_sessions SET version = version + 1 WHERE sid = ? AND user_id = ?",
					sessionID,
					7,
				).Error)
			},
			want: model.ErrAssistantKeyAuthorizationChanged,
		},
		{
			name:      "bumped user auth version",
			sessionID: "fence-user-auth-version",
			mutate: func(t *testing.T, tx *gorm.DB, _ string) {
				require.NoError(t, tx.Exec("UPDATE users SET auth_version = auth_version + 1 WHERE id = ?", 7).Error)
			},
			want: model.ErrAssistantKeyAuthorizationChanged,
		},
		{
			name:      "enabled two factor",
			sessionID: "fence-enabled-two-factor",
			mutate: func(t *testing.T, tx *gorm.DB, _ string) {
				require.NoError(t, tx.Create(&model.TwoFA{
					UserId: 7, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true,
				}).Error)
			},
			want: model.ErrAssistantKeyTwoFactorInvalid,
		},
		{
			name:          "rotated two factor backup codes",
			sessionID:     "fence-rotated-two-factor",
			twoFactorCode: "ABCD-EFGH",
			seed: func(t *testing.T, db *gorm.DB) {
				oldHash, err := common.Password2Hash("ABCD-EFGH")
				require.NoError(t, err)
				require.NoError(t, db.Create(&model.TwoFA{
					UserId: 7, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true,
				}).Error)
				require.NoError(t, db.Create(&model.TwoFABackupCode{
					UserId: 7, CodeHash: oldHash,
				}).Error)
			},
			mutate: func(t *testing.T, tx *gorm.DB, _ string) {
				newHash, err := common.Password2Hash("WXYZ-1234")
				require.NoError(t, err)
				require.NoError(t, tx.Model(&model.TwoFABackupCode{}).
					Where("user_id = ? AND is_used = ?", 7, false).
					Update("is_used", true).Error)
				require.NoError(t, tx.Create(&model.TwoFABackupCode{
					UserId: 7, CodeHash: newHash,
				}).Error)
			},
			want: model.ErrAssistantKeyTwoFactorInvalid,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			runAssistantKeyPostgresSecurityMutation(t, test)
		})
	}
}
