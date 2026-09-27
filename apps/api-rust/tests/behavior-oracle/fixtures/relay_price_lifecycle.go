// Current-Go price/admission/configuration timing oracle, with real PostgreSQL.
// Run from apps/api-go under tests/scripts/with-local-services.py:
// GOMAXPROCS=2 go run -p=2 ../api-rust/tests/behavior-oracle/fixtures/relay_price_lifecycle.go OUTPUT.json
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	relayconstant "github.com/LIghtJUNction/api.lmm.best/relay/constant"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/config"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type balances struct {
	Wallet int `json:"wallet"`
	Token  int `json:"token"`
	Used   int `json:"used"`
}

type priceCase struct {
	Name            string            `json:"name"`
	Options         map[string]string `json:"options"`
	Retry           map[string]string `json:"retry_options,omitempty"`
	Final           map[string]string `json:"final_options,omitempty"`
	Wallet          int               `json:"wallet"`
	Refund          bool              `json:"refund"`
	Tool            string            `json:"tool"`
	Usage           json.RawMessage   `json:"usage"`
	Free            bool              `json:"free"`
	Prepaid         int               `json:"prepaid"`
	Status          int               `json:"status"`
	ErrorCode       string            `json:"error_code"`
	Reserved        balances          `json:"reserved"`
	Retried         *balances         `json:"retried,omitempty"`
	Settled         balances          `json:"settled"`
	Actual          int               `json:"actual"`
	ModelRatio      float64           `json:"model_ratio"`
	CompletionRatio float64           `json:"completion_ratio"`
	GroupRatio      float64           `json:"group_ratio"`
	ModelPrice      float64           `json:"model_price"`
	ToolPrice       float64           `json:"tool_price"`
	QuotaUnit       float64           `json:"quota_unit"`
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func apply(options map[string]string) {
	for key, value := range options {
		switch key {
		case "ModelPrice":
			must(ratio_setting.UpdateModelPriceByJSONString(value))
		case "ModelRatio":
			must(ratio_setting.UpdateModelRatioByJSONString(value))
		case "CompletionRatio":
			must(ratio_setting.UpdateCompletionRatioByJSONString(value))
		case "CacheRatio":
			must(ratio_setting.UpdateCacheRatioByJSONString(value))
		case "CreateCacheRatio":
			must(ratio_setting.UpdateCreateCacheRatioByJSONString(value))
		case "ImageRatio":
			must(ratio_setting.UpdateImageRatioByJSONString(value))
		case "GroupRatio":
			must(ratio_setting.UpdateGroupRatioByJSONString(value))
		case "GroupGroupRatio":
			must(ratio_setting.UpdateGroupGroupRatioByJSONString(value))
		case "QuotaPerUnit":
			var err error
			common.QuotaPerUnit, err = strconv.ParseFloat(value, 64)
			must(err)
		case "PreConsumedQuota":
			var err error
			common.PreConsumedQuota, err = strconv.Atoi(value)
			must(err)
		case "tool_price_setting.prices":
			operation_setting.LoadToolPricesFromJSONString(value)
		case "quota_setting.enable_free_model_pre_consume":
			must(config.GlobalConfig.LoadFromDB(map[string]string{key: value}))
		default:
			panic("unhandled fixture option " + key)
		}
	}
}

func initial() map[string]string {
	return map[string]string{"ModelPrice": "{}", "ModelRatio": `{"priced-model":2}`, "CompletionRatio": `{"priced-model":3}`, "CacheRatio": `{"priced-model":0.25}`, "CreateCacheRatio": `{"priced-model":1.25}`, "ImageRatio": `{"priced-model":2}`, "GroupRatio": `{"default":1}`, "GroupGroupRatio": "{}", "QuotaPerUnit": "500000", "PreConsumedQuota": "10", "tool_price_setting.prices": `{"file_search":2.5}`}
}

func state(db *gorm.DB, user, token int) balances {
	var u model.User
	var t model.Token
	must(db.First(&u, user).Error)
	must(db.First(&t, token).Error)
	return balances{Wallet: u.Quota, Token: t.RemainQuota, Used: t.UsedQuota}
}

func main() {
	if len(os.Args) != 2 {
		panic("expected output path")
	}
	dsn, err := url.Parse(os.Getenv("LMM_TEST_DATABASE_URL"))
	must(err)
	database := strings.TrimPrefix(dsn.Path, "/")
	if (dsn.Scheme != "postgres" && dsn.Scheme != "postgresql") || (dsn.Hostname() != "127.0.0.1" && dsn.Hostname() != "localhost") || (database != "lmm_ci" && !strings.HasPrefix(database, "lmm_test")) {
		panic("requires loopback test PostgreSQL")
	}
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	admin, err := gorm.Open(postgres.Open(dsn.String()), cfg)
	must(err)
	schema := fmt.Sprintf("relay_price_oracle_%d", time.Now().UnixNano())
	must(admin.Exec("CREATE SCHEMA " + schema).Error)
	defer func() { must(admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error) }()
	query := dsn.Query()
	query.Set("search_path", schema)
	dsn.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(dsn.String()), cfg)
	must(err)
	sqlDB, err := db.DB()
	must(err)
	sqlDB.SetMaxOpenConns(4)
	defer sqlDB.Close()
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = false, false, false
	common.QuotaRemindThreshold = 0
	gin.SetMode(gin.TestMode)
	must(db.AutoMigrate(&model.User{}, &model.Token{}, &model.Channel{}, &model.SubscriptionPlan{}, &model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}))
	must(db.Create(&model.Channel{Id: 7, Type: 1, Key: "fixture"}).Error)
	usage := json.RawMessage(`{"prompt_tokens":20,"completion_tokens":5}`)
	cases := []priceCase{}
	for _, kind := range []string{"fixed-zero", "ratio-zero", "group-zero"} {
		for _, flag := range []string{"default", "true", "false"} {
			for _, wallet := range []int{0, 1000} {
				options := initial()
				switch kind {
				case "fixed-zero":
					options["ModelPrice"] = `{"priced-model":0}`
				case "ratio-zero":
					options["ModelRatio"] = `{"priced-model":0}`
				case "group-zero":
					options["GroupRatio"] = `{"default":0}`
				}
				if flag != "default" {
					options["quota_setting.enable_free_model_pre_consume"] = flag
				}
				cases = append(cases, priceCase{Name: fmt.Sprintf("%s-%s-wallet-%d", kind, flag, wallet), Options: options, Wallet: wallet, Usage: usage})
			}
		}
		for _, flag := range []string{"true", "false"} {
			options := initial()
			options["quota_setting.enable_free_model_pre_consume"] = flag
			switch kind {
			case "fixed-zero":
				options["ModelPrice"] = `{"priced-model":0}`
			case "ratio-zero":
				options["ModelRatio"] = `{"priced-model":0}`
			case "group-zero":
				options["GroupRatio"] = `{"default":0}`
			}
			cases = append(cases, priceCase{Name: kind + "-" + flag + "-failed-empty", Options: options, Wallet: 1000, Refund: true, Usage: json.RawMessage(`{}`)})
		}
	}
	for _, flag := range []string{"true", "false"} {
		for _, wallet := range []int{0, 1000} {
			options := initial()
			options["ModelPrice"] = `{"priced-model":0.000001}`
			options["quota_setting.enable_free_model_pre_consume"] = flag
			cases = append(cases, priceCase{Name: fmt.Sprintf("tiny-fixed-%s-wallet-%d", flag, wallet), Options: options, Wallet: wallet, Usage: usage})
		}
	}
	for _, fixed := range []bool{false, true} {
		options := initial()
		options["GroupRatio"] = `{"default":0.5}`
		if fixed {
			options["ModelPrice"] = `{"priced-model":0.000002}`
		}
		retry := map[string]string{"ModelPrice": `{"priced-model":99}`, "ModelRatio": `{"priced-model":99}`, "CompletionRatio": `{"priced-model":99}`, "CacheRatio": `{"priced-model":99}`, "CreateCacheRatio": `{"priced-model":99}`, "ImageRatio": `{"priced-model":99}`, "GroupRatio": `{"default":2}`, "QuotaPerUnit": "1000000", "tool_price_setting.prices": `{"file_search":5}`}
		final := map[string]string{"ModelPrice": `{"priced-model":88}`, "ModelRatio": `{"priced-model":88}`, "CompletionRatio": `{"priced-model":88}`, "GroupRatio": `{"default":7}`, "QuotaPerUnit": "2000000", "tool_price_setting.prices": `{"file_search":7}`}
		cases = append(cases, priceCase{Name: fmt.Sprintf("retry-fixed-%t", fixed), Options: options, Retry: retry, Final: final, Wallet: 100000, Tool: "file_search", Usage: json.RawMessage(`{"prompt_tokens":20,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":4,"cached_creation_tokens":3,"image_tokens":2}}`)})
	}
	for _, freeFirst := range []bool{false, true} {
		options := initial()
		options["quota_setting.enable_free_model_pre_consume"] = "false"
		options["GroupRatio"] = `{"default":0.5}`
		retry := map[string]string{"GroupRatio": `{"default":0}`}
		if freeFirst {
			options["GroupRatio"] = `{"default":0}`
			retry["GroupRatio"] = `{"default":2}`
		}
		cases = append(cases, priceCase{Name: fmt.Sprintf("retry-free-first-%t", freeFirst), Options: options, Retry: retry, Wallet: 100000, Usage: usage})
	}
	for index := range cases {
		tc := &cases[index]
		operation_setting.GetQuotaSetting().EnableFreeModelPreConsume = true
		apply(tc.Options)
		user, token := 1000+index, 2000+index
		key := fmt.Sprintf("lifecycle-token-%d", index)
		must(db.Create(&model.User{Id: user, Username: fmt.Sprintf("lifecycle-%d", index), AffCode: fmt.Sprintf("aff-%d", index), Quota: tc.Wallet}).Error)
		must(db.Create(&model.Token{Id: token, UserId: user, Key: key, RemainQuota: 1000000}).Error)
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"priced-model","input":"hello"}`))
		ctx.Set(common.RequestIdKey, tc.Name)
		info := &relaycommon.RelayInfo{OriginModelName: "priced-model", UsingGroup: "default", UserGroup: "default", RequestId: tc.Name, StartTime: time.Now(), RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeResponses, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 7, ChannelType: 1, UpstreamModelName: "priced-model"}}
		price, err := helper.ModelPriceHelper(ctx, info, 3, &types.TokenCountMeta{})
		must(err)
		tc.Free = price.FreeModel
		// Identity is irrelevant to option timing; use the actual payer only
		// after initial price selection so no trust discount alters the vector.
		info.UserId, info.TokenId, info.TokenKey = user, token, key
		info.UserSetting = dto.UserSetting{BillingPreference: "wallet_only"}
		if !price.FreeModel {
			if apiErr := service.PreConsumeBilling(ctx, price.QuotaToPreConsume, info); apiErr != nil {
				tc.Status, tc.ErrorCode = apiErr.StatusCode, string(apiErr.GetErrorCode())
				tc.Reserved = state(db, user, token)
				tc.Settled = tc.Reserved
				continue
			}
		}
		tc.Status = 200
		tc.Prepaid = info.FinalPreConsumedQuota
		tc.Reserved = state(db, user, token)
		if len(tc.Retry) > 0 {
			apply(tc.Retry)
			// getChannel calls HandleGroupRatio each attempt; model PriceData is
			// retained. UserId=0 isolates this test from paid-credit aggregation.
			info.UserId = 0
			info.PriceData.GroupRatioInfo = helper.HandleGroupRatio(ctx, info)
			info.UserId = user
			mustAPI := service.PrepareTieredBillingForSelectedGroup(ctx, info)
			if mustAPI != nil {
				panic(mustAPI)
			}
			after := state(db, user, token)
			tc.Retried = &after
		}
		if tc.Tool != "" {
			info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
		}
		apply(tc.Final)
		if tc.Refund {
			if info.Billing != nil {
				info.Billing.Refund(ctx)
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				current := state(db, user, token)
				if current.Wallet == tc.Wallet && current.Token == 1000000 && current.Used == 0 {
					break
				}
				if time.Now().After(deadline) {
					panic("refund timeout")
				}
				time.Sleep(time.Millisecond)
			}
		} else {
			var usage dto.Usage
			must(json.Unmarshal(tc.Usage, &usage))
			service.PostTextConsumeQuota(ctx, info, &usage, nil)
		}
		tc.Settled = state(db, user, token)
		tc.Actual = tc.Wallet - tc.Settled.Wallet
		tc.ModelRatio, tc.CompletionRatio, tc.ModelPrice = info.PriceData.ModelRatio, info.PriceData.CompletionRatio, info.PriceData.ModelPrice
		tc.GroupRatio = info.PriceData.GroupRatioInfo.GroupRatio
		tc.QuotaUnit = common.QuotaPerUnit
		if tc.Tool != "" {
			tc.ToolPrice = operation_setting.GetToolPriceForModel(tc.Tool, info.OriginModelName)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = service.DrainBillingRefundTasks(ctx)
	must(err)
	data, err := json.MarshalIndent(cases, "", "  ")
	must(err)
	must(os.WriteFile(os.Args[1], append(data, '\n'), 0644))
	fmt.Printf("wrote %d current-Go price lifecycle vectors\n", len(cases))
}
