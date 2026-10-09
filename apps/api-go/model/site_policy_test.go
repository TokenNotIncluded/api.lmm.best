package model

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupSitePolicyModelTest(t *testing.T) {
	setupPriceLockTest(t)
	before := *system_setting.GetLegalSettings()
	t.Cleanup(func() { *system_setting.GetLegalSettings() = before })
}

func TestSitePolicyBaselineProtectsAgainstOtherDashboardWriters(t *testing.T) {
	setupSitePolicyModelTest(t)
	require.NoError(t, UpdateOption("legal.user_agreement", "original"))
	baseline, err := ReadSitePolicies(context.Background())
	require.NoError(t, err)
	require.NoError(t, UpdateOption("legal.user_agreement", "other editor"))
	_, err = UpdateSitePolicyWithExpectation(map[string]string{"legal.user_agreement": "stale edit"}, map[string]string{"legal.user_agreement": baseline["legal.user_agreement"]})
	require.ErrorIs(t, err, ErrSitePolicyChanged)
	assert.Equal(t, "other editor", persistedPriceOption(t, "legal.user_agreement"))
}

func TestSitePolicyConcurrentConfirmationsHaveOneWinner(t *testing.T) {
	setupSitePolicyModelTest(t)
	require.NoError(t, UpdateOption("legal.refund_policy", "original"))
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for _, text := range []string{"first editor", "second editor"} {
		wait.Add(1)
		go func(value string) {
			defer wait.Done()
			_, err := UpdateSitePolicyWithExpectation(map[string]string{"legal.refund_policy": value}, map[string]string{"legal.refund_policy": "original"})
			results <- err
		}(text)
	}
	wait.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrSitePolicyChanged) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	assert.Equal(t, 1, success)
	assert.Equal(t, 1, conflicts)
}

func TestSitePolicyEnglishPreviewAlsoChecksPrimaryFallback(t *testing.T) {
	setupSitePolicyModelTest(t)
	require.NoError(t, UpdateOption("legal.privacy_policy", "中文原文"))
	require.NoError(t, UpdateOption("legal.privacy_policy", "新版中文"))
	_, err := UpdateSitePolicyWithExpectation(map[string]string{"legal.privacy_policy_en": "English translation"}, map[string]string{"legal.privacy_policy": "中文原文", "legal.privacy_policy_en": ""})
	require.ErrorIs(t, err, ErrSitePolicyChanged)
	values, err := ReadSitePolicies(context.Background())
	require.NoError(t, err)
	assert.Empty(t, values["legal.privacy_policy_en"])
	for _, values := range []map[string]string{{"PayKey": "blocked"}, {"legal.unknown": "blocked"}, {"legal.refund_policy": "a", "legal.user_agreement": "b"}} {
		_, err := UpdateSitePolicyWithExpectation(values, map[string]string{"legal.refund_policy": ""})
		require.Error(t, err)
	}
}
