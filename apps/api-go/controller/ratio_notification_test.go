package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRatioNotificationFeedFiltersPrivateChanges(t *testing.T) {
	db := openTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Ability{}, &model.Channel{}, &model.RatioNotification{}))
	u := model.User{Username: "feed", Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(&u).Error)
	e := model.RatioNotification{ID: "feed-event", EffectiveAt: 123, Changes: `[{"option":"GroupRatio","group":"default","old":1,"new":2},{"option":"GroupRatio","group":"secret-group","old":4,"new":5}]`}
	require.NoError(t, db.Create(&e).Error)
	router := gin.New()
	router.GET("/", func(c *gin.Context) { c.Set("id", u.Id); ListRatioNotifications(c) })
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "feed-event")
	require.NotContains(t, w.Body.String(), "secret-group")
	require.NoError(t, db.Model(&u).Update("group", "other").Error)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	require.NotContains(t, w.Body.String(), "feed-event")
}

func TestRatioDeliveryRetryOnlyFailed(t *testing.T) {
	db := openTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.RatioDelivery{}, &model.Log{}))
	d := model.RatioDelivery{EventID: "retry", UserID: 1, Status: "failed", Attempts: 5}
	require.NoError(t, db.Create(&d).Error)
	router := gin.New()
	router.POST("/:id", RetryRatioDelivery)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/1", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, db.First(&d, d.ID).Error)
	require.Equal(t, 0, d.Attempts)
	require.Equal(t, "pending", d.Status)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/1", nil))
	require.Equal(t, http.StatusConflict, w.Code)
}
