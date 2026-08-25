package controller

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantKeyPostgresConfigurationLockSurvivesThroughCredentialCommit(t *testing.T) {
	harness := openAssistantKeyPostgresHarness(t)
	flow := createAssistantKeyPostgresFlow(t, "config-lock-session", nil)
	require.NoError(t, harness.db.Exec(`UPDATE options SET value = '{"default":0,"vip":2}' WHERE key = 'GroupRatio'`).Error)

	err := consumeAssistantKeyPostgresFlow(flow, "config-lock-session")
	require.ErrorIs(t, err, errAssistantKeyWarningChanged)
	assert.EqualValues(t, 0, countAssistantKeyPostgresRows(t, harness.db, "tokens"))
	assert.EqualValues(t, 0, countAssistantKeyPostgresRows(t, harness.db, "assistant_secure_cards"))
	var authFlow model.AuthFlow
	require.NoError(t, harness.db.Where("session_id = ?", "config-lock-session").First(&authFlow).Error)
	assert.Nil(t, authFlow.ConsumedAt)

}
