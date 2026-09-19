package controller

import (
	"errors"
	"fmt"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHeroSMSWrappedBalanceErrorRetainsBusinessStatus(t *testing.T) {
	for _, wrap := range []func(error) error{func(e error) error { return e }, func(e error) error { return fmt.Errorf("reserve: %w", e) }, func(e error) error { return errors.Join(errors.New("context"), e) }} {
		response := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(response)
		heroSMSError(c, wrap(model.NewHeroSMSError(http.StatusPaymentRequired, "TEMPORARY_SMS_MINIMUM_BALANCE", "Temporary SMS purchases require a balance of at least USD 10")))
		require.Equal(t, 402, response.Code)
		require.Contains(t, response.Body.String(), "TEMPORARY_SMS_MINIMUM_BALANCE")
		require.NotContains(t, response.Body.String(), "INTERNAL_ERROR")
	}
}
func TestHeroSMSUnknownFailureRemainsServerError(t *testing.T) {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	heroSMSError(c, errors.New("private database detail"))
	require.Equal(t, 500, response.Code)
	require.NotContains(t, response.Body.String(), "private database detail")
}
