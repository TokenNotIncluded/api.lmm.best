package model

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestOptionsSnapshotWaitsForCompletePublication(t *testing.T) {
	setupPriceLockTest(t)
	require.NoError(t, UpdateOptionsBulk(map[string]string{
		"ModelPrice": `{"snapshot-model":1}`, "ModelRatio": `{"snapshot-model":2}`,
	}))
	started := make(chan struct{})
	finished := make(chan map[string]string, 1)
	func() {
		optionUpdateMutex.Lock()
		defer optionUpdateMutex.Unlock()
		require.NoError(t, updateOptionMap("ModelPrice", `{"snapshot-model":3}`))
		go func() {
			close(started)
			finished <- GetOptionsSnapshot()
		}()
		<-started
		select {
		case <-finished:
			t.Fatal("reader sampled a partly published option batch")
		case <-time.After(30 * time.Millisecond):
		}
		require.NoError(t, updateOptionMap("ModelRatio", `{"snapshot-model":4}`))
	}()
	select {
	case snapshot := <-finished:
		require.JSONEq(t, `{"snapshot-model":3}`, snapshot["ModelPrice"])
		require.JSONEq(t, `{"snapshot-model":4}`, snapshot["ModelRatio"])
		var meta map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(snapshot["CompletionRatioMeta"]), &meta))
		require.Contains(t, meta, "snapshot-model")
		snapshot["ModelPrice"] = "caller mutation"
		require.NotEqual(t, snapshot["ModelPrice"], GetOptionsSnapshot()["ModelPrice"])
	case <-time.After(5 * time.Second):
		t.Fatal("snapshot stayed blocked after publication completed")
	}
}

func TestPriceLockReceiptUsesTransactionPricingNotStaleRuntime(t *testing.T) {
	setupPriceLockTest(t)
	values := make(map[string]string, len(modelPriceOptionKeys))
	for _, key := range modelPriceOptionKeys {
		values[key] = "{}"
	}
	values["ModelPrice"] = `{"source":7}`
	require.NoError(t, UpdateOptionsBulk(values))
	_, err := UpdateModelPriceLock("other", true)
	require.NoError(t, err)
	common.OptionMapRWMutex.Lock()
	common.OptionMap["ModelPrice"] = `{"source":999}`
	common.OptionMap[ModelPriceLocksOptionKey] = "{}"
	common.OptionMap["SyntheticSecret"] = "not-in-pricing-receipt"
	common.OptionMapRWMutex.Unlock()

	result, err := UpdateModelPriceLock("source", true)
	require.NoError(t, err)
	require.Len(t, result.Pricing, len(modelPriceOptionKeys)+1)
	for key, value := range values {
		require.JSONEq(t, value, result.Pricing[key], key)
	}
	require.JSONEq(t, `{"source":true,"other":true}`, result.Pricing[ModelPriceLocksOptionKey])
	require.NotContains(t, result.Pricing, "SyntheticSecret")
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "not-in-pricing-receipt")

	unlocked, err := UpdateModelPriceLock("source", false)
	require.NoError(t, err)
	require.JSONEq(t, `{"other":true}`, unlocked.Pricing[ModelPriceLocksOptionKey])
	// Another committed write must not mutate the earlier receipt or its pricing.
	require.JSONEq(t, `{"source":true,"other":true}`, result.Pricing[ModelPriceLocksOptionKey])
	result.Pricing[ModelPriceLocksOptionKey] = "{}"
	require.True(t, IsModelPriceLocked("other"))
}

func TestPriceLockReceiptIsAbsentForOrdinaryWritesAndFailedTransactions(t *testing.T) {
	setupPriceLockTest(t)
	result, err := UpdateOptionWithWarnings("ModelPrice", `{"source":1}`)
	require.NoError(t, err)
	require.Nil(t, result.Pricing)

	const callback = "test:price-lock-snapshot-failure"
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if option, ok := tx.Statement.Dest.(*Option); ok &&
			option.Key == ModelPriceLocksOptionKey && option.Value != "{}" {
			// Fail the final write after the transaction snapshot was captured.
			tx.AddError(errors.New("injected persistence failure"))
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Create().Remove(callback) })
	result, err = UpdateModelPriceLock("source", true)
	require.Error(t, err)
	require.Nil(t, result.Pricing)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "pricing")
	require.False(t, IsModelPriceLocked("source"))
}
