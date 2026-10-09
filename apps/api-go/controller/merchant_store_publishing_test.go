package controller

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMerchantStorePublishingSeparatesSellerConfigurationFromBuyerAcceptance(t *testing.T) {
	for _, item := range []struct {
		err  error
		code string
	}{
		{model.ErrMerchantStoreSellerTermsNotConfigured, "STORE_SELLER_TERMS_NOT_CONFIGURED"},
		{model.ErrMerchantStoreSellerTerms, "STORE_SELLER_TERMS_REQUIRED"},
	} {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		merchantStoreRespond(context, nil, item.err)
		require.Equal(t, 409, recorder.Code)
		var body map[string]any
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
		require.Equal(t, item.code, body["code"])
	}
}
