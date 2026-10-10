package controller

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
)

// These relay helpers are shared by the developer-access automatic reviewer.
func mustReviewResponseContent(body []byte) json.RawMessage {
	response, err := parseAssistantResponse(body)
	if err != nil || len(response.Choices) == 0 {
		return nil
	}
	return response.Choices[0].Message.Content
}

func newAssistantReviewContext(ctx context.Context, root *model.User, routeGroup string) (*gin.Context, *httptest.ResponseRecorder, error) {
	routeGroup = strings.TrimSpace(routeGroup)
	if routeGroup == "" {
		routeGroup = setting.DefaultAssistantGroup
	}
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	// Keep synthetic review requests origin-less. Relay adaptors join this path
	// to the selected channel base URL; an absolute URL here would be treated as
	// path text and could produce https://provider.example/http://assistant-review/...
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", io.NopCloser(strings.NewReader("{}")))
	ginContext.Request = ginContext.Request.WithContext(ctx)
	ginContext.Set(common.RequestIdKey, common.NewRequestId())
	ginContext.Set("id", root.Id)
	ginContext.Set("username", root.Username)
	ginContext.Set("role", root.Role)
	ginContext.Set("group", routeGroup)
	root.ToBaseUser().WriteContext(ginContext)
	token := &model.Token{UserId: root.Id, Name: "assistant-review", Group: routeGroup, UnlimitedQuota: true}
	if err := middleware.SetupContextForToken(ginContext, token); err != nil {
		return nil, nil, err
	}
	common.SetContextKey(ginContext, constant.ContextKeyUsingGroup, routeGroup)
	common.SetContextKey(ginContext, constant.ContextKeyRequestStartTime, time.Now())
	return ginContext, recorder, nil
}
