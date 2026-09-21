package controller

import (
	"bytes"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAcquisitionRejectsUnconsentedAndTestVisitsWithoutCookies(t *testing.T) {
	for _, payload := range []string{`{"consent":false}`, `{"consent":true,"test":true}`, `{"consent":true,"nonce":"bad"}`} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/acquisition/visit", bytes.NewBufferString(payload))
		c.Request.Header.Set("Content-Type", "application/json")
		ObserveAcquisition(c)
		require.Equal(t, http.StatusNoContent, c.Writer.Status())
		require.Empty(t, w.Header().Get("Set-Cookie"))
	}
}
func TestAcquisitionConsentRevocationRemovesLinkedSourceOnly(t *testing.T) {
	db := setupUserOnboardingTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AcquisitionVisitor{}, &model.AcquisitionVisit{}, &model.AcquisitionAccount{}, &model.AcquisitionActivity{}, &model.AcquisitionConsent{}, &model.AcquisitionCorrection{}, &model.AcquisitionCorrectionHead{}, &model.AcquisitionFirstPayment{}))
	raw := "0123456789abcdef0123456789abcdef"
	hash := model.AcquisitionVisitorHash(raw)
	require.NoError(t, db.Create(&model.AcquisitionVisitor{ID: hash, UserID: 9}).Error)
	require.NoError(t, db.Create(&model.AcquisitionAccount{UserID: 9, RegistrationSource: "github"}).Error)
	require.NoError(t, db.Create(&model.AcquisitionAccount{UserID: 10, RegistrationSource: "forum"}).Error)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/acquisition/consent", nil)
	c.Request.AddCookie(&http.Cookie{Name: acquisitionCookie, Value: raw})
	RevokeAcquisition(c)
	var count int64
	require.NoError(t, db.Model(&model.AcquisitionAccount{}).Where("user_id = 9").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Model(&model.AcquisitionAccount{}).Where("user_id = 10").Count(&count).Error)
	require.Equal(t, int64(1), count)
}
