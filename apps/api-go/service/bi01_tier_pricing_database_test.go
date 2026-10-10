// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/servicetier"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	hosttypes "github.com/LIghtJUNction/api.lmm.best/types"
	"github.com/stretchr/testify/require"
)

// This is an application/database test, not a replacement wallet model. The
// existing fixture owns an isolated SQLite database and a local Redis test
// server. Never point this test at an application or production database.
func TestBI01TierUsageWalletAndConsumeLog(t *testing.T) {
	const openingBalance = 100_000_000
	for _, tc := range []struct {
		name, requested, actual      string
		input, output, cached, write int
		want                         int
	}{
		{"below", "fast", "priority", 271999, 0, 0, 0, 3263988},
		{"equal", "fast", "priority", 272000, 0, 0, 0, 3264000},
		{"audit-long", "fast", "priority", 272001, 0, 0, 0, 6528024},
		{"ultrafast-long", "ultrafast", "ultrafast", 272001, 0, 0, 0, 19584072},
		{"returned-fast-cache", "ultrafast", "priority", 272001, 1000, 2000, 1000, 6580824},
		{"returned-ultrafast-cache", "fast", "ultrafast", 272001, 1000, 2000, 1000, 19742472},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// These helpers create and restore DB/cache globals. Do not parallelize.
			db, info, ctx := subscriptionBillingFixture(t, 100000, true, "wallet_only")
			require.NoError(t, db.AutoMigrate(&model.Log{}, &model.Channel{}))
			previousLogDB, previousLogEnabled := model.LOG_DB, common.LogConsumeEnabled
			model.LOG_DB, common.LogConsumeEnabled = db, true
			t.Cleanup(func() { model.LOG_DB, common.LogConsumeEnabled = previousLogDB, previousLogEnabled })
			require.NoError(t, db.Model(&model.User{}).Where("id = ?", info.UserId).Update("quota", openingBalance).Error)
			require.NoError(t, db.Model(&model.Token{}).Where("id = ?", info.TokenId).Update("remain_quota", openingBalance).Error)
			cacheWalletQuotaForBillingTest(t, info.UserId, openingBalance)
			var token model.Token
			require.NoError(t, db.First(&token, info.TokenId).Error)
			cacheBillingTokenForTest(t, token)
			channel := model.Channel{Name: "bi01-local-upstream"}
			require.NoError(t, db.Create(&channel).Error)
			info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: channel.Id, ChannelType: constant.ChannelTypeOpenAI}
			info.StartTime, info.OriginModelName, info.UsingGroup = time.Now(), "gpt-6-astra", "paid"
			// No notification is eligible: a large synthetic balance and an
			// explicit low threshold, with no email or external destination.
			info.UserQuota = openingBalance
			info.UserSetting.QuotaWarningThreshold = 1
			info.UserEmail = ""

			fixture, err := os.ReadFile("../pkg/servicetier/testdata/official-prices.md")
			require.NoError(t, err)
			now := time.Now().UTC()
			catalog, err := servicetier.ParseCatalog(string(fixture), now)
			require.NoError(t, err)
			rates := catalog.Models[info.OriginModelName]
			rates.Standard.Long = nil
			catalog.Models[info.OriginModelName] = rates
			encoded, err := json.Marshal(catalog)
			require.NoError(t, err)
			require.NoError(t, setting.ValidateServiceTierOption(servicetier.CatalogOption, string(encoded)))
			catalog, err = servicetier.DecodeCatalog(string(encoded))
			require.NoError(t, err)
			policy := servicetier.DefaultPolicy()
			policy.Enabled = true
			policy.FastGroups, policy.UltrafastGroups = []string{"paid"}, []string{"paid"}
			quote, err := servicetier.NewQuote(catalog, policy, info.OriginModelName, tc.requested, "paid", 1, 1000, now)
			require.NoError(t, err)
			scale, err := common.CreditsPerUSD()
			require.NoError(t, err)
			require.Equal(t, "500000", scale.String(), "the fixed USD anchor must not change")
			info.ServiceTierQuote, info.ServiceTierCreditsPerUSD = quote, scale.String()
			reserveUSD, err := quote.ReserveUSD(tc.input)
			require.NoError(t, err)
			reserve, err := servicetier.Credits(reserveUSD, scale.String(), common.MaxQuota)
			require.NoError(t, err)
			info.PriceData = hosttypes.PriceData{QuotaToPreConsume: reserve, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}
			require.Nil(t, PreConsumeBilling(ctx, reserve, info))
			require.Equal(t, BillingSourceWallet, info.BillingSource)
			assertWalletBudgetBalances(t, db, info, openingBalance-reserve, openingBalance-reserve, reserve)

			// Only this loopback HTTP server is contacted. Observe the provider's
			// returned tier through the same method used by the actual relay.
			usage := dto.Usage{PromptTokens: tc.input, CompletionTokens: tc.output, TotalTokens: tc.input + tc.output}
			usage.PromptTokensDetails.CachedTokens = tc.cached
			usage.PromptTokensDetails.CacheWriteTokens = tc.write
			payload, err := json.Marshal(struct {
				Tier  string    `json:"service_tier"`
				Usage dto.Usage `json:"usage"`
			}{tc.actual, usage})
			require.NoError(t, err)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(payload)
			}))
			defer upstream.Close()
			response, err := upstream.Client().Get(upstream.URL)
			require.NoError(t, err)
			data, err := io.ReadAll(response.Body)
			require.NoError(t, response.Body.Close())
			require.NoError(t, err)
			info.ObserveServiceTier(data)
			var result struct {
				Usage dto.Usage `json:"usage"`
			}
			require.NoError(t, json.Unmarshal(data, &result))

			// Changing catalog pointers after admission cannot reprice this
			// request. Expected charges above are independent fixed assertions.
			snapshotRates := catalog.Models[info.OriginModelName]
			snapshotRates.Fast.Long.Input, snapshotRates.Ultrafast.Long.Input = 999, 999
			actual, err := serviceTierUsageQuota(info, &result.Usage)
			require.NoError(t, err)
			require.Equal(t, tc.want, actual)
			require.NoError(t, PostTextConsumeQuotaWithResult(ctx, info, &result.Usage, nil))
			assertWalletBudgetBalances(t, db, info, openingBalance-tc.want, openingBalance-tc.want, tc.want)
			var user model.User
			require.NoError(t, db.First(&user, info.UserId).Error)
			require.EqualValues(t, tc.want, user.UsedQuota)
			require.Equal(t, 1, user.RequestCount)
			require.NoError(t, db.First(&channel, channel.Id).Error)
			require.EqualValues(t, tc.want, channel.UsedQuota)
			var logs []model.Log
			require.NoError(t, db.Where("type = ? AND user_id = ?", model.LogTypeConsume, info.UserId).Find(&logs).Error)
			require.Len(t, logs, 1)
			require.Equal(t, tc.want, logs[0].Quota)
			var logged struct {
				Quote servicetier.Quote `json:"service_tier_pricing"`
				Scale string            `json:"service_tier_credits_per_usd"`
			}
			require.NoError(t, json.Unmarshal([]byte(logs[0].Other), &logged))
			require.Equal(t, "500000", logged.Scale)
			require.Equal(t, quote.ActualTier, logged.Quote.ActualTier)
			require.Equal(t, quote.CatalogSHA256, logged.Quote.CatalogSHA256)
			require.Equal(t, quote.ContextLimit, logged.Quote.ContextLimit)
			require.Equal(t, quote.SalesMultiplier, logged.Quote.SalesMultiplier)
			require.False(t, logged.Quote.ReconciliationRequired)
			loggedUSD, err := logged.Quote.CostUSD(servicetier.Usage{Input: tc.input, Output: tc.output, Cached: tc.cached, CacheWrite: tc.write})
			require.NoError(t, err)
			loggedQuota, err := servicetier.Credits(loggedUSD, logged.Scale, common.MaxQuota)
			require.NoError(t, err)
			require.Equal(t, tc.want, loggedQuota)

			// Repeat settlement, not the whole HTTP handler: session idempotency
			// must leave both balances and the existing consumption record intact.
			require.NoError(t, info.Billing.Settle(tc.want))
			info.Billing.Refund(ctx)
			assertWalletBudgetBalances(t, db, info, openingBalance-tc.want, openingBalance-tc.want, tc.want)
			var count int64
			require.NoError(t, db.Model(&model.Log{}).Where("type = ? AND user_id = ?", model.LogTypeConsume, info.UserId).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}
