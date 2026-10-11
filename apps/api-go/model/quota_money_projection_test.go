package model

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func quotaMoneyDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Log{}, &QuotaData{}))
	previous, previousLog := DB, LOG_DB
	DB, LOG_DB = db, db
	t.Cleanup(func() { DB, LOG_DB = previous, previousLog })
	return db
}

func TestQuotaMoneyProjectionUsesCapturedAnchorAndPreservesRaw(t *testing.T) {
	for _, tc := range []struct {
		quota int
		basis string
		usd   string
	}{
		{500000, "500000", "1"},
		{3359744, "3359744", "1"},
		{1, "500000.0", "0.000002"},
		{500000, "5e5", "1"},
		{1, "3", "0.333333333333333333"},
		{0, "500000", "0"},
	} {
		t.Run(tc.basis+tc.usd, func(t *testing.T) {
			log := Log{Type: LogTypeConsume, Quota: tc.quota, QuotaStoredValid: true, Other: `{"pricing_credits_per_usd":` + tc.basis + `,"admin_info":{"secret":"redacted"}}`}
			formatUserLogs([]*Log{&log}, 0)
			require.True(t, log.QuotaMoneyAvailable)
			require.Equal(t, tc.usd, *log.QuotaUSD)
			require.Equal(t, tc.quota, log.Quota)
			require.NotContains(t, log.Other, "secret")
			raw, err := json.Marshal(log)
			require.NoError(t, err)
			require.Contains(t, string(raw), `"quota_usd":"`+tc.usd+`"`)
		})
	}
}

func TestQuotaMoneyProjectionRefusesUnprovenOrMalformedMoney(t *testing.T) {
	for index, other := range []string{
		"", "null", "[]", "not-json", `{"pricing_credits_per_usd":500000`,
		`{}`, `{"pricing_unit_credits_per_unit":500000}`, `{"pricing_credits_per_usd":"500000"}`,
		`{"pricing_credits_per_usd":true}`, `{"pricing_credits_per_usd":null}`,
		`{"pricing_credits_per_usd":0}`, `{"pricing_credits_per_usd":-500000}`,
		`{"pricing_credits_per_usd":500000.00000000001}`,
		`{"pricing_credits_per_usd":9007199254740992}`, `{"pricing_credits_per_usd":1e99999999}`,
		`{"pricing_credits_per_usd":500000,"padding":"` + strings.Repeat("x", quotaMoneyMaxMetadataBytes) + `"}`,
	} {
		t.Run(fmt.Sprintf("invalid-%d", index), func(t *testing.T) {
			result := ProjectLogQuotaMoney(500000, LogTypeConsume, other)
			require.False(t, result.QuotaMoneyAvailable)
			require.Nil(t, result.QuotaUSD)
			raw, err := json.Marshal(result)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "quota_usd")
		})
	}
	for _, logType := range []int{LogTypeTopup, LogTypeRefund, LogTypeManage, LogTypeSystem} {
		projection := ProjectLogQuotaMoney(500000, logType, `{"pricing_credits_per_usd":500000}`)
		require.False(t, projection.QuotaMoneyAvailable)
		require.Equal(t, "not_consumption", projection.QuotaMoneyBasis)
	}
	require.False(t, ProjectLogQuotaMoney(-1, LogTypeConsume, `{"pricing_credits_per_usd":500000}`).QuotaMoneyAvailable)
}

func TestQuotaMoneyNullHistoryRemainsUnknownAcrossReadEndpoints(t *testing.T) {
	db := quotaMoneyDB(t)
	require.NoError(t, db.Create(&Log{UserId: 7, TokenId: 7, Type: LogTypeConsume, Quota: 500000, Content: "valid", Other: `{"pricing_credits_per_usd":500000}`}).Error)
	require.NoError(t, db.Exec("INSERT INTO logs(user_id, token_id, type, quota, content, other) VALUES (7, 7, ?, NULL, 'null', ?)", LogTypeConsume, `{"pricing_credits_per_usd":500000}`).Error)
	check := func(logs []*Log, err error) {
		t.Helper()
		require.NoError(t, err)
		require.Len(t, logs, 2)
		for _, row := range logs {
			if row.Content == "null" {
				require.False(t, row.QuotaMoneyAvailable)
				require.Nil(t, row.QuotaUSD)
				require.Equal(t, "invalid_stored_quota", row.QuotaMoneyBasis)
			} else {
				require.True(t, row.QuotaMoneyAvailable)
				require.Equal(t, "1", *row.QuotaUSD)
			}
		}
	}
	logs, _, err := GetAllLogs(LogTypeUnknown, 0, 0, "", "", "", 0, 10, 0, "", "", "")
	check(logs, err)
	logs, _, err = GetUserLogs(7, LogTypeUnknown, 0, 0, "", "", 0, 10, "", "", "")
	check(logs, err)
	logs, err = GetLogByTokenId(7)
	check(logs, err)
	logs, _, err = GetLogByTokenIdPage(7, 0, 10)
	check(logs, err)
	stat, err := SumUsedQuotaByUserID(LogTypeUnknown, 0, 0, "", 7, "", 0, "")
	require.NoError(t, err)
	require.Equal(t, 500000, stat.Quota)
	require.Equal(t, 1, stat.UnpricedQuotaRecords)
	require.False(t, stat.QuotaMoneyAvailable)
	var nullRows int64
	require.NoError(t, db.Table("logs").Where("quota IS NULL").Count(&nullRows).Error)
	require.EqualValues(t, 1, nullRows)
}

type quotaMoneyConcurrentLogger struct {
	logger.Interface
	afterSum func()
	fired    bool
}

func (hook *quotaMoneyConcurrentLogger) Trace(_ context.Context, _ time.Time, query func() (string, int64), _ error) {
	sql, _ := query()
	if !hook.fired && strings.HasPrefix(sql, "SELECT COALESCE(sum(quota), 0) quota") {
		hook.fired = true
		hook.afterSum()
	}
}

func TestQuotaStatisticSnapshotExcludesConcurrentNewCharge(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "quota-stat.sqlite")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec("PRAGMA journal_mode=WAL").Error)
	require.NoError(t, db.AutoMigrate(&Log{}))
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(3)
	previous := LOG_DB
	LOG_DB = db
	t.Cleanup(func() { LOG_DB = previous; _ = pool.Close() })
	row := Log{Type: LogTypeConsume, UserId: 7, Quota: 500000, Other: `{"pricing_credits_per_usd":500000}`}
	require.NoError(t, db.Create(&row).Error)
	hook := &quotaMoneyConcurrentLogger{Interface: logger.Default.LogMode(logger.Silent)}
	hook.afterSum = func() {
		require.NoError(t, db.Create(&Log{Type: LogTypeConsume, UserId: 7, Quota: 500000, Other: row.Other}).Error)
	}
	db.Config.Logger = hook
	stat, err := SumUsedQuotaByUserID(LogTypeUnknown, 0, 0, "", 7, "", 0, "")
	require.NoError(t, err)
	require.True(t, hook.fired, "a new charge arrived between raw sum and money projection")
	require.Equal(t, 500000, stat.Quota)
	require.True(t, stat.QuotaMoneyAvailable)
	require.Equal(t, "1", *stat.QuotaUSD)
	var count int64
	require.NoError(t, db.Model(&Log{}).Count(&count).Error)
	require.EqualValues(t, 2, count, "the concurrent charge was committed outside the snapshot")
}

func TestQuotaStatisticProjectsMixedAnchorsWithSameOwnershipAndFilters(t *testing.T) {
	db := quotaMoneyDB(t)
	now := time.Now().Unix()
	rows := []Log{
		{UserId: 7, Type: LogTypeConsume, Quota: 3359744, Other: `{"pricing_credits_per_usd":3359744}`, ModelName: "chosen", Group: "g", RequestId: "r", TokenName: "t", ChannelId: 8, CreatedAt: now},
		{UserId: 7, Type: LogTypeConsume, Quota: 500000, Other: `{"pricing_credits_per_usd":500000}`, ModelName: "chosen", Group: "g", RequestId: "r", TokenName: "t", ChannelId: 8, CreatedAt: now},
		{UserId: 8, Type: LogTypeConsume, Quota: 99, Other: `{}`, ModelName: "chosen", Group: "g", RequestId: "r", TokenName: "t", ChannelId: 8, CreatedAt: now},
		{UserId: 7, Type: LogTypeConsume, Quota: 100, Other: `{}`, ModelName: "other", Group: "g", RequestId: "r", TokenName: "t", ChannelId: 8, CreatedAt: now},
		{UserId: 7, Type: LogTypeConsume, Quota: 101, Other: `{}`, ModelName: "chosen", Group: "g", RequestId: "other", TokenName: "t", ChannelId: 8, CreatedAt: now},
		{UserId: 7, Type: LogTypeTopup, Quota: 102, Other: `{}`, ModelName: "chosen", Group: "g", RequestId: "r", TokenName: "t", ChannelId: 8, CreatedAt: now},
	}
	require.NoError(t, db.Create(&rows).Error)
	stat, err := SumUsedQuotaByUserID(LogTypeUnknown, now-1, now+1, "chosen", 7, "t", 8, "g", "r")
	require.NoError(t, err)
	require.Equal(t, 3859744, stat.Quota)
	require.True(t, stat.QuotaMoneyAvailable)
	require.Equal(t, "2", *stat.QuotaUSD)
	require.Zero(t, stat.UnpricedQuotaRecords)
	require.False(t, stat.QuotaProjectionTruncated)
	stat, err = SumUsedQuotaByUserID(LogTypeUnknown, 0, 0, "", 7, "", 0, "")
	require.NoError(t, err)
	require.Equal(t, 3859945, stat.Quota)
	require.False(t, stat.QuotaMoneyAvailable)
	require.Nil(t, stat.QuotaUSD)
	require.Equal(t, 2, stat.UnpricedQuotaRecords)
	stat, err = SumUsedQuotaByUserID(LogTypeUnknown, 0, 0, "", 9, "", 0, "")
	require.NoError(t, err)
	require.True(t, stat.QuotaMoneyAvailable)
	require.Equal(t, "0", *stat.QuotaUSD)
	require.Equal(t, "empty_result", stat.QuotaMoneyBasis)
	var stored Log
	require.NoError(t, db.First(&stored, rows[0].Id).Error)
	require.Equal(t, 3359744, stored.Quota)
}

func TestQuotaStatisticRoundsOnceAndBoundsHistoryWork(t *testing.T) {
	db := quotaMoneyDB(t)
	logs := []Log{{Quota: 1, Other: `{"pricing_credits_per_usd":3}`}, {Quota: 1, Other: `{"pricing_credits_per_usd":3}`}, {Quota: 1, Other: `{"pricing_credits_per_usd":3}`}}
	require.NoError(t, db.Create(&logs).Error)
	projection, _, truncated, err := projectQuotaStatistic(db.Table("logs"))
	require.NoError(t, err)
	require.False(t, truncated)
	require.Equal(t, "1", *projection.QuotaUSD)
	bounded := make([]Log, quotaMoneyMaxRecords+1)
	for i := range bounded {
		bounded[i] = Log{UserId: 2, Quota: 1, Other: `{"pricing_credits_per_usd":500000}`}
	}
	require.NoError(t, db.CreateInBatches(bounded, 100).Error)
	projection, _, truncated, err = projectQuotaStatistic(db.Table("logs").Where("user_id = ? AND id < ?", 2, bounded[len(bounded)-1].Id))
	require.NoError(t, err)
	require.False(t, truncated)
	require.True(t, projection.QuotaMoneyAvailable)
	require.Equal(t, "0.02", *projection.QuotaUSD)
	projection, _, truncated, err = projectQuotaStatistic(db.Table("logs").Where("user_id = ?", 2))
	require.NoError(t, err)
	require.True(t, truncated)
	require.False(t, projection.QuotaMoneyAvailable)
	require.Nil(t, projection.QuotaUSD)
	bases := make([]Log, quotaMoneyMaxBases+1)
	for i := range bases {
		bases[i] = Log{UserId: 3, Quota: 1, Other: fmt.Sprintf(`{"pricing_credits_per_usd":%d}`, 1000+i)}
	}
	require.NoError(t, db.Create(&bases).Error)
	projection, _, truncated, err = projectQuotaStatistic(db.Table("logs").Where("user_id = ? AND id < ?", 3, bases[len(bases)-1].Id))
	require.NoError(t, err)
	require.False(t, truncated)
	require.True(t, projection.QuotaMoneyAvailable)
	projection, _, truncated, err = projectQuotaStatistic(db.Table("logs").Where("user_id = ?", 3))
	require.NoError(t, err)
	require.True(t, truncated)
	require.Nil(t, projection.QuotaUSD)
}

func TestQuotaProjectionAddsNoStoredColumnsAndDoesNotGuessBucketUnits(t *testing.T) {
	db := quotaMoneyDB(t)
	for _, table := range []string{"logs", "quota_data"} {
		for _, column := range []string{"quota_usd", "quota_money_available", "quota_money_basis", "quota_stored_valid"} {
			require.False(t, db.Migrator().HasColumn(table, column))
		}
	}
	require.NoError(t, db.Create(&QuotaData{UserID: 7, Quota: 3859744, CreatedAt: 3600, UseGroup: "g"}).Error)
	rows, err := GetQuotaDataByUserId(7, 0, 7200)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 3859744, rows[0].Quota)
	require.False(t, rows[0].QuotaMoneyAvailable)
	require.Equal(t, "bucket_without_credit_basis", rows[0].QuotaMoneyBasis)
	flow, err := GetFlowQuotaData(0, 7200, "", 7, 1)
	require.NoError(t, err)
	require.Len(t, flow, 1)
	require.False(t, flow[0].QuotaMoneyAvailable)
	require.Equal(t, "bucket_without_credit_basis", flow[0].QuotaMoneyBasis)
}
