package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func setupPricingLockControllerTest(t *testing.T) {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	db := openTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Log{}))
	previousRefresh := refreshPricingCache
	refreshPricingCache = func() error { return nil }
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		refreshPricingCache = previousRefresh
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
}

func invokePricingLockHandler(t *testing.T, handler gin.HandlerFunc, body any) []string {
	t.Helper()
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/option/", bytes.NewReader(encoded))
	handler(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success  bool     `json:"success"`
		Message  string   `json:"message"`
		Warnings []string `json:"warnings"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success, response.Message)
	return response.Warnings
}

func TestUpdateOptionLockedPricingWarnsWithoutRuntimeMutation(t *testing.T) {
	for _, field := range []struct {
		key  string
		read func() string
		set  func(string) error
	}{
		{"ModelPrice", ratio_setting.ModelPrice2JSONString, ratio_setting.UpdateModelPriceByJSONString},
		{"ImageRatio", ratio_setting.ImageRatio2JSONString, ratio_setting.UpdateImageRatioByJSONString},
		{"AudioRatio", ratio_setting.AudioRatio2JSONString, ratio_setting.UpdateAudioRatioByJSONString},
		{"AudioCompletionRatio", ratio_setting.AudioCompletionRatio2JSONString, ratio_setting.UpdateAudioCompletionRatioByJSONString},
		{"CreateCacheRatio", ratio_setting.CreateCacheRatio2JSONString, ratio_setting.UpdateCreateCacheRatioByJSONString},
	} {
		t.Run(field.key, func(t *testing.T) {
			setupPricingLockControllerTest(t)
			previous := field.read()
			t.Cleanup(func() { require.NoError(t, field.set(previous)) })
			require.NoError(t, model.UpdateOption(field.key, `{"locked-model":2,"open-model":3}`))
			require.NoError(t, model.UpdateOption("ModelPriceLock", `{"locked-model":true}`))
			warnings := invokePricingLockHandler(t, UpdateOption, OptionUpdateRequest{
				Key: field.key, Value: `{"locked-model":"invalid-but-ignored","open-model":4}`,
			})
			require.NotEmpty(t, warnings)
			want := `{"locked-model":2,"open-model":4}`
			require.JSONEq(t, want, field.read())
			var saved model.Option
			require.NoError(t, model.DB.Where("key = ?", field.key).First(&saved).Error)
			require.JSONEq(t, want, saved.Value)
		})
	}
}

func TestBulkAndValidationRespectPricingLock(t *testing.T) {
	setupPricingLockControllerTest(t)
	previous := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previous)) })
	require.NoError(t, model.UpdateOption("ModelPrice", `{"locked-model":2,"open-model":3}`))
	require.NoError(t, model.UpdateOption("ModelPriceLock", `{"locked-model":true}`))
	body := OptionValuesRequest{Values: map[string]string{
		"ModelPrice": `{"open-model":4}`,
	}}
	require.NotEmpty(t, invokePricingLockHandler(t, ValidateOptions, body))
	require.JSONEq(t, `{"locked-model":2,"open-model":3}`, ratio_setting.ModelPrice2JSONString())
	require.NotEmpty(t, invokePricingLockHandler(t, UpdateOptionsBulk, body))
	require.JSONEq(t, `{"locked-model":2,"open-model":4}`, ratio_setting.ModelPrice2JSONString())
}

func TestResetModelRatioPreservesLockedRuntimePrice(t *testing.T) {
	setupPricingLockControllerTest(t)
	previous := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previous)) })
	require.NoError(t, model.UpdateOption("ModelRatio", `{"locked-custom-model":17,"open-custom-model":3}`))
	require.NoError(t, model.UpdateOption("ModelPriceLock", `{"locked-custom-model":true}`))
	require.NotEmpty(t, invokePricingLockHandler(t, ResetModelRatio, nil))
	rates := ratio_setting.GetModelRatioCopy()
	require.Equal(t, float64(17), rates["locked-custom-model"])
	require.NotContains(t, rates, "open-custom-model")
	var saved model.Option
	require.NoError(t, model.DB.Where("key = ?", "ModelRatio").First(&saved).Error)
	require.JSONEq(t, ratio_setting.ModelRatio2JSONString(), saved.Value)
}
