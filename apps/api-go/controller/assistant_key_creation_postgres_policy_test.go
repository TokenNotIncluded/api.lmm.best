package controller

import (
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantKeyPostgresWarningDefaultMatchesRuntimePolicy(t *testing.T) {
	harness := openAssistantKeyPostgresHarness(t)
	seedAssistantKeyPostgresOptions(t, harness.db, map[string]string{
		"GroupRatio": `{"default":0,"vip":2}`,
	})

	withoutWarning := createAssistantKeyPostgresFlow(t, "missing-warning", nil)
	require.ErrorIs(t, consumeAssistantKeyPostgresFlow(withoutWarning, "missing-warning"), errAssistantKeyWarningChanged)

	defaultWarning := ratio_setting.GroupWarning{
		Enabled:       true,
		Message:       zeroRatioWarningMessage,
		Mode:          "modal",
		Confirmations: 3,
	}
	withWarning := createAssistantKeyPostgresFlow(t, "matched-warning", &defaultWarning)
	require.NoError(t, consumeAssistantKeyPostgresFlow(withWarning, "matched-warning"))
	assert.EqualValues(t, 1, countAssistantKeyPostgresRows(t, harness.db, "tokens"))
	assert.EqualValues(t, 1, countAssistantKeyPostgresRows(t, harness.db, "assistant_secure_cards"))
}

func TestAssistantKeyPostgresDisabledUserCannotConsumePreparedFlow(t *testing.T) {
	harness := openAssistantKeyPostgresHarness(t)
	flow := createAssistantKeyPostgresFlow(t, "disabled-user", nil)
	require.NoError(t, harness.db.Model(&model.User{}).Where("id = ?", 7).Update("status", 0).Error)

	err := consumeAssistantKeyPostgresFlow(flow, "disabled-user")
	require.ErrorIs(t, err, model.ErrAssistantKeyAuthorizationChanged)
	var authFlow model.AuthFlow
	require.NoError(t, harness.db.Where("session_id = ?", "disabled-user").First(&authFlow).Error)
	assert.Nil(t, authFlow.ConsumedAt)
	assert.EqualValues(t, 0, countAssistantKeyPostgresRows(t, harness.db, "tokens"))
	assert.EqualValues(t, 0, countAssistantKeyPostgresRows(t, harness.db, "assistant_secure_cards"))
}

func TestAssistantKeyPostgresConcurrentReplayCreatesExactlyOneCredential(t *testing.T) {
	harness := openAssistantKeyPostgresHarness(t)
	flow := createAssistantKeyPostgresFlow(t, "replay-session", nil)
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- consumeAssistantKeyPostgresFlow(flow, "replay-session")
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var successes, failures int
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		failures++
		require.ErrorIs(t, err, model.ErrAuthFlowConsumed)
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, failures)
	assert.EqualValues(t, 1, countAssistantKeyPostgresRows(t, harness.db, "tokens"))
	assert.EqualValues(t, 1, countAssistantKeyPostgresRows(t, harness.db, "assistant_secure_cards"))
}
