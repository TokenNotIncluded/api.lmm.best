package controller

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/i18n"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRootSubscriptionResetRequestDecodesSnakeCaseFilters(t *testing.T) {
	var request rootSubscriptionResetPreviewRequest
	err := json.Unmarshal([]byte(`{
		"mode":"hard",
		"all_matching":true,
		"filter":{"query":"alice","plan_id":7,"plan_ids":[7,9],"user_ids":[11,13]}
	}`), &request)
	require.NoError(t, err)
	require.True(t, request.AllMatching)
	require.NotNil(t, request.Filter)
	require.Equal(t, "alice", request.Filter.Query)
	require.Equal(t, 7, request.Filter.PlanId)
	require.Equal(t, []int{7, 9}, request.Filter.PlanIds)
	require.Equal(t, []int{11, 13}, request.Filter.UserIds)
}

func TestRootSubscriptionResetPreviewRequiresExplicitSoftExpiryAndRejectsHardField(t *testing.T) {
	settings := operation_setting.GetPaymentSetting()
	previous := *settings
	t.Cleanup(func() { *settings = previous })
	settings.ComplianceConfirmed = true
	settings.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	for _, body := range []string{
		`{"mode":"soft"}`, `{"mode":"soft","voucher_expires_at":null}`, `{"mode":"soft","voucher_expires_at":0}`,
		`{"mode":"soft","voucher_expires_at":253402300800}`, `{"mode":"soft","voucher_expires_at":1.5}`, `{"mode":"soft","voucher_expires_at":"2000000000"}`,
		`{"mode":"hard","voucher_expires_at":0}`, `{"mode":"hard","voucher_expires_at":null}`, `{"mode":"hard","voucher_expires_at":2000000000}`,
	} {
		response := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(response)
		c.Set("id", 1)
		c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
		RootPreviewSubscriptionsBatch(c)
		var payload struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
		require.False(t, payload.Success, body)
		require.Contains(t, payload.Message, "voucher_expires_at", body)
	}
	var req rootSubscriptionResetPreviewRequest
	require.NoError(t, json.Unmarshal([]byte(`{"mode":"soft","voucher_expires_at":253402300799}`), &req))
	var expiry int64
	require.NoError(t, json.Unmarshal(req.VoucherExpiresAt, &expiry))
	require.Equal(t, model.MaxSubscriptionResetVoucherExpiresAt, expiry)
	for _, expiry := range []string{"null", "0", "2000000000"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{"preview_token":"p","operation_id":"o","voucher_expires_at":`+expiry+`}`))
		var execute rootSubscriptionResetExecuteRequest
		require.Error(t, decodeStrictJSONRequest(c, &execute))
	}
}

func TestRootSubscriptionResetRequestRejectsUnknownOrTrailingJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	decode := func(body string) error {
		context, _ := gin.CreateTestContext(httptest.NewRecorder())
		context.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
		var request rootSubscriptionResetPreviewRequest
		return decodeStrictJSONRequest(context, &request)
	}

	require.Error(t, decode(`{"mode":"hard","all_matching":true,"filter":{"plan_ids_typo":[1]}}`))
	require.Error(t, decode(`{"mode":"hard"} {"mode":"soft"}`))
	require.NoError(t, decode(`{"mode":"hard","all_matching":true,"filter":{"plan_ids":[1]}}`))

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{"preview_token":"p","operation_id":"o","mode":"hard"}`))
	var executeRequest rootSubscriptionResetExecuteRequest
	require.Error(t, decodeStrictJSONRequest(context, &executeRequest))
}

func TestRootSubscriptionResetWritesRequireComplianceBeforeDecoding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	paymentSetting := operation_setting.GetPaymentSetting()
	original := *paymentSetting
	t.Cleanup(func() { *paymentSetting = original })
	paymentSetting.ComplianceConfirmed = false
	paymentSetting.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion

	for _, test := range []struct {
		name    string
		handler func(*gin.Context)
	}{
		{name: "preview", handler: RootPreviewSubscriptionsBatch},
		{name: "execute", handler: RootResetSubscriptionsBatch},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest("POST", "/", strings.NewReader("{"))
			expectedMessage := common.TranslateMessage(context, i18n.MsgPaymentComplianceRequired)

			test.handler(context)

			var response struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			require.False(t, response.Success)
			require.Equal(t, expectedMessage, response.Message)
		})
	}
}

func TestParseSubscriptionResetIdsDeduplicatesAndRejectsInvalidOrOversizedInput(t *testing.T) {
	values, err := parseSubscriptionResetIds("1,2,2,3")
	require.NoError(t, err)
	require.Equal(t, []int{1, 2, 3}, values)

	_, err = parseSubscriptionResetIds("1,invalid")
	require.ErrorContains(t, err, "invalid")

	parts := make([]string, 101)
	for index := range parts {
		parts[index] = strconv.Itoa(index + 1)
	}
	_, err = parseSubscriptionResetIds(strings.Join(parts, ","))
	require.ErrorContains(t, err, "too many")
}
