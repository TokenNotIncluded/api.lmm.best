package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRetiredDeveloperAccessEndpointsPreserveHistoryAndNeverActivate(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.DeveloperAccessRequest{}, &model.DeveloperAccessRecommendationArchive{}, &model.AuthFlow{}))
	user := model.User{Username: "retired-l1", AffCode: "retired-l1", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	stored := model.DeveloperAccessRequest{UserId: user.Id, Status: "pending", Reason: "private legacy reason", AIRecommendation: "private legacy letter"}
	require.NoError(t, db.Create(&stored).Error)
	archive := model.DeveloperAccessRecommendationArchive{UserId: user.Id, RequestId: stored.Id, Recommendation: "private archived letter"}
	require.NoError(t, db.Create(&archive).Error)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, body := range []string{"", `{"confirmed":true,"reason":"create access","ai_recommendation":"old recommendation","confirmation_token":"old-token"}`, "invalid-json"} {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(method, "/retired", strings.NewReader(body))
			c.Set("id", user.Id)
			RetiredDeveloperAccessRequest(c)
			require.Equal(t, http.StatusGone, w.Code)
			require.Contains(t, w.Body.String(), "DEVELOPER_ACCESS_LETTER_RETIRED")
			require.NotContains(t, w.Body.String(), "private")
		}
	}
	var actual model.User
	require.NoError(t, db.First(&actual, user.Id).Error)
	require.Zero(t, actual.ConsoleActivatedAt)
	var request model.DeveloperAccessRequest
	require.NoError(t, db.First(&request, stored.Id).Error)
	require.Equal(t, stored, request)
	var actualArchive model.DeveloperAccessRecommendationArchive
	require.NoError(t, db.First(&actualArchive, archive.Id).Error)
	require.Equal(t, archive, actualArchive)
	var flows int64
	require.NoError(t, db.Model(&model.AuthFlow{}).Count(&flows).Error)
	require.Zero(t, flows)
}
