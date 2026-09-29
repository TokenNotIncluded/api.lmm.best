// Generate funding vectors by executing current Go BillingSession against an
// isolated local PostgreSQL schema. No provider or external payment is called.
// Run from apps/api-go with LMM_TEST_DATABASE_URL supplied by local-services:
// GOMAXPROCS=2 go run -p=2 ../api-rust/tests/behavior-oracle/fixtures/relay_funding.go OUTPUT.json
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type grant struct {
	Total    int64 `json:"total"`
	Used     int64 `json:"used"`
	Overflow bool  `json:"overflow"`
}

type snapshot struct {
	Wallet        int     `json:"wallet"`
	TokenRemain   int     `json:"token_remain"`
	TokenUsed     int     `json:"token_used"`
	Subscriptions []int64 `json:"subscriptions"`
	LedgerStatus  string  `json:"ledger_status"`
	LedgerQuota   int64   `json:"ledger_actual_quota"`
	LedgerWallet  int64   `json:"ledger_wallet_quota"`
}

type testcase struct {
	Name          string    `json:"name"`
	Preference    string    `json:"preference"`
	Wallet        int       `json:"wallet"`
	TokenQuota    int       `json:"token_quota"`
	TokenInfinite bool      `json:"token_unlimited"`
	Grants        []grant   `json:"grants"`
	Force         bool      `json:"force_preconsume"`
	Budget        int       `json:"budget"`
	Grow          int       `json:"grow"`
	Actual        int       `json:"actual"`
	Refund        bool      `json:"refund"`
	DeleteToken   bool      `json:"delete_token_after_reserve"`
	Source        string    `json:"source"`
	Reserved      int       `json:"reserved_quota"`
	ErrorCode     string    `json:"error_code"`
	ErrorStatus   int       `json:"error_status"`
	SettleError   bool      `json:"settle_error"`
	AfterReserve  snapshot  `json:"after_reserve"`
	AfterGrow     *snapshot `json:"after_grow,omitempty"`
	AfterFinal    snapshot  `json:"after_final"`
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func read(db *gorm.DB, userID, tokenID int, requestID string) snapshot {
	var user model.User
	var token model.Token
	check(db.First(&user, userID).Error)
	check(db.Unscoped().First(&token, tokenID).Error)
	result := snapshot{Wallet: user.Quota, TokenRemain: token.RemainQuota, TokenUsed: token.UsedQuota, Subscriptions: []int64{}}
	check(db.Model(&model.UserSubscription{}).Where("user_id=?", userID).Order("id").Pluck("amount_used", &result.Subscriptions).Error)
	var record model.SubscriptionPreConsumeRecord
	err := db.Where("request_id=?", requestID).First(&record).Error
	if err == nil {
		result.LedgerStatus, result.LedgerQuota, result.LedgerWallet = record.Status, record.ActualQuota, record.WalletConsumed
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		panic(err)
	}
	return result
}

func main() {
	if len(os.Args) != 2 {
		panic("expected output path")
	}
	dsn, err := url.Parse(os.Getenv("LMM_TEST_DATABASE_URL"))
	check(err)
	database := strings.TrimPrefix(dsn.Path, "/")
	if (dsn.Scheme != "postgres" && dsn.Scheme != "postgresql") || (dsn.Hostname() != "127.0.0.1" && dsn.Hostname() != "localhost") || (database != "lmm_ci" && !strings.HasPrefix(database, "lmm_test")) {
		panic("oracle requires a loopback lmm_test* or lmm_ci database")
	}
	config := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	admin, err := gorm.Open(postgres.Open(dsn.String()), config)
	check(err)
	schema := fmt.Sprintf("relay_funding_oracle_%d", time.Now().UnixNano())
	check(admin.Exec("CREATE SCHEMA " + schema).Error)
	defer func() { check(admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error) }()
	query := dsn.Query()
	query.Set("search_path", schema)
	dsn.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(dsn.String()), config)
	check(err)
	sqlDB, err := db.DB()
	check(err)
	sqlDB.SetMaxOpenConns(4)
	defer sqlDB.Close()
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	common.RedisEnabled, common.BatchUpdateEnabled = false, false
	common.QuotaPerUnit = 500000
	gin.SetMode(gin.TestMode)
	check(db.AutoMigrate(&model.User{}, &model.Token{}, &model.SubscriptionPlan{}, &model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}))
	full := []grant{{Total: 100, Overflow: true}}
	cases := []testcase{
		{Name: "wallet-only", Preference: "wallet_only", Wallet: 100, Grants: full, Actual: 4},
		{Name: "wallet-first", Preference: "wallet_first", Wallet: 100, Grants: full, Actual: 4},
		{Name: "wallet-first-fallback", Preference: "wallet_first", Wallet: 5, Grants: full, Actual: 4},
		{Name: "subscription-only", Preference: "subscription_only", Wallet: 100, Grants: full, Actual: 4},
		{Name: "subscription-first", Preference: "subscription_first", Wallet: 100, Grants: full, Actual: 4},
		{Name: "unknown-preference", Preference: "invalid", Wallet: 100, Grants: full, Actual: 4},
		{Name: "no-subscription-wallet-fallback", Preference: "subscription_first", Wallet: 100, Actual: 4},
		{Name: "no-subscription-only-denied", Preference: "subscription_only", Wallet: 100, Actual: 4},
		{Name: "exhausted-subscription-wallet-fallback", Preference: "subscription_first", Wallet: 100, Grants: []grant{{Total: 10, Used: 10, Overflow: true}}, Actual: 4},
		{Name: "strict-subscription-denied", Preference: "subscription_first", Wallet: 100, Grants: []grant{{Total: 3}}, Actual: 4},
		{Name: "strict-other-grant-disables-overflow", Preference: "subscription_first", Wallet: 100, Grants: []grant{{Total: 3, Overflow: true}, {Total: 2}}, Actual: 4},
		{Name: "partial-subscription-wallet", Preference: "subscription_first", Wallet: 7, Grants: []grant{{Total: 3, Overflow: true}}, Actual: 8},
		{Name: "partial-wallet-insufficient-rollback", Preference: "subscription_first", Wallet: 6, Grants: []grant{{Total: 3, Overflow: true}}, Actual: 4},
		{Name: "largest-partial-not-multiple-grants", Preference: "subscription_first", Wallet: 3, Grants: []grant{{Total: 3, Overflow: true}, {Total: 7, Overflow: true}}, Actual: 9},
		{Name: "full-grant-before-partial", Preference: "subscription_first", Wallet: 100, Grants: []grant{{Total: 3, Overflow: true}, {Total: 20, Overflow: true}}, Actual: 4},
		{Name: "unlimited-grant", Preference: "subscription_only", Wallet: 0, Grants: []grant{{Total: 0}}, Actual: 20},
		{Name: "unlimited-token-counts-usage", Preference: "subscription_only", Wallet: 0, Grants: full, TokenInfinite: true, Actual: 4},
		{Name: "token-insufficient-rollback", Preference: "subscription_first", Wallet: 100, Grants: full, TokenQuota: 5, Actual: 4},
		{Name: "actual-subscription-overflow", Preference: "subscription_first", Wallet: 100, Grants: []grant{{Total: 12, Overflow: true}}, Actual: 20},
		{Name: "actual-strict-overflow-retains-intent", Preference: "subscription_only", Wallet: 100, Grants: []grant{{Total: 12}}, Actual: 20},
		{Name: "wallet-actual-overage-debt", Preference: "wallet_only", Wallet: 100, Actual: 200},
		{Name: "wallet-refund", Preference: "wallet_only", Wallet: 100, Refund: true},
		{Name: "subscription-refund", Preference: "subscription_first", Wallet: 100, Grants: full, Refund: true},
		{Name: "high-balance-still-reserves", Preference: "wallet_only", Wallet: 6000000, TokenQuota: 6000000, Actual: 4},
		{Name: "force-high-balance-reserves", Preference: "wallet_only", Wallet: 6000000, TokenQuota: 6000000, Force: true, Actual: 4},
		{Name: "wallet-grow-overage-debt", Preference: "wallet_only", Wallet: 15, Grow: 20, Actual: 20},
		{Name: "subscription-grow", Preference: "subscription_first", Wallet: 100, Grants: full, Grow: 20, Actual: 15},
		{Name: "soft-deleted-subscription-token-settles", Preference: "subscription_first", Wallet: 100, Grants: full, DeleteToken: true, Actual: 20},
	}
	for i := range cases {
		tc := &cases[i]
		tc.Budget = 10
		if tc.TokenQuota == 0 && !tc.TokenInfinite {
			tc.TokenQuota = 10000
		}
		if tc.Grants == nil {
			tc.Grants = []grant{}
		}
		userID, tokenID := 1000+i, 2000+i
		key, requestID := fmt.Sprintf("fixture-token-%d", i), fmt.Sprintf("oracle-request-%d", i)
		check(db.Create(&model.User{Id: userID, Username: fmt.Sprintf("oracle-%d", i), AffCode: fmt.Sprintf("aff-%d", i), Quota: tc.Wallet}).Error)
		check(db.Create(&model.Token{Id: tokenID, UserId: userID, Key: key, RemainQuota: tc.TokenQuota, UnlimitedQuota: tc.TokenInfinite}).Error)
		for j, grant := range tc.Grants {
			planID, subID := 3000+i*10+j, 4000+i*10+j
			check(db.Create(&model.SubscriptionPlan{Id: planID, Title: "funding oracle", DurationUnit: "day", DurationValue: 1, QuotaResetPeriod: "never"}).Error)
			check(db.Create(&model.UserSubscription{Id: subID, UserId: userID, PlanId: planID, AmountTotal: grant.Total, AmountUsed: grant.Used, Status: "active", StartTime: time.Now().Unix(), EndTime: time.Now().Unix() + 3600 + int64(j), AllowWalletOverflow: grant.Overflow}).Error)
		}
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		ctx.Set("token_quota", tc.TokenQuota)
		info := &relaycommon.RelayInfo{UserId: userID, TokenId: tokenID, TokenKey: key, TokenUnlimited: tc.TokenInfinite, RequestId: requestID, UserQuota: tc.Wallet, OriginModelName: "oracle-model", ForcePreConsume: tc.Force, UserSetting: dto.UserSetting{BillingPreference: tc.Preference}}
		session, apiErr := service.NewBillingSession(ctx, info, tc.Budget)
		if apiErr != nil {
			tc.ErrorCode, tc.ErrorStatus = string(apiErr.GetErrorCode()), apiErr.StatusCode
			tc.AfterReserve = read(db, userID, tokenID, requestID)
			tc.AfterFinal = tc.AfterReserve
			continue
		}
		tc.Source, tc.Reserved = info.BillingSource, session.GetPreConsumedQuota()
		tc.AfterReserve = read(db, userID, tokenID, requestID)
		if tc.Grow > 0 {
			check(session.Reserve(tc.Grow))
			grown := read(db, userID, tokenID, requestID)
			tc.AfterGrow = &grown
		}
		if tc.DeleteToken {
			check(db.Delete(&model.Token{}, tokenID).Error)
		}
		if tc.Refund {
			session.Refund(ctx)
			session.Refund(ctx)
			deadline := time.Now().Add(3 * time.Second)
			for {
				state := read(db, userID, tokenID, requestID)
				if state.Wallet == tc.Wallet && state.TokenRemain == tc.TokenQuota {
					break
				}
				if time.Now().After(deadline) {
					panic("refund did not reach durable balances")
				}
				time.Sleep(time.Millisecond)
			}
		} else {
			tc.SettleError = session.Settle(tc.Actual) != nil
			if !tc.SettleError {
				check(session.Settle(tc.Actual))
				session.Refund(ctx) // A late refund must not undo committed usage.
			}
		}
		tc.AfterFinal = read(db, userID, tokenID, requestID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = service.DrainBillingRefundTasks(ctx)
	check(err)
	data, err := json.MarshalIndent(cases, "", "  ")
	check(err)
	check(os.WriteFile(os.Args[1], append(data, '\n'), 0644))
	fmt.Printf("wrote %d current-Go PostgreSQL funding vectors\n", len(cases))
}
