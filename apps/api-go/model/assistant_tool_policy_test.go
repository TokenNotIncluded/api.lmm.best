package model

import (
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/stretchr/testify/require"
)

func setupAssistantToolPolicyOptionTest(t *testing.T) {
	t.Helper()
	setupPriceLockTest(t)
	previous := setting.GetAssistantSettings().ToolPolicy
	t.Cleanup(func() { require.NoError(t, setting.UpdateAssistantToolPolicy(previous)) })
}

func TestAssistantToolPolicyExpectationCanonicalBaselineAndLegacyDefault(t *testing.T) {
	setupAssistantToolPolicyOptionTest(t)
	legacy := ""
	first := `{"tools":{"search_web":false},"groups":{},"version":1}`
	_, err := UpdateOptionsBulkWithAssistantToolPolicyExpectation(map[string]string{setting.AssistantToolPolicyOptionKey: first}, &legacy)
	require.NoError(t, err)
	require.JSONEq(t, first, persistedPriceOption(t, setting.AssistantToolPolicyOptionKey))
	require.False(t, setting.AssistantToolEnabled("search_web"))
	// Whitespace and key order are irrelevant; only the committed policy matters.
	baseline := ` {"version":1,"tools":{"search_web":false},"groups":{}} `
	merged := `{"version":1,"groups":{"drawing":false},"tools":{"search_web":false}}`
	_, err = UpdateOptionsBulkWithAssistantToolPolicyExpectation(map[string]string{setting.AssistantToolPolicyOptionKey: merged}, &baseline)
	require.NoError(t, err)
	require.JSONEq(t, merged, persistedPriceOption(t, setting.AssistantToolPolicyOptionKey))
	require.False(t, setting.AssistantToolEnabled("prepare_image_generation"))
	require.False(t, setting.AssistantToolEnabled("search_web"))
}

func TestAssistantToolPolicyLegacyWriterInvalidatesStaleGuardedBatch(t *testing.T) {
	setupAssistantToolPolicyOptionTest(t)
	baseline := setting.DefaultAssistantToolPolicy
	remote := `{"version":1,"groups":{"drawing":false},"tools":{}}`
	// The existing single-option writer on another session must share the lock.
	require.NoError(t, UpdateOption(setting.AssistantToolPolicyOptionKey, remote))
	_, err := UpdateOptionsBulkWithAssistantToolPolicyExpectation(map[string]string{
		setting.AssistantToolPolicyOptionKey: `{"version":1,"groups":{},"tools":{"search_web":false}}`,
		"Notice":                             "must not save",
	}, &baseline)
	require.ErrorIs(t, err, ErrAssistantToolPolicyConflict)
	require.JSONEq(t, remote, persistedPriceOption(t, setting.AssistantToolPolicyOptionKey))
	require.False(t, setting.AssistantToolEnabled("prepare_image_generation"))
	require.True(t, setting.AssistantToolEnabled("search_web"))
	var count int64
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", "Notice").Count(&count).Error)
	require.Zero(t, count)
}

func TestAssistantToolPolicyConcurrentBaselinesCannotOverwriteWinner(t *testing.T) {
	setupAssistantToolPolicyOptionTest(t)
	baseline := setting.DefaultAssistantToolPolicy
	policies := []string{
		`{"version":1,"groups":{"drawing":false},"tools":{}}`,
		`{"version":1,"groups":{},"tools":{"search_web":false}}`,
	}
	errors := make([]error, len(policies))
	var workers sync.WaitGroup
	start := make(chan struct{})
	for index, policy := range policies {
		workers.Add(1)
		go func(index int, policy string) {
			defer workers.Done()
			<-start
			_, errors[index] = UpdateOptionsBulkWithAssistantToolPolicyExpectation(map[string]string{setting.AssistantToolPolicyOptionKey: policy}, &baseline)
		}(index, policy)
	}
	close(start)
	workers.Wait()
	winner := -1
	for index, err := range errors {
		if err == nil {
			require.Equal(t, -1, winner, "exactly one stale-baseline write may win")
			winner = index
		} else {
			require.ErrorIs(t, err, ErrAssistantToolPolicyConflict)
		}
	}
	require.NotEqual(t, -1, winner)
	require.JSONEq(t, policies[winner], persistedPriceOption(t, setting.AssistantToolPolicyOptionKey))
}

func TestAssistantToolPolicyRejectedFirstWriteDoesNotCreateDefaultRow(t *testing.T) {
	setupAssistantToolPolicyOptionTest(t)
	wrong := `{"version":1,"groups":{"drawing":false},"tools":{}}`
	_, err := UpdateOptionsBulkWithAssistantToolPolicyExpectation(map[string]string{setting.AssistantToolPolicyOptionKey: setting.DefaultAssistantToolPolicy}, &wrong)
	require.ErrorIs(t, err, ErrAssistantToolPolicyConflict)
	var count int64
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", setting.AssistantToolPolicyOptionKey).Count(&count).Error)
	require.Zero(t, count, "a failed comparison must roll back creation of the lock row")
}
