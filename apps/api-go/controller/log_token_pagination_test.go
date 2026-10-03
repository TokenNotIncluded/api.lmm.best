package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type tokenLogPaginationRow struct {
	ID          int `gorm:"primaryKey;autoIncrement:false"`
	TokenID     int
	CreatedAt   int64
	RequestID   string
	Content     string
	ChannelName string
	Other       string
}

func useTokenLogPaginationDB(t *testing.T, db *gorm.DB, dialect common.DatabaseType) {
	t.Helper()
	previousDB, previousType, previousMode := model.LOG_DB, common.LogDatabaseType(), gin.Mode()
	model.LOG_DB = db
	common.SetLogDatabaseType(dialect)
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() {
		model.LOG_DB = previousDB
		common.SetLogDatabaseType(previousType)
		gin.SetMode(previousMode)
	})
	require.NoError(t, db.Table("logs").AutoMigrate(&tokenLogPaginationRow{}))
	rows := make([]tokenLogPaginationRow, 1006)
	for i := range rows {
		id := i + 1
		if dialect == common.DatabaseTypeClickHouse {
			// The tested ordering must follow time/request id rather than id.
			id = len(rows) - i
		}
		rows[i] = tokenLogPaginationRow{ID: id, TokenID: 7, CreatedAt: int64(i/2 + 1), RequestID: fmt.Sprintf("request-%04d", i), Content: strconv.Itoa(i + 1), ChannelName: "private-channel", Other: `{"admin_info":{"secret":"hidden"},"audit_info":{"route":"hidden"},"safe":"kept"}`}
	}
	rows[1005].TokenID = 8
	require.NoError(t, db.Table("logs").CreateInBatches(&rows, 100).Error)
}

type tokenLogPaginationResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func requestTokenLogPagination(t *testing.T, tokenID int, query string) tokenLogPaginationResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("GET", "/api/log/token"+query, nil)
	if tokenID != 0 {
		c.Set("token_id", tokenID)
	}
	GetLogByKey(c)
	var response tokenLogPaginationResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

func assertTokenLogPaginationContract(t *testing.T) {
	t.Helper()
	for _, test := range []struct {
		name, query                     string
		token, page, size, count, first int
		legacy, failure                 bool
	}{
		{name: "legacy", token: 7, count: 1000, first: 1005, legacy: true},
		{name: "legacy ignores filters", query: "?token_id=8&type=2&start_timestamp=9999999999", token: 7, count: 1000, first: 1005, legacy: true},
		{name: "first page token scoped", query: "?p=1&page_size=2&token_id=8&end_timestamp=1", token: 7, page: 1, size: 2, count: 2, first: 1005},
		{name: "second page", query: "?p=2&page_size=2", token: 7, page: 2, size: 2, count: 2, first: 1003},
		{name: "deep history", query: "?p=101&page_size=10", token: 7, page: 101, size: 10, count: 5, first: 5},
		{name: "empty page", query: "?p=102&page_size=10", token: 7, page: 102, size: 10},
		{name: "p opt in", query: "?p=1", token: 7, page: 1, size: 10, count: 10, first: 1005},
		{name: "page size opt in", query: "?page_size=2", token: 7, page: 1, size: 2, count: 2, first: 1005},
		{name: "ps opt in", query: "?ps=2", token: 7, page: 1, size: 2, count: 2, first: 1005},
		{name: "size opt in", query: "?size=2", token: 7, page: 1, size: 2, count: 2, first: 1005},
		{name: "full page", query: "?page_size=1000", token: 7, page: 1, size: 1000, count: 1000, first: 1005},
		{name: "cap", query: "?page_size=1001", token: 7, page: 1, size: 1000, count: 1000, first: 1005},
		{name: "negative", query: "?p=-2&page_size=-1&ps=-1&size=-1", token: 7, page: 1, size: 10, count: 10, first: 1005},
		{name: "zero", query: "?p=0&page_size=0", token: 7, page: 1, size: 10, count: 10, first: 1005},
		{name: "invalid", query: "?p=bad&page_size=bad", token: 7, page: 1, size: 10, count: 10, first: 1005},
		{name: "alias precedence", query: "?page_size=bad&ps=2&size=3", token: 7, page: 1, size: 2, count: 2, first: 1005},
		{name: "empty p opt in", query: "?p", token: 7, page: 1, size: 10, count: 10, first: 1005},
		{name: "empty ps opt in", query: "?ps=", token: 7, page: 1, size: 10, count: 10, first: 1005},
		{name: "duplicate first value", query: "?p=2&p=1&size=3&size=9", token: 7, page: 2, size: 3, count: 3, first: 1002},
		{name: "invalid first duplicate", query: "?p=bad&p=2&size=2", token: 7, page: 1, size: 2, count: 2, first: 1005},
		{name: "largest valid page", query: "?p=" + strconv.Itoa(int(^uint(0)>>1)) + "&size=1", token: 7, page: int(^uint(0) >> 1), size: 1},
		{name: "parse overflow", query: "?p=92233720368547758080&page_size=92233720368547758080", token: 7, page: 1, size: 10, count: 10, first: 1005},
		{name: "ps parse overflow", query: "?ps=92233720368547758080", token: 7, page: 1, size: 10, count: 10, first: 1005},
		{name: "size parse overflow", query: "?size=92233720368547758080", token: 7, page: 1, size: 10, count: 10, first: 1005},
		{name: "offset overflow", query: "?p=" + strconv.Itoa(int(^uint(0)>>1)) + "&size=2", token: 7, failure: true},
		{name: "end overflow", query: "?p=" + strconv.Itoa(int(^uint(0)>>1)/2+1) + "&size=2", token: 7, failure: true},
		{name: "other token", query: "?p=1&token_id=7", token: 8, page: 1, size: 10, count: 1, first: 1006},
		{name: "unknown token", query: "?p=1&token_id=7", token: 9, page: 1, size: 10},
		{name: "missing token legacy", legacy: true, failure: true},
		{name: "missing token page", query: "?p=1", failure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := requestTokenLogPagination(t, test.token, test.query)
			require.Equal(t, !test.failure, response.Success)
			if test.failure {
				require.Empty(t, response.Data)
				return
			}
			var logs []model.Log
			if test.legacy {
				require.NoError(t, json.Unmarshal(response.Data, &logs))
			} else {
				var page struct {
					Page     int         `json:"page"`
					PageSize int         `json:"page_size"`
					Total    int         `json:"total"`
					Items    []model.Log `json:"items"`
				}
				require.NoError(t, json.Unmarshal(response.Data, &page))
				require.Equal(t, test.page, page.Page)
				require.Equal(t, test.size, page.PageSize)
				total := 1005
				if test.token == 8 {
					total = 1
				}
				if test.token == 9 {
					total = 0
				}
				require.Equal(t, total, page.Total)
				require.NotNil(t, page.Items)
				logs = page.Items
			}
			require.Len(t, logs, test.count)
			for i, log := range logs {
				require.Equal(t, test.token, log.TokenId)
				require.Equal(t, strconv.Itoa(test.first-i), log.Content)
				require.Equal(t, max(0, test.page-1)*test.size+i+1, log.Id)
				require.Empty(t, log.ChannelName)
				require.JSONEq(t, `{"safe":"kept"}`, log.Other)
			}
		})
	}
}

func TestTokenLogOptionalPagination(t *testing.T) {
	for _, dialect := range []common.DatabaseType{common.DatabaseTypeSQLite, common.DatabaseTypeClickHouse} {
		t.Run(string(dialect), func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "logs.db")), &gorm.Config{})
			require.NoError(t, err)
			pool, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, pool.Close()) })
			useTokenLogPaginationDB(t, db, dialect)
			// ClickHouse's production ORDER BY is exercised against a SQL engine;
			// this does not claim a real ClickHouse server qualification.
			assertTokenLogPaginationContract(t)
		})
	}
}

func TestTokenLogOptionalPaginationPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" || os.Getenv("TEST_POSTGRES_ISOLATED_SCHEMA") != "1" {
		t.Skip("set TEST_POSTGRES_DSN and TEST_POSTGRES_ISOLATED_SCHEMA=1 for an isolated PostgreSQL schema")
	}
	base, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	basePool, err := base.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, basePool.Close()) })
	schema := fmt.Sprintf("token_logs_%d_%d", os.Getpid(), time.Now().UnixNano())
	require.NoError(t, createAssistantKeyPostgresSchema(base, schema))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		require.NoError(t, dropAssistantKeyPostgresSchema(base.WithContext(ctx), schema))
	})
	db, err := gorm.Open(postgres.Open(assistantKeyPostgresDSN(dsn, schema)), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	useTokenLogPaginationDB(t, db, common.DatabaseTypePostgreSQL)
	assertTokenLogPaginationContract(t)
}

func TestTokenLogPaginationDatabaseFailuresAndLegacyAvoidsCount(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "logs.db")), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	useTokenLogPaginationDB(t, db, common.DatabaseTypeSQLite)
	for _, failCount := range []bool{true, false} {
		t.Run(fmt.Sprintf("count_failure_%t", failCount), func(t *testing.T) {
			queries := 0
			require.NoError(t, db.Callback().Query().After("gorm:query").Register("token-log-query-failure", func(tx *gorm.DB) {
				queries++
				isCount := strings.Contains(strings.ToLower(tx.Statement.SQL.String()), "count(*)")
				if isCount == failCount {
					tx.AddError(errors.New("injected token log query failure"))
				}
			}))
			t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove("token-log-query-failure")) })
			response := requestTokenLogPagination(t, 7, "?p=1")
			require.False(t, response.Success)
			require.Empty(t, response.Data)
			if failCount {
				require.Equal(t, 1, queries, "failed count must prevent the row query")
				legacy := requestTokenLogPagination(t, 7, "")
				require.True(t, legacy.Success, "legacy reads must not count")
			} else {
				require.Equal(t, 2, queries)
			}
		})
	}
}
