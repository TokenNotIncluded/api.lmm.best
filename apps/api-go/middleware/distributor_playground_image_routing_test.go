/*
Copyright (C) 2026 LIghtJUNction
*/

package middleware

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/i18n"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDistributorRoutesPlaygroundImagesToAdvancedCustomChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	require.NoError(t, i18n.Init())
	previousDB := model.DB
	previousSQLitePath := common.SQLitePath
	previousRedisEnabled := common.RedisEnabled
	previousMemoryCacheEnabled := common.MemoryCacheEnabled
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	common.SQLitePath = filepath.Join(t.TempDir(), "drawing.db")
	t.Setenv("SQL_DSN", "")
	require.NoError(t, model.InitDB())
	db := model.DB
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
		model.DB = previousDB
		common.SQLitePath = previousSQLitePath
		common.RedisEnabled = previousRedisEnabled
		common.MemoryCacheEnabled = previousMemoryCacheEnabled
	})

	channel := &model.Channel{
		Type: constant.ChannelTypeAdvancedCustom, Status: common.ChannelStatusEnabled,
		Key: "test-key", Name: "drawing", Models: "image-2", Group: "image-2",
		OtherSettings: `{"advanced_custom":{"advanced_routes":[` +
			`{"incoming_path":"/v1/images/generations","upstream_path":"/images/generations","models":["image-2"]},` +
			`{"incoming_path":"/v1/images/edits","upstream_path":"/images/edits","models":["image-2"]}]}}`,
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(nil))

	for _, operation := range []string{"generations", "edits"} {
		t.Run(operation, func(t *testing.T) {
			path := "/pg/images/" + operation
			request := httptest.NewRequest(http.MethodPost, path+"?group=image-2", strings.NewReader(`{"model":"image-2","prompt":"test"}`))
			request.Header.Set("Content-Type", "application/json")
			if operation == "edits" {
				var body bytes.Buffer
				writer := multipart.NewWriter(&body)
				require.NoError(t, writer.WriteField("model", "image-2"))
				require.NoError(t, writer.WriteField("prompt", "test"))
				part, err := writer.CreateFormFile("image", "input.png")
				require.NoError(t, err)
				_, err = part.Write([]byte("\x89PNG\r\n\x1a\n"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				request = httptest.NewRequest(http.MethodPost, path+"?group=image-2", &body)
				request.Header.Set("Content-Type", writer.FormDataContentType())
			}

			router := gin.New()
			router.POST(path, func(c *gin.Context) {
				common.SetContextKey(c, constant.ContextKeyUsingGroup, "image-2")
				c.Next()
			}, Distribute(), func(c *gin.Context) {
				require.Equal(t, channel.Id, common.GetContextKeyInt(c, constant.ContextKeyChannelId))
				require.Equal(t, "image-2", common.GetContextKeyString(c, constant.ContextKeyUsingGroup))
				c.Status(http.StatusNoContent)
			})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
		})
	}
}
