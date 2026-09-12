package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPriceLockControllerPreservesLockedRuntimePrices(t *testing.T) {
	db := openTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Log{}, &model.RatioNotification{}, &model.RatioDelivery{}))
	previousRefresh := refreshPricingCache
	previousImage := ratio_setting.ImageRatio2JSONString()
	previousRatio := ratio_setting.ModelRatio2JSONString()
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	refreshPricingCache = func() error { return nil }
	t.Cleanup(func() {
		refreshPricingCache = previousRefresh
		_ = ratio_setting.UpdateImageRatioByJSONString(previousImage)
		_ = ratio_setting.UpdateModelRatioByJSONString(previousRatio)
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
	require.NoError(t, model.UpdateOption("ImageRatio", `{"locked-controller-model":2,"unlocked-controller-model":3}`))
	require.NoError(t, model.UpdateOption("ModelRatio", `{"locked-controller-model":7}`))
	require.NoError(t, model.UpdateOption(model.ModelPriceLocksOptionKey, `{"locked-controller-model":true}`))

	for _, test := range []struct {
		name    string
		handler gin.HandlerFunc
		body    string
	}{
		{
			name:    "single image update",
			handler: UpdateOption,
			body:    `{"key":"ImageRatio","value":"{\"locked-controller-model\":9,\"unlocked-controller-model\":4}"}`,
		},
		{
			name:    "bulk image update",
			handler: UpdateOptionsBulk,
			body:    `{"values":{"ImageRatio":"{\"locked-controller-model\":9,\"unlocked-controller-model\":4}"}}`,
		},
		{
			name:    "validate locked image update",
			handler: ValidateOptions,
			body:    `{"values":{"ImageRatio":"{\"locked-controller-model\":9,\"unlocked-controller-model\":4}"}}`,
		},
		{
			name:    "reset model ratio",
			handler: ResetModelRatio,
			body:    `{}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var before, after int64
			require.NoError(t, db.Model(&model.RatioNotification{}).Count(&before).Error)
			response := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(response)
			context.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(test.body))
			test.handler(context)
			require.Equal(t, http.StatusOK, response.Code)
			var payload struct {
				Success      bool     `json:"success"`
				Warnings     []string `json:"warnings"`
				LockedModels []string `json:"locked_models"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
			require.True(t, payload.Success, response.Body.String())
			require.NoError(t, db.Model(&model.RatioNotification{}).Count(&after).Error)
			if test.name == "single image update" || test.name == "reset model ratio" {
				require.Equal(t, before+1, after)
			} else {
				require.Equal(t, before, after)
			}
			require.NotEmpty(t, payload.Warnings)
			require.Equal(t, []string{"locked-controller-model"}, payload.LockedModels)
			image, _ := ratio_setting.GetImageRatio("locked-controller-model")
			require.Equal(t, float64(2), image)
			ratio, _, _ := ratio_setting.GetModelRatio("locked-controller-model")
			require.Equal(t, float64(7), ratio)
			image, _ = ratio_setting.GetImageRatio("unlocked-controller-model")
			require.Equal(t, float64(4), image)
		})
	}
}

func TestUpdateModelPriceLockRequiresBoolean(t *testing.T) {
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(
		`{"key":"ModelPriceLock","model":"test-model","value":"true"}`,
	))
	UpdateOption(context)
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Contains(t, response.Body.String(), "value must be a boolean")
}
