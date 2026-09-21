package controller

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	financeExportDefaultWindow = 30 * 24 * 60 * 60
	// Keep a generous one-year upper bound while retaining a hard server-side
	// limit. The UI accepts an exact datetime range; this bound prevents an
	// accidental all-history export from exhausting the log database.
	financeExportMaxWindow = 365 * 24 * 60 * 60
	financeExportMaxRows   = 200_000
	// User exports are streamed in small primary-key batches. Unlike the
	// time-windowed ledgers, the user snapshot is intentionally complete, so
	// it has no row cap; this is the memory bound rather than a data-loss cap.
	financeExportUserBatchSize = 1_000
)

// FinanceExport is deliberately an allowlisted projection. Never add a
// password, access token, channel key, provider payload, request body, IP,
// or opaque `other` field here: this endpoint is designed for AI analysis and
// the resulting files may leave the administrator's browser.
type financeExportManifest struct {
	SchemaVersion  string          `json:"schema_version"`
	GeneratedAt    int64           `json:"generated_at"`
	StartTimestamp int64           `json:"start_timestamp"`
	EndTimestamp   int64           `json:"end_timestamp"`
	Redactions     []string        `json:"redactions"`
	Rows           map[string]int  `json:"rows"`
	Truncated      map[string]bool `json:"truncated"`
	Notes          []string        `json:"notes"`
}

type financeUserExport struct {
	ID                 int      `json:"user_id" gorm:"column:id"`
	Username           string   `json:"username" gorm:"column:username"`
	Role               int      `json:"role" gorm:"column:role"`
	Status             int      `json:"status" gorm:"column:status"`
	Group              string   `json:"group" gorm:"column:group"`
	Quota              int      `json:"quota" gorm:"column:quota"`
	UsedQuota          int      `json:"used_quota" gorm:"column:used_quota"`
	AffQuota           int      `json:"affiliate_quota" gorm:"column:aff_quota"`
	AffHistoryQuota    int      `json:"affiliate_history_quota" gorm:"column:aff_history"`
	RequestCount       int      `json:"request_count" gorm:"column:request_count"`
	CreatedAt          int64    `json:"created_at" gorm:"column:created_at"`
	LastAPIActivityAt  int64    `json:"last_api_activity_at" gorm:"column:last_api_activity_at"`
	TrustLevelOverride *int     `json:"trust_level_override,omitempty" gorm:"column:trust_level_override"`
	GroupRatio         *float64 `json:"effective_group_ratio,omitempty" gorm:"-"`
	TopupGroupRatio    *float64 `json:"effective_topup_group_ratio,omitempty" gorm:"-"`
}

type financeChannelExport struct {
	ID                 int     `json:"channel_id" gorm:"column:id"`
	Type               int     `json:"type" gorm:"column:type"`
	Status             int     `json:"status" gorm:"column:status"`
	Name               string  `json:"name" gorm:"column:name"`
	Weight             *uint   `json:"weight,omitempty" gorm:"column:weight"`
	CreatedTime        int64   `json:"created_time" gorm:"column:created_time"`
	TestTime           int64   `json:"test_time" gorm:"column:test_time"`
	ResponseTime       int     `json:"response_time" gorm:"column:response_time"`
	BaseURL            *string `json:"base_url,omitempty" gorm:"column:base_url"`
	Balance            float64 `json:"balance" gorm:"column:balance"`
	BalanceUpdatedTime int64   `json:"balance_updated_time" gorm:"column:balance_updated_time"`
	Models             string  `json:"models" gorm:"column:models"`
	ModelMapping       *string `json:"model_mapping,omitempty" gorm:"column:model_mapping"`
	Group              string  `json:"group" gorm:"column:group"`
	UsedQuota          int64   `json:"used_quota" gorm:"column:used_quota"`
	Priority           *int64  `json:"priority,omitempty" gorm:"column:priority"`
	AutoBan            *int    `json:"auto_ban,omitempty" gorm:"column:auto_ban"`
	Tag                *string `json:"tag,omitempty" gorm:"column:tag"`
}

type financePlanExport struct {
	ID                   int     `json:"plan_id" gorm:"column:id"`
	Title                string  `json:"title" gorm:"column:title"`
	Subtitle             string  `json:"subtitle" gorm:"column:subtitle"`
	PriceAmount          float64 `json:"price_amount" gorm:"column:price_amount"`
	Currency             string  `json:"currency" gorm:"column:currency"`
	DurationUnit         string  `json:"duration_unit" gorm:"column:duration_unit"`
	DurationValue        int     `json:"duration_value" gorm:"column:duration_value"`
	CustomSeconds        int64   `json:"custom_seconds" gorm:"column:custom_seconds"`
	Enabled              bool    `json:"enabled" gorm:"column:enabled"`
	SortOrder            int     `json:"sort_order" gorm:"column:sort_order"`
	AllowBalancePay      *bool   `json:"allow_balance_pay,omitempty" gorm:"column:allow_balance_pay"`
	AllowWalletOverflow  *bool   `json:"allow_wallet_overflow,omitempty" gorm:"column:allow_wallet_overflow"`
	MaxPurchasePerUser   int     `json:"max_purchase_per_user" gorm:"column:max_purchase_per_user"`
	UpgradeGroup         string  `json:"upgrade_group" gorm:"column:upgrade_group"`
	DowngradeGroup       string  `json:"downgrade_group" gorm:"column:downgrade_group"`
	TotalAmount          int64   `json:"total_amount" gorm:"column:total_amount"`
	QuotaResetPeriod     string  `json:"quota_reset_period" gorm:"column:quota_reset_period"`
	QuotaResetCustomSecs int64   `json:"quota_reset_custom_seconds" gorm:"column:quota_reset_custom_seconds"`
	CreatedAt            int64   `json:"created_at" gorm:"column:created_at"`
	UpdatedAt            int64   `json:"updated_at" gorm:"column:updated_at"`
}

type financeTopUpExport struct {
	ID                   int     `json:"topup_id" gorm:"column:id"`
	UserID               int     `json:"user_id" gorm:"column:user_id"`
	Amount               int64   `json:"amount" gorm:"column:amount"`
	CreditedQuota        int64   `json:"credited_quota" gorm:"column:credited_quota"`
	ExpectedAmountMicros int64   `json:"expected_amount_micros" gorm:"column:expected_amount_micros"`
	SettledAmountMicros  int64   `json:"settled_amount_micros" gorm:"column:settled_amount_micros"`
	SettlementCurrency   string  `json:"settlement_currency" gorm:"column:settlement_currency"`
	Money                float64 `json:"money" gorm:"column:money"`
	PaymentMethod        string  `json:"payment_method" gorm:"column:payment_method"`
	PaymentProvider      string  `json:"payment_provider" gorm:"column:payment_provider"`
	CreateTime           int64   `json:"create_time" gorm:"column:create_time"`
	CompleteTime         int64   `json:"complete_time" gorm:"column:complete_time"`
	Status               string  `json:"status" gorm:"column:status"`
}

type financeSubscriptionOrderExport struct {
	ID                        int     `json:"order_id" gorm:"column:id"`
	UserID                    int     `json:"user_id" gorm:"column:user_id"`
	PlanID                    int     `json:"plan_id" gorm:"column:plan_id"`
	Money                     float64 `json:"plan_price" gorm:"column:money"`
	PlanCurrency              string  `json:"plan_currency" gorm:"column:plan_currency"`
	ExpectedAmountMicros      int64   `json:"expected_amount_micros" gorm:"column:expected_amount_micros"`
	SettlementCurrency        string  `json:"settlement_currency" gorm:"column:settlement_currency"`
	PaymentMethod             string  `json:"payment_method" gorm:"column:payment_method"`
	PaymentProvider           string  `json:"payment_provider" gorm:"column:payment_provider"`
	ProviderProductID         string  `json:"provider_product_id" gorm:"column:provider_product_id"`
	ProviderSubscriptionState string  `json:"provider_subscription_state" gorm:"column:provider_subscription_state"`
	CurrentPeriodStart        int64   `json:"current_period_start" gorm:"column:current_period_start"`
	CurrentPeriodEnd          int64   `json:"current_period_end" gorm:"column:current_period_end"`
	RefundedAmountMicros      int64   `json:"refunded_amount_micros" gorm:"column:refunded_amount_micros"`
	Status                    string  `json:"status" gorm:"column:status"`
	CreateTime                int64   `json:"create_time" gorm:"column:create_time"`
	CompleteTime              int64   `json:"complete_time" gorm:"column:complete_time"`
}

type financeSubscriptionPaymentEventExport struct {
	ID                     int    `json:"payment_event_id" gorm:"column:id"`
	SubscriptionOrderID    int    `json:"subscription_order_id" gorm:"column:subscription_order_id"`
	PaymentProvider        string `json:"payment_provider" gorm:"column:payment_provider"`
	SettlementCurrency     string `json:"settlement_currency" gorm:"column:settlement_currency"`
	SettlementAmountMicros int64  `json:"settlement_amount_micros" gorm:"column:settlement_amount_micros"`
	PeriodStart            int64  `json:"period_start" gorm:"column:period_start"`
	PeriodEnd              int64  `json:"period_end" gorm:"column:period_end"`
	CreatedTime            int64  `json:"created_time" gorm:"column:created_time"`
}

type financeUsageExport struct {
	ID               int    `json:"log_id" gorm:"column:id"`
	UserID           int    `json:"user_id" gorm:"column:user_id"`
	CreatedAt        int64  `json:"created_at" gorm:"column:created_at"`
	Type             int    `json:"type" gorm:"column:type"`
	Username         string `json:"username" gorm:"column:username"`
	TokenName        string `json:"token_name" gorm:"column:token_name"`
	ModelName        string `json:"model_name" gorm:"column:model_name"`
	Quota            int    `json:"quota" gorm:"column:quota"`
	PromptTokens     int    `json:"prompt_tokens" gorm:"column:prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens" gorm:"column:completion_tokens"`
	UseTime          int    `json:"use_time" gorm:"column:use_time"`
	IsStream         bool   `json:"is_stream" gorm:"column:is_stream"`
	ChannelID        int    `json:"channel_id" gorm:"column:channel_id"`
	Group            string `json:"group" gorm:"column:group"`
}

type financeBountyLedgerExport struct {
	ID                 int    `json:"ledger_id" gorm:"column:id"`
	ProjectID          int    `json:"project_id" gorm:"column:project_id"`
	ChallengeID        int    `json:"challenge_id" gorm:"column:challenge_id"`
	UserID             int    `json:"user_id" gorm:"column:user_id"`
	CounterpartyUserID int    `json:"counterparty_user_id" gorm:"column:counterparty_user_id"`
	Kind               string `json:"kind" gorm:"column:kind"`
	Quota              int    `json:"quota" gorm:"column:quota"`
	Note               string `json:"note" gorm:"column:note"`
	CreatedAt          int64  `json:"created_at" gorm:"column:created_at"`
}

type financeCheckinExport struct {
	ID           int    `json:"checkin_id" gorm:"column:id"`
	UserID       int    `json:"user_id" gorm:"column:user_id"`
	CheckinDate  string `json:"checkin_date" gorm:"column:checkin_date"`
	QuotaAwarded int    `json:"quota_awarded" gorm:"column:quota_awarded"`
	CreatedAt    int64  `json:"created_at" gorm:"column:created_at"`
}

type financeRedemptionExport struct {
	ID           int    `json:"redemption_id" gorm:"column:id"`
	UserID       int    `json:"created_by_user_id" gorm:"column:user_id"`
	Status       int    `json:"status" gorm:"column:status"`
	Name         string `json:"name" gorm:"column:name"`
	Quota        int    `json:"quota" gorm:"column:quota"`
	CreatedTime  int64  `json:"created_time" gorm:"column:created_time"`
	RedeemedTime int64  `json:"redeemed_time" gorm:"column:redeemed_time"`
	UsedUserID   int    `json:"used_user_id" gorm:"column:used_user_id"`
	ExpiredTime  int64  `json:"expired_time" gorm:"column:expired_time"`
}

type financeUserSubscriptionExport struct {
	ID                  int    `json:"subscription_id" gorm:"column:id"`
	UserID              int    `json:"user_id" gorm:"column:user_id"`
	PlanID              int    `json:"plan_id" gorm:"column:plan_id"`
	AmountTotal         int64  `json:"amount_total" gorm:"column:amount_total"`
	AmountUsed          int64  `json:"amount_used" gorm:"column:amount_used"`
	StartTime           int64  `json:"start_time" gorm:"column:start_time"`
	EndTime             int64  `json:"end_time" gorm:"column:end_time"`
	Status              string `json:"status" gorm:"column:status"`
	Source              string `json:"source" gorm:"column:source"`
	LastResetTime       int64  `json:"last_reset_time" gorm:"column:last_reset_time"`
	NextResetTime       int64  `json:"next_reset_time" gorm:"column:next_reset_time"`
	UpgradeGroup        string `json:"upgrade_group" gorm:"column:upgrade_group"`
	PrevUserGroup       string `json:"previous_user_group" gorm:"column:prev_user_group"`
	DowngradeGroup      string `json:"downgrade_group" gorm:"column:downgrade_group"`
	AllowWalletOverflow bool   `json:"allow_wallet_overflow" gorm:"column:allow_wallet_overflow"`
	CreatedAt           int64  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt           int64  `json:"updated_at" gorm:"column:updated_at"`
}

type financeExportBundle struct {
	Manifest                   financeExportManifest
	Options                    map[string]string
	EffectivePricing           []model.Pricing
	Users                      []financeUserExport
	UserStream                 func(io.Writer) error
	Channels                   []financeChannelExport
	ChannelsStream             func(io.Writer) error
	Plans                      []financePlanExport
	PlansStream                func(io.Writer) error
	TopUps                     []financeTopUpExport
	TopUpsStream               func(io.Writer) error
	SubscriptionOrders         []financeSubscriptionOrderExport
	SubscriptionOrdersStream   func(io.Writer) error
	SubscriptionPaymentEvents  []financeSubscriptionPaymentEventExport
	SubscriptionPaymentsStream func(io.Writer) error
	Usage                      []financeUsageExport
	UsageStream                func(io.Writer) error
	BountyLedger               []financeBountyLedgerExport
	BountyLedgerStream         func(io.Writer) error
	Checkins                   []financeCheckinExport
	CheckinsStream             func(io.Writer) error
	Redemptions                []financeRedemptionExport
	RedemptionsStream          func(io.Writer) error
	UserSubscriptions          []financeUserSubscriptionExport
	UserSubscriptionsStream    func(io.Writer) error
}

func parseFinanceExportWindow(c *gin.Context) (int64, int64, error) {
	now := time.Now().Unix()
	start := now - financeExportDefaultWindow
	end := now
	parse := func(name string, fallback int64) (int64, error) {
		value := strings.TrimSpace(c.Query(name))
		if value == "" {
			return fallback, nil
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed <= 0 {
			return 0, fmt.Errorf("invalid %s", name)
		}
		return parsed, nil
	}
	var err error
	if start, err = parse("start_timestamp", start); err != nil {
		return 0, 0, err
	}
	if end, err = parse("end_timestamp", end); err != nil {
		return 0, 0, err
	}
	if start >= end {
		return 0, 0, fmt.Errorf("start_timestamp must be before end_timestamp")
	}
	if end-start > financeExportMaxWindow {
		return 0, 0, fmt.Errorf("export window cannot exceed %d days", financeExportMaxWindow/(24*60*60))
	}
	return start, end, nil
}

var financeExportOptionKeys = map[string]struct{}{
	"ModelPrice":                        {},
	"ModelRatio":                        {},
	"CacheRatio":                        {},
	"CreateCacheRatio":                  {},
	"CompletionRatio":                   {},
	"ImageRatio":                        {},
	"AudioRatio":                        {},
	"AudioCompletionRatio":              {},
	"GroupRatio":                        {},
	"GroupGroupRatio":                   {},
	"TopupGroupRatio":                   {},
	"UserUsableGroups":                  {},
	"QuotaPerUnit":                      {},
	"Price":                             {},
	"USDExchangeRate":                   {},
	"MinTopUp":                          {},
	"DataExportInterval":                {},
	"payment_setting.amount_options":    {},
	"payment_setting.amount_discount":   {},
	"tool_price_setting.prices":         {},
	"billing_setting.billing_mode":      {},
	"billing_setting.billing_expr":      {},
	"checkin_setting.enabled":           {},
	"checkin_setting.min_quota":         {},
	"checkin_setting.max_quota":         {},
	"checkin_setting.level_multipliers": {},
}

// countFinanceExportRows keeps the manifest useful without materializing the
// matching records in the request heap. The returned count is the number of
// rows that will actually be exported; truncated reports that the database
// contains more rows than the hard export cap.
func countFinanceExportRows(query *gorm.DB) (int, bool, error) {
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return 0, false, err
	}
	if total > int64(financeExportMaxRows) {
		return financeExportMaxRows, true, nil
	}
	return int(total), false, nil
}

// streamFinanceQueryJSON reads one database row at a time and writes it
// immediately. In particular, it must not be replaced with Find(&slice): the
// export endpoint is intentionally usable on installations with millions of
// ledger/log rows.
func streamFinanceQueryJSON[T any](writer io.Writer, query *gorm.DB) error {
	if _, err := io.WriteString(writer, "["); err != nil {
		return err
	}
	rows, err := query.Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	encoder := json.NewEncoder(writer)
	wroteRow := false
	for rows.Next() {
		var value T
		if err := query.ScanRows(rows, &value); err != nil {
			return err
		}
		if wroteRow {
			if _, err := io.WriteString(writer, ","); err != nil {
				return err
			}
		}
		if err := encoder.Encode(value); err != nil {
			return err
		}
		wroteRow = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = io.WriteString(writer, "]\n")
	return err
}

func loadFinanceExportBundle(start, end int64) (financeExportBundle, error) {
	bundle := financeExportBundle{
		Options: make(map[string]string),
		Manifest: financeExportManifest{
			SchemaVersion:  "lmm-finance-export/v1",
			GeneratedAt:    time.Now().Unix(),
			StartTimestamp: start,
			EndTimestamp:   end,
			Redactions: []string{
				"user passwords, access tokens, API keys, channel keys, redemption keys, provider payloads, IPs, request bodies, opaque log fields, channel remarks, URL credentials/query strings, trade/provider event identifiers",
			},
			Rows:      make(map[string]int),
			Truncated: make(map[string]bool),
			Notes: []string{
				"usage, top-up, subscription-order, check-in, and bounty-ledger rows are limited to the requested time window",
				"channels-pricing includes configured channel balances, model lists, and model mappings; it does not make live upstream requests",
				"redemptions exclude redemption keys and include all non-deleted codes subject to the row limit",
				"user-subscriptions contains quota entitlement snapshots; payment trade/provider payloads remain excluded",
				"the export is an analysis snapshot, not an accounting ledger of record",
			},
		},
	}
	if model.DB == nil || model.LOG_DB == nil {
		return bundle, gorm.ErrInvalidDB
	}

	options, err := model.AllOption()
	if err != nil {
		return bundle, err
	}
	for _, option := range options {
		if _, ok := financeExportOptionKeys[option.Key]; ok {
			bundle.Options[option.Key] = option.Value
		}
	}
	bundle.EffectivePricing = model.GetPricing()

	var userCount int64
	if err := model.DB.Model(&model.User{}).Count(&userCount).Error; err != nil {
		return bundle, err
	}
	bundle.UserStream = func(writer io.Writer) error {
		return streamFinanceUsersJSON(writer, bundle.Options)
	}
	channelsBase := func() *gorm.DB {
		return model.DB.Model(&model.Channel{})
	}
	channelsQuery := func() *gorm.DB {
		return channelsBase().
			Select("id", "type", "status", "name", "weight", "created_time", "test_time", "response_time", "base_url", "balance", "balance_updated_time", "models", "model_mapping", "group", "used_quota", "priority", "auto_ban", "tag").
			Order("id ASC").Limit(financeExportMaxRows)
	}
	channelCount, channelTruncated, err := countFinanceExportRows(channelsBase())
	if err != nil {
		return bundle, err
	}
	bundle.Manifest.Rows["channels"] = channelCount
	bundle.Manifest.Truncated["channels"] = channelTruncated
	bundle.ChannelsStream = func(writer io.Writer) error {
		return streamFinanceChannelsJSON(writer, channelsQuery())
	}

	plansBase := func() *gorm.DB {
		return model.DB.Model(&model.SubscriptionPlan{})
	}
	plansQuery := func() *gorm.DB {
		return plansBase().
			Select("id", "title", "subtitle", "price_amount", "currency", "duration_unit", "duration_value", "custom_seconds", "enabled", "sort_order", "allow_balance_pay", "allow_wallet_overflow", "max_purchase_per_user", "upgrade_group", "downgrade_group", "total_amount", "quota_reset_period", "quota_reset_custom_seconds", "created_at", "updated_at").
			Order("sort_order ASC, id ASC").Limit(financeExportMaxRows)
	}
	planCount, planTruncated, err := countFinanceExportRows(plansBase())
	if err != nil {
		return bundle, err
	}
	bundle.Manifest.Rows["plans"] = planCount
	bundle.Manifest.Truncated["plans"] = planTruncated
	bundle.PlansStream = func(writer io.Writer) error {
		return streamFinanceQueryJSON[financePlanExport](writer, plansQuery())
	}
	topUpsBase := func() *gorm.DB {
		return model.DB.Model(&model.TopUp{}).
			Where("create_time >= ? AND create_time <= ?", start, end)
	}
	topUpsQuery := func() *gorm.DB {
		return topUpsBase().
			Select("id", "user_id", "amount", "credited_quota", "expected_amount_micros", "settled_amount_micros", "settlement_currency", "money", "payment_method", "payment_provider", "create_time", "complete_time", "status").
			Order("create_time ASC, id ASC").Limit(financeExportMaxRows)
	}
	topUpsCount, topUpsTruncated, err := countFinanceExportRows(topUpsBase())
	if err != nil {
		return bundle, err
	}
	bundle.Manifest.Rows["topups"] = topUpsCount
	bundle.Manifest.Truncated["topups"] = topUpsTruncated
	bundle.TopUpsStream = func(writer io.Writer) error {
		return streamFinanceQueryJSON[financeTopUpExport](writer, topUpsQuery())
	}

	subscriptionOrdersBase := func() *gorm.DB {
		return model.DB.Model(&model.SubscriptionOrder{}).
			Where("create_time >= ? AND create_time <= ?", start, end)
	}
	subscriptionOrdersQuery := func() *gorm.DB {
		return subscriptionOrdersBase().
			Select("id", "user_id", "plan_id", "money", "plan_currency", "expected_amount_micros", "settlement_currency", "payment_method", "payment_provider", "provider_product_id", "provider_subscription_state", "current_period_start", "current_period_end", "refunded_amount_micros", "status", "create_time", "complete_time").
			Order("create_time ASC, id ASC").Limit(financeExportMaxRows)
	}
	subscriptionOrdersCount, subscriptionOrdersTruncated, err := countFinanceExportRows(subscriptionOrdersBase())
	if err != nil {
		return bundle, err
	}
	bundle.Manifest.Rows["subscription_orders"] = subscriptionOrdersCount
	bundle.Manifest.Truncated["subscription_orders"] = subscriptionOrdersTruncated
	bundle.SubscriptionOrdersStream = func(writer io.Writer) error {
		return streamFinanceQueryJSON[financeSubscriptionOrderExport](writer, subscriptionOrdersQuery())
	}

	subscriptionPaymentsBase := func() *gorm.DB {
		return model.DB.Model(&model.SubscriptionPaymentEvent{}).
			Where("created_time >= ? AND created_time <= ?", start, end)
	}
	subscriptionPaymentsQuery := func() *gorm.DB {
		return subscriptionPaymentsBase().
			Select("id", "subscription_order_id", "payment_provider", "settlement_currency", "settlement_amount_micros", "period_start", "period_end", "created_time").
			Order("created_time ASC, id ASC").Limit(financeExportMaxRows)
	}
	subscriptionPaymentsCount, subscriptionPaymentsTruncated, err := countFinanceExportRows(subscriptionPaymentsBase())
	if err != nil {
		return bundle, err
	}
	bundle.Manifest.Rows["subscription_payment_events"] = subscriptionPaymentsCount
	bundle.Manifest.Truncated["subscription_payment_events"] = subscriptionPaymentsTruncated
	bundle.SubscriptionPaymentsStream = func(writer io.Writer) error {
		return streamFinanceQueryJSON[financeSubscriptionPaymentEventExport](writer, subscriptionPaymentsQuery())
	}
	usageTypes := []int{
		model.LogTypeConsume,
		model.LogTypeTopup,
		model.LogTypeRefund,
		model.LogTypeSystem,
		model.LogTypeManage,
	}
	usageBase := func() *gorm.DB {
		return model.LOG_DB.Model(&model.Log{}).
			Where("created_at >= ? AND created_at <= ? AND type IN ?", start, end, usageTypes)
	}
	usageQuery := func() *gorm.DB {
		return usageBase().
			Select("id", "user_id", "created_at", "type", "username", "token_name", "model_name", "quota", "prompt_tokens", "completion_tokens", "use_time", "is_stream", "channel_id", "group").
			Order("created_at ASC, id ASC").Limit(financeExportMaxRows)
	}
	usageCount, usageTruncated, err := countFinanceExportRows(usageBase())
	if err != nil {
		return bundle, err
	}
	bundle.Manifest.Rows["usage"] = usageCount
	bundle.Manifest.Truncated["usage"] = usageTruncated
	bundle.UsageStream = func(writer io.Writer) error {
		return streamFinanceQueryJSON[financeUsageExport](writer, usageQuery())
	}

	bountyLedgerBase := func() *gorm.DB {
		return model.DB.Model(&model.OpenSourceBountyLedger{}).
			Where("created_at >= ? AND created_at <= ?", start, end)
	}
	bountyLedgerQuery := func() *gorm.DB {
		return bountyLedgerBase().
			Select("id", "project_id", "challenge_id", "user_id", "counterparty_user_id", "kind", "quota", "note", "created_at").
			Order("created_at ASC, id ASC").Limit(financeExportMaxRows)
	}
	bountyLedgerCount, bountyLedgerTruncated, err := countFinanceExportRows(bountyLedgerBase())
	if err != nil {
		return bundle, err
	}
	bundle.Manifest.Rows["bounty_ledger"] = bountyLedgerCount
	bundle.Manifest.Truncated["bounty_ledger"] = bountyLedgerTruncated
	bundle.BountyLedgerStream = func(writer io.Writer) error {
		return streamFinanceQueryJSON[financeBountyLedgerExport](writer, bountyLedgerQuery())
	}

	checkinsBase := func() *gorm.DB {
		return model.DB.Model(&model.Checkin{}).
			Where("created_at >= ? AND created_at <= ?", start, end)
	}
	checkinsQuery := func() *gorm.DB {
		return checkinsBase().
			Select("id", "user_id", "checkin_date", "quota_awarded", "created_at").
			Order("created_at ASC, id ASC").Limit(financeExportMaxRows)
	}
	checkinsCount, checkinsTruncated, err := countFinanceExportRows(checkinsBase())
	if err != nil {
		return bundle, err
	}
	bundle.Manifest.Rows["checkins"] = checkinsCount
	bundle.Manifest.Truncated["checkins"] = checkinsTruncated
	bundle.CheckinsStream = func(writer io.Writer) error {
		return streamFinanceQueryJSON[financeCheckinExport](writer, checkinsQuery())
	}

	redemptionsBase := func() *gorm.DB {
		return model.DB.Model(&model.Redemption{}).Where("deleted_at IS NULL")
	}
	redemptionsQuery := func() *gorm.DB {
		return redemptionsBase().
			Select("id", "user_id", "status", "name", "quota", "created_time", "redeemed_time", "used_user_id", "expired_time").
			Order("id ASC").Limit(financeExportMaxRows)
	}
	redemptionsCount, redemptionsTruncated, err := countFinanceExportRows(redemptionsBase())
	if err != nil {
		return bundle, err
	}
	bundle.Manifest.Rows["redemptions"] = redemptionsCount
	bundle.Manifest.Truncated["redemptions"] = redemptionsTruncated
	bundle.RedemptionsStream = func(writer io.Writer) error {
		return streamFinanceQueryJSON[financeRedemptionExport](writer, redemptionsQuery())
	}

	userSubscriptionsBase := func() *gorm.DB {
		return model.DB.Model(&model.UserSubscription{})
	}
	userSubscriptionsQuery := func() *gorm.DB {
		return userSubscriptionsBase().
			Select("id", "user_id", "plan_id", "amount_total", "amount_used", "start_time", "end_time", "status", "source", "last_reset_time", "next_reset_time", "upgrade_group", "prev_user_group", "downgrade_group", "allow_wallet_overflow", "created_at", "updated_at").
			Order("id ASC").Limit(financeExportMaxRows)
	}
	userSubscriptionsCount, userSubscriptionsTruncated, err := countFinanceExportRows(userSubscriptionsBase())
	if err != nil {
		return bundle, err
	}
	bundle.Manifest.Rows["user_subscriptions"] = userSubscriptionsCount
	bundle.Manifest.Truncated["user_subscriptions"] = userSubscriptionsTruncated
	bundle.UserSubscriptionsStream = func(writer io.Writer) error {
		return streamFinanceQueryJSON[financeUserSubscriptionExport](writer, userSubscriptionsQuery())
	}

	bundle.Manifest.Rows["options"] = len(bundle.Options)
	bundle.Manifest.Rows["effective_model_pricing"] = len(bundle.EffectivePricing)
	bundle.Manifest.Rows["users"] = int(userCount)
	bundle.Manifest.Truncated["users"] = false
	return bundle, nil
}

func decodeFinanceOption(value string) any {
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err == nil {
		return decoded
	}
	return value
}

func decodeFinanceFloatMap(value string) map[string]float64 {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var decoded map[string]float64
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return nil
	}
	return decoded
}

func sanitizeFinanceBaseURL(value *string) *string {
	if value == nil {
		return nil
	}
	parsed, err := url.Parse(strings.TrimSpace(*value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil
	}
	sanitized := parsed.Scheme + "://" + parsed.Host
	return &sanitized
}

func applyFinanceUserRatios(users []financeUserExport, options map[string]string) {
	groupRatios := decodeFinanceFloatMap(options["GroupRatio"])
	topupGroupRatios := decodeFinanceFloatMap(options["TopupGroupRatio"])
	for index := range users {
		if ratio, ok := groupRatios[users[index].Group]; ok {
			value := ratio
			users[index].GroupRatio = &value
		}
		if ratio, ok := topupGroupRatios[users[index].Group]; ok {
			value := ratio
			users[index].TopupGroupRatio = &value
		}
	}
}

// streamFinanceUsersJSON writes the complete user snapshot without retaining
// all user rows in the request heap. A million users therefore costs roughly
// one batch, not a million-row slice plus JSON copies.
func streamFinanceUsersJSON(writer io.Writer, options map[string]string) error {
	if _, err := io.WriteString(writer, "["); err != nil {
		return err
	}
	encoder := json.NewEncoder(writer)
	lastID := 0
	wroteRow := false
	for {
		rows := make([]financeUserExport, 0, financeExportUserBatchSize)
		query := model.DB.Model(&model.User{}).
			Select("id", "username", "role", "status", "group", "quota", "used_quota", "aff_quota", "aff_history", "request_count", "created_at", "last_api_activity_at", "trust_level_override").
			Where("id > ?", lastID).
			Order("id ASC").
			Limit(financeExportUserBatchSize)
		if err := query.Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		applyFinanceUserRatios(rows, options)
		for index := range rows {
			if wroteRow {
				if _, err := io.WriteString(writer, ","); err != nil {
					return err
				}
			}
			if err := encoder.Encode(rows[index]); err != nil {
				return err
			}
			wroteRow = true
		}
		lastID = rows[len(rows)-1].ID
		if len(rows) < financeExportUserBatchSize {
			break
		}
	}
	_, err := io.WriteString(writer, "]\n")
	return err
}

// streamFinanceChannelsJSON keeps channel credentials out of the heap while
// preserving the same URL redaction used by the former slice export.
func streamFinanceChannelsJSON(writer io.Writer, query *gorm.DB) error {
	if _, err := io.WriteString(writer, "["); err != nil {
		return err
	}
	rows, err := query.Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	encoder := json.NewEncoder(writer)
	wroteRow := false
	for rows.Next() {
		var value financeChannelExport
		if err := query.ScanRows(rows, &value); err != nil {
			return err
		}
		value.BaseURL = sanitizeFinanceBaseURL(value.BaseURL)
		if wroteRow {
			if _, err := io.WriteString(writer, ","); err != nil {
				return err
			}
		}
		if err := encoder.Encode(value); err != nil {
			return err
		}
		wroteRow = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = io.WriteString(writer, "]\n")
	return err
}

type financeDocument struct {
	Name  string
	Value any
	Write func(io.Writer) error
}

func financeDocuments(bundle financeExportBundle) []financeDocument {
	return []financeDocument{
		{Name: "manifest.json", Value: bundle.Manifest},
		{Name: "financial-options.json", Value: bundle.Options},
		{Name: "model-prices-and-ratios.json", Value: map[string]any{
			"model_prices":            decodeFinanceOption(bundle.Options["ModelPrice"]),
			"model_ratios":            decodeFinanceOption(bundle.Options["ModelRatio"]),
			"completion_ratios":       decodeFinanceOption(bundle.Options["CompletionRatio"]),
			"cache_ratios":            decodeFinanceOption(bundle.Options["CacheRatio"]),
			"create_cache_ratios":     decodeFinanceOption(bundle.Options["CreateCacheRatio"]),
			"image_ratios":            decodeFinanceOption(bundle.Options["ImageRatio"]),
			"audio_ratios":            decodeFinanceOption(bundle.Options["AudioRatio"]),
			"audio_completion_ratios": decodeFinanceOption(bundle.Options["AudioCompletionRatio"]),
			"tool_prices":             decodeFinanceOption(bundle.Options["tool_price_setting.prices"]),
			"billing_modes":           decodeFinanceOption(bundle.Options["billing_setting.billing_mode"]),
			"billing_expressions":     decodeFinanceOption(bundle.Options["billing_setting.billing_expr"]),
		}},
		{Name: "effective-model-pricing.json", Value: bundle.EffectivePricing},
		{Name: "users-balances.json", Value: bundle.Users, Write: bundle.UserStream},
		{Name: "channels-pricing.json", Value: bundle.Channels, Write: bundle.ChannelsStream},
		{Name: "subscription-plans.json", Value: bundle.Plans, Write: bundle.PlansStream},
		{Name: "topups.json", Value: bundle.TopUps, Write: bundle.TopUpsStream},
		{Name: "subscription-orders.json", Value: bundle.SubscriptionOrders, Write: bundle.SubscriptionOrdersStream},
		{Name: "subscription-payment-events.json", Value: bundle.SubscriptionPaymentEvents, Write: bundle.SubscriptionPaymentsStream},
		{Name: "usage-billing-records.json", Value: bundle.Usage, Write: bundle.UsageStream},
		{Name: "bounty-ledger.json", Value: bundle.BountyLedger, Write: bundle.BountyLedgerStream},
		{Name: "checkins.json", Value: bundle.Checkins, Write: bundle.CheckinsStream},
		{Name: "redemptions.json", Value: bundle.Redemptions, Write: bundle.RedemptionsStream},
		{Name: "user-subscriptions.json", Value: bundle.UserSubscriptions, Write: bundle.UserSubscriptionsStream},
	}
}

func (document financeDocument) write(writer io.Writer) error {
	if document.Write != nil {
		return document.Write(writer)
	}
	return writeFinanceJSON(writer, document.Value)
}

func writeFinanceJSON(writer io.Writer, value any) error {
	reflected := reflect.ValueOf(value)
	if reflected.IsValid() && (reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Array) &&
		!(reflected.Kind() == reflect.Slice && reflected.Type().Elem().Kind() == reflect.Uint8) {
		if reflected.Kind() == reflect.Slice && reflected.IsNil() {
			_, err := io.WriteString(writer, "null\n")
			return err
		}
		return writeFinanceArray(writer, reflected)
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

// writeFinanceArray streams one row at a time. encoding/json otherwise builds
// the full slice in memory before its first write, which can duplicate a large
// usage export inside the API process.
func writeFinanceArray(writer io.Writer, rows reflect.Value) error {
	if _, err := io.WriteString(writer, "["); err != nil {
		return err
	}
	encoder := json.NewEncoder(writer)
	for index := 0; index < rows.Len(); index++ {
		separator := "\n"
		if index > 0 {
			separator = ",\n"
		}
		if _, err := io.WriteString(writer, separator); err != nil {
			return err
		}
		if err := encoder.Encode(rows.Index(index).Interface()); err != nil {
			return err
		}
	}
	_, err := io.WriteString(writer, "]\n")
	return err
}

func writeFinanceText(c *gin.Context, documents []financeDocument) error {
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Status(http.StatusOK)
	if _, err := io.WriteString(c.Writer, "LMM Finance Analysis Export\n========================================\n\n"); err != nil {
		return err
	}
	for _, document := range documents {
		if _, err := io.WriteString(c.Writer, "## "+document.Name+"\n"); err != nil {
			return err
		}
		if err := document.write(c.Writer); err != nil {
			return err
		}
		if _, err := io.WriteString(c.Writer, "\n"); err != nil {
			return err
		}
	}
	return nil
}

func writeFinanceZip(c *gin.Context, documents []financeDocument) error {
	filename := fmt.Sprintf("lmm-finance-export-%s.zip", time.Now().UTC().Format("20060102-150405"))
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	writer := zip.NewWriter(c.Writer)
	for _, document := range documents {
		entry, err := writer.Create(document.Name)
		if err != nil {
			_ = writer.Close()
			return err
		}
		if err := document.write(entry); err != nil {
			_ = writer.Close()
			return err
		}
	}
	return writer.Close()
}

// ExportFinancialData creates a redacted, admin-only analysis snapshot. The
// response is either a ZIP (default) or a plain-text bundle for clipboard use.
func ExportFinancialData(c *gin.Context) {
	format := strings.ToLower(strings.TrimSpace(c.DefaultQuery("format", "zip")))
	if format != "zip" && format != "text" {
		common.ApiErrorMsg(c, "format must be zip or text")
		return
	}
	start, end, err := parseFinanceExportWindow(c)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	bundle, err := loadFinanceExportBundle(start, end)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	documents := financeDocuments(bundle)
	model.RecordOperationAuditLog(
		c.GetInt("id"),
		"Financial analysis export generated",
		c.ClientIP(),
		"finance.export",
		map[string]interface{}{
			"format":          format,
			"start_timestamp": start,
			"end_timestamp":   end,
			"rows":            bundle.Manifest.Rows,
		},
		nil,
		nil,
	)
	if format == "text" {
		_ = writeFinanceText(c, documents)
		return
	}
	if err := writeFinanceZip(c, documents); err != nil {
		// Headers may already be committed; only log the failure instead of
		// attempting to write a second JSON response.
		return
	}
}
