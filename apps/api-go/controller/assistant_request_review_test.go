package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAssistantReviewContextUsesRelativeRelayPath(t *testing.T) {
	root := &model.User{Id: 7, Username: "review-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	ctx, _, err := newAssistantReviewContext(context.Background(), root, "review")
	require.NoError(t, err)
	require.NotNil(t, ctx.Request.URL)
	require.Empty(t, ctx.Request.URL.Scheme)
	require.Empty(t, ctx.Request.URL.Host)
	require.Equal(t, "/v1/chat/completions", ctx.Request.URL.Path)
}

func TestAdminAssistantRequestReviewsRespectRoleScope(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	actor := &model.User{Username: "review-admin", AffCode: "review-admin-aff", Password: "password", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
	peer := &model.User{Username: "peer-admin", AffCode: "peer-admin-aff", Password: "password", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(actor).Error)
	require.NoError(t, db.Create(peer).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", actor.Id)
	c.Set("role", actor.Role)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/assistant/admin/request-reviews?user_id="+strconv.Itoa(peer.Id), nil)
	AdminListAssistantRequestReviews(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"success":false`)
}
