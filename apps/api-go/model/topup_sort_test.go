package model

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupTopUpSortModelTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&TopUp{}))
	previousDB := DB
	previousType := common.MainDatabaseType()
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			require.NoError(t, sqlDB.Close())
		}
	})
	return db
}

func topUpSortPage(page, pageSize int) *common.PageInfo {
	return &common.PageInfo{Page: page, PageSize: pageSize}
}

func topUpSortIDs(topups []*TopUp) []int {
	ids := make([]int, 0, len(topups))
	for _, topup := range topups {
		ids = append(ids, topup.Id)
	}
	return ids
}

func seedTopUpSortRows(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now().Unix()
	require.NoError(t, db.Create(&[]TopUp{
		{Id: 1, UserId: 7, TradeNo: "match-one", Amount: 20, Money: 9, Status: common.TopUpStatusFailed, PaymentMethod: "stripe", CreateTime: now - 5},
		{Id: 2, UserId: 7, TradeNo: "match-two", Amount: 10, Money: 5, Status: common.TopUpStatusSuccess, PaymentMethod: "epay", CreateTime: now - 4},
		{Id: 3, UserId: 7, TradeNo: "match-three", Amount: 10, Money: 3, Status: common.TopUpStatusPending, PaymentMethod: "stripe", CreateTime: now - 3},
		{Id: 4, UserId: 7, TradeNo: "other-four", Amount: 30, Money: 7, Status: common.TopUpStatusSuccess, PaymentMethod: "creem", CreateTime: now - 2},
		{Id: 5, UserId: 8, TradeNo: "match-five", Amount: 1, Money: 1, Status: common.TopUpStatusSuccess, PaymentMethod: "epay", CreateTime: now - 1},
	}).Error)
}

func TestTopUpSortIsGlobalStableAndUserIsolatedAcrossPages(t *testing.T) {
	db := setupTopUpSortModelTestDB(t)
	seedTopUpSortRows(t, db)

	ascending := NewTopUpSortSpec("amount", "asc", false)
	pageOne, total, err := GetUserTopUps(7, topUpSortPage(1, 2), ascending)
	require.NoError(t, err)
	require.Equal(t, int64(4), total)
	require.Equal(t, []int{2, 3}, topUpSortIDs(pageOne), "equal amounts use id ASC as the stable tie-breaker")
	pageTwo, _, err := GetUserTopUps(7, topUpSortPage(2, 2), ascending)
	require.NoError(t, err)
	require.Equal(t, []int{1, 4}, topUpSortIDs(pageTwo))

	descending := NewTopUpSortSpec("amount", "desc", false)
	pageOne, _, err = GetUserTopUps(7, topUpSortPage(1, 2), descending)
	require.NoError(t, err)
	require.Equal(t, []int{4, 1}, topUpSortIDs(pageOne))
	pageTwo, _, err = GetUserTopUps(7, topUpSortPage(2, 2), descending)
	require.NoError(t, err)
	require.Equal(t, []int{3, 2}, topUpSortIDs(pageTwo))
}

func TestTopUpSortAppliesToSearchAndAdminHistory(t *testing.T) {
	db := setupTopUpSortModelTestDB(t)
	seedTopUpSortRows(t, db)

	searched, total, err := SearchUserTopUps(7, "%match%", topUpSortPage(1, 2), NewTopUpSortSpec("money", "asc", false))
	require.NoError(t, err)
	require.Equal(t, int64(3), total)
	require.Equal(t, []int{3, 2}, topUpSortIDs(searched))
	searched, _, err = SearchUserTopUps(7, "%match%", topUpSortPage(2, 2), NewTopUpSortSpec("money", "asc", false))
	require.NoError(t, err)
	require.Equal(t, []int{1}, topUpSortIDs(searched))

	admin, total, err := GetAllTopUps(topUpSortPage(1, 3), NewTopUpSortSpec("user_id", "desc", true))
	require.NoError(t, err)
	require.Equal(t, int64(5), total)
	require.Equal(t, []int{5, 4, 3}, topUpSortIDs(admin), "admin history includes every user and uses id DESC within equal user ids")
	adminSearch, _, err := SearchAllTopUps("%match%", topUpSortPage(1, 5), NewTopUpSortSpec("trade_no", "asc", true))
	require.NoError(t, err)
	require.Equal(t, []int{5, 1, 3, 2}, topUpSortIDs(adminSearch))
}

func TestTopUpSortWhitelistAndInvalidFallback(t *testing.T) {
	commonFields := map[string]string{
		"create_time":    "create_time ASC, id ASC",
		"amount":         "amount ASC, id ASC",
		"money":          "money ASC, id ASC",
		"status":         "status ASC, id ASC",
		"payment_method": "payment_method ASC, id ASC",
	}
	for field, want := range commonFields {
		require.Equal(t, want, NewTopUpSortSpec(field, "asc", false).orderClause(), field)
	}
	require.Equal(t, "user_id ASC, id ASC", NewTopUpSortSpec("user_id", "asc", true).orderClause())
	require.Equal(t, "trade_no DESC, id DESC", NewTopUpSortSpec("trade_no", "desc", true).orderClause())
	require.Equal(t, "create_time DESC, id DESC", NewTopUpSortSpec("user_id", "asc", false).orderClause())
	require.Equal(t, "create_time DESC, id DESC", NewTopUpSortSpec("money; DROP TABLE top_ups", "sideways", true).orderClause())
}

func topUpSortDryRunDB(t *testing.T, dialect string) *gorm.DB {
	t.Helper()
	config := &gorm.Config{DryRun: true, DisableAutomaticPing: true}
	var (
		db  *gorm.DB
		err error
	)
	switch dialect {
	case "mysql":
		db, err = gorm.Open(mysql.New(mysql.Config{
			DSN:                       "gorm:gorm@tcp(127.0.0.1:9910)/gorm?charset=utf8mb4&parseTime=True&loc=Local",
			SkipInitializeWithVersion: true,
		}), config)
	case "postgres":
		db, err = gorm.Open(postgres.New(postgres.Config{
			DSN:                  "host=127.0.0.1 port=9910 user=gorm dbname=gorm sslmode=disable",
			PreferSimpleProtocol: true,
		}), config)
	default:
		t.Fatalf("unsupported dialect %q", dialect)
	}
	require.NoError(t, err)
	return db
}

func TestTopUpSortSQLIsPortableAndPrecedesPagination(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			statement := applyTopUpSort(
				topUpSortDryRunDB(t, dialect).Model(&TopUp{}),
				NewTopUpSortSpec("payment_method", "desc", true),
			).Limit(25).Offset(50).Find(&[]TopUp{}).Statement
			require.NoError(t, statement.Error)
			sql := statement.SQL.String()
			orderIndex := strings.Index(sql, "ORDER BY payment_method DESC, id DESC")
			limitIndex := strings.Index(sql, "LIMIT")
			offsetIndex := strings.Index(sql, "OFFSET")
			require.GreaterOrEqual(t, orderIndex, 0, sql)
			require.Greater(t, limitIndex, orderIndex, sql)
			require.Greater(t, offsetIndex, limitIndex, sql)
		})
	}
}
