package service

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/decisions"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	hosttypes "github.com/LIghtJUNction/api.lmm.best/types"
	"github.com/stretchr/testify/require"
)

func TestDecisionsActualInputSettlesWalletSubscriptionAndToken(t *testing.T) {
	for _, tc := range []struct {
		name, preference, host string
		input, want, wallet    int
		sub                    int64
		group                  float64
	}{
		{"wallet standard", "wallet_only", "api.openai.com", 272000, 13600, 13600, 0, 1},
		{"wallet long", "wallet_only", "api.openai.com", 272001, 27200, 27200, 0, 1},
		{"regional long split", "subscription_first", "eu.api.openai.com", 272001, 29920, 28920, 1000, 1},
		{"group discount", "wallet_only", "us.api.openai.com", 272000, 7480, 7480, 0, 0.5},
		{"authoritative zero", "wallet_only", "api.openai.com", 0, 0, 0, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, info, ctx := subscriptionBillingFixture(t, 1000, true, tc.preference)
			enableNativeVoiceTestLogs(t, db, info)
			info.StartTime, info.OriginModelName, info.RelayFormat = time.Now(), "gpt-6-luna", types.RelayFormatOpenAIDecisions
			info.PriceData = hosttypes.PriceData{GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: tc.group}}
			expression := `(len > 272000 ? tier("long", p * 0.2) : tier("standard", p * 0.1)) * ((header("x-lmm-billing-upstream-host") == "eu.api.openai.com" || header("x-lmm-billing-upstream-host") == "us.api.openai.com") ? 1.1 : 1)`
			info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ModelName: "gpt-6-luna@decisions", ExprString: expression, ExprHash: billingexpr.ExprHashString(expression), GroupRatio: tc.group, QuotaPerUnit: 500000, ExprVersion: 1, EstimatedPromptTokens: 1000, EstimatedQuotaBeforeGroup: 50, EstimatedQuotaAfterGroup: 50}
			info.BillingRequestInput = &billingexpr.RequestInput{Headers: map[string]string{"x-lmm-billing-upstream-host": tc.host}}
			session, apiErr := NewBillingSession(ctx, info, 50)
			require.Nil(t, apiErr)
			info.Billing, info.FinalPreConsumedQuota = session, session.GetPreConsumedQuota()
			// Native usage details remain in the response and cannot add chat
			// cache-write/output charges to the validated inclusive input total.
			var request decisions.Request
			require.NoError(t, json.Unmarshal([]byte(`{"model":"gpt-6-luna","input":"x","questions":[{"type":"predicate","name":"x","instructions":"True?"}]}`), &request))
			raw := fmt.Sprintf(`{"model":"gpt-6-luna","answers":[{"type":"predicate","name":"x","probability":0.75}],"usage":{"input_tokens":%d,"input_tokens_details":{"cached_tokens":100,"cache_write_tokens":50},"output_tokens":1000}}`, tc.input)
			input, err := decisions.InputUsage([]byte(raw), request.Questions)
			require.NoError(t, err)
			info.ResponsesUsageReported = true
			require.NoError(t, PostTextConsumeQuotaWithResult(ctx, info, &dto.Usage{PromptTokens: input, TotalTokens: input}, nil))
			// Repeated lifecycle finalization and a late refund cannot double debit.
			require.NoError(t, session.Settle(tc.want))
			session.Refund(ctx)
			assertNativeVoiceBalances(t, db, info, tc.sub, tc.wallet, tc.want)
			log, other := readNativeVoiceTestLog(t, db)
			require.Equal(t, tc.want, log.Quota)
			require.Equal(t, tc.input, log.PromptTokens)
			require.Zero(t, log.CompletionTokens)
			require.Equal(t, "gpt-6-luna@decisions", other["billing_price_key"])
			var key model.Token
			require.NoError(t, db.First(&key, info.TokenId).Error)
			require.Equal(t, tc.want, key.UsedQuota)
		})
	}
}
