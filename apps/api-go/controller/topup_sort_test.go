package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/LIghtJUNction/api.lmm.best/model"
)

func setupTopUpSortControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.TopUp{}))
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			require.NoError(t, sqlDB.Close())
		}
	})
	return db
}

func topUpSortRequest(t *testing.T, userID int, query string) []int {
	t.Helper()
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set("id", userID)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/user/topup/self?"+query, nil)
	GetUserTopUps(context)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Items []struct {
				ID int `json:"id"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success, recorder.Body.String())
	ids := make([]int, 0, len(response.Data.Items))
	for _, item := range response.Data.Items {
		ids = append(ids, item.ID)
	}
	return ids
}

func TestGetUserTopUpsSortsGloballyBeforePagination(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupTopUpSortControllerTestDB(t)
	now := time.Now().Unix()
	require.NoError(t, db.Create(&[]model.TopUp{
		{Id: 1, UserId: 7, TradeNo: "small", Amount: 10, CreateTime: now},
		{Id: 2, UserId: 7, TradeNo: "large", Amount: 30, CreateTime: now},
		{Id: 3, UserId: 7, TradeNo: "middle", Amount: 20, CreateTime: now},
	}).Error)

	require.Equal(t, []int{1, 3}, topUpSortRequest(t, 7, "p=1&page_size=2&sort_by=amount&sort_order=asc"))
}
