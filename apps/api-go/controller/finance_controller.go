package controller

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	financeDashboardDefaultWindow = 30 * 24 * 60 * 60
	financeDashboardMaxWindow     = 366 * 24 * 60 * 60
	financeDashboardMaxSourceRows = 100_000
	// Finance dashboards are read-only, but a busy installation can still
	// have hundreds of thousands of source rows in one window. Keep each
	// database round-trip small so the accumulator never retains raw source
	// rows after a batch has been processed.
	financeDashboardBatchSize  = 1_000
	financeDashboardMaxEntries = 100
	// User-level finance details are a bounded top-user view, not a complete
	// export. Keep the accumulator from retaining one object per source row on
	// large installations; totals continue to be updated independently.
	financeDashboardMaxUserMetrics     = 100_000
	financeDashboardMaxMethodUserPairs = 200_000
	// Payment methods are admin-visible metadata, not finance source rows. Keep
	// discovery bounded and fail closed instead of returning a partial selector
	// when malformed or unbounded historical values are present.
	financeDashboardMaxPaymentMethods = 256
)

var errFinancePaymentMethodsLimit = errors.New("finance payment method discovery limit exceeded")

type financeRange struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type financeMethodMetric struct {
	Method       string `json:"method"`
	Provider     string `json:"provider"`
	Category     string `json:"category,omitempty"`
	AmountMicros int64  `json:"amount_micros"`
	Orders       int64  `json:"orders"`
	Users        int64  `json:"users"`
	TokenUnits   int64  `json:"token_units"`
}

type financeCurrencyMetric struct {
	Currency     string `json:"currency"`
	AmountMicros int64  `json:"amount_micros"`
	Orders       int64  `json:"orders"`
}

type financeDailyMetric struct {
	Date          string `json:"date"`
	RevenueMicros int64  `json:"revenue_micros"`
	RefundMicros  int64  `json:"refund_micros"`
	ExpenseMicros int64  `json:"expense_micros"`
	ProfitMicros  int64  `json:"profit_micros"`
	TokenUnits    int64  `json:"token_units"`
	Requests      int64  `json:"requests"`
}

type financeUserMetric struct {
	UserID          int    `json:"user_id"`
	Username        string `json:"username,omitempty"`
	DisplayName     string `json:"display_name,omitempty"`
	RevenueMicros   int64  `json:"revenue_micros"`
	RefundMicros    int64  `json:"refund_micros"`
	ExpenseMicros   int64  `json:"expense_micros"`
	TokenCostMicros int64  `json:"token_cost_micros"`
	TokenUnits      int64  `json:"token_units"`
	Requests        int64  `json:"requests"`
}

type financeTokenMetric struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	TotalTokens         int64 `json:"total_tokens"`
	Requests            int64 `json:"requests"`
	EstimatedCostMicros int64 `json:"estimated_cost_micros"`
	UnpricedRequests    int64 `json:"unpriced_requests"`
}

type financeOverview struct {
	Range            financeRange `json:"range"`
	Currency         string       `json:"currency"`
	RevenueMicros    int64        `json:"revenue_micros"`
	RefundMicros     int64        `json:"refund_micros"`
	NetRevenueMicros int64        `json:"net_revenue_micros"`
	ExpenseMicros    int64        `json:"expense_micros"`
	ProfitMicros     int64        `json:"profit_micros"`
	// SettlementRevenueByCurrency is the native-fiat view. RevenueMicros and
	// profit remain USD-only; no exchange rate is guessed in the report layer.
	SettlementRevenueByCurrency []financeCurrencyMetric `json:"settlement_revenue_by_currency"`
	// Missing/invalid ISO currency, missing settlement micros, and wallet-funded
	// platform amounts are deliberately counted but never folded into fiat.
	UnclassifiedSettlementOrders int64 `json:"unclassified_settlement_orders"`
	// Token usage is recorded independently from payment receipts. Once a
	// payment-method filter is applied, historical usage cannot be attributed
	// to that method unless the request log carries an explicit source, so the
	// dashboard must not silently fold all usage cost into one method's profit.
	CostAttribution string                       `json:"cost_attribution"`
	RevenueByMethod []financeMethodMetric        `json:"revenue_by_method"`
	RefundByMethod  []financeMethodMetric        `json:"refund_by_method"`
	ExpenseByMethod []financeMethodMetric        `json:"expense_by_method"`
	Tokens          financeTokenMetric           `json:"tokens"`
	Daily           []financeDailyMetric         `json:"daily"`
	Users           []financeUserMetric          `json:"users"`
	PaymentMethods  []model.FinancePaymentMethod `json:"payment_methods"`
	SourcesBounded  bool                         `json:"sources_bounded"`
	// Users is an intentionally bounded ranking. Keep this explicit so an
	// administrator does not mistake the first page for a complete user list.
	UserMetricsTruncated      bool `json:"user_metrics_truncated"`
	UserMetricsComplete       bool `json:"user_metrics_complete"`
	UserMetricsLimit          int  `json:"user_metrics_limit"`
	MethodUserMetricsComplete bool `json:"method_user_metrics_complete"`
	MethodUserMetricsLimit    int  `json:"method_user_metrics_limit"`
}

type financeAccumulator struct {
	overview          financeOverview
	start             int64
	end               int64
	methods           map[string]*financeMethodMetric
	refunds           map[string]*financeMethodMetric
	expenses          map[string]*financeMethodMetric
	settlementRevenue map[string]*financeCurrencyMetric
	daily             map[string]*financeDailyMetric
	users             map[int]*financeUserMetric
	methodUsers       map[string]map[int]struct{}
	methodUserPairs   int
}

type financeSubscriptionPaymentEventRow struct {
	Id                     int
	UserId                 int
	PaymentMethod          string
	PaymentProvider        string
	SettlementCurrency     string
	SettlementAmountMicros int64
	CreatedTime            int64
}

func newFinanceAccumulator(start, end int64, paymentMethods []model.FinancePaymentMethod) *financeAccumulator {
	return &financeAccumulator{
		overview: financeOverview{
			Range:                     financeRange{Start: start, End: end},
			Currency:                  model.FinanceCurrencyUSD,
			CostAttribution:           "complete",
			PaymentMethods:            paymentMethods,
			SourcesBounded:            true,
			UserMetricsComplete:       true,
			UserMetricsLimit:          financeDashboardMaxUserMetrics,
			MethodUserMetricsComplete: true,
			MethodUserMetricsLimit:    financeDashboardMaxMethodUserPairs,
		},
		start: start, end: end,
		methods: make(map[string]*financeMethodMetric), refunds: make(map[string]*financeMethodMetric), expenses: make(map[string]*financeMethodMetric), settlementRevenue: make(map[string]*financeCurrencyMetric), daily: make(map[string]*financeDailyMetric), users: make(map[int]*financeUserMetric), methodUsers: make(map[string]map[int]struct{}),
	}
}

func (a *financeAccumulator) dailyMetric(timestamp int64) *financeDailyMetric {
	key := time.Unix(timestamp, 0).UTC().Format("2006-01-02")
	metric := a.daily[key]
	if metric == nil {
		metric = &financeDailyMetric{Date: key}
		a.daily[key] = metric
	}
	return metric
}

func (a *financeAccumulator) userMetric(userID int) *financeUserMetric {
	if userID <= 0 {
		return nil
	}
	metric := a.users[userID]
	if metric == nil {
		if len(a.users) >= financeDashboardMaxUserMetrics {
			a.overview.UserMetricsComplete = false
			return nil
		}
		metric = &financeUserMetric{UserID: userID}
		a.users[userID] = metric
	}
	return metric
}

func (a *financeAccumulator) addMethodUser(key string, userID int) {
	if userID <= 0 {
		return
	}
	users := a.methodUsers[key]
	if users != nil {
		if _, exists := users[userID]; exists {
			return
		}
	}
	if a.methodUserPairs >= financeDashboardMaxMethodUserPairs {
		a.overview.MethodUserMetricsComplete = false
		return
	}
	if users == nil {
		users = make(map[int]struct{})
		a.methodUsers[key] = users
	}
	users[userID] = struct{}{}
	a.methodUserPairs++
}

func (a *financeAccumulator) addRevenue(method, provider string, amount, timestamp int64, userID int) {
	if amount <= 0 {
		return
	}
	key := strings.TrimSpace(method) + "\x00" + strings.TrimSpace(provider)
	metric := a.methods[key]
	if metric == nil {
		metric = &financeMethodMetric{Method: strings.TrimSpace(method), Provider: strings.TrimSpace(provider)}
		a.methods[key] = metric
	}
	metric.AmountMicros += amount
	metric.Orders++
	if userID > 0 {
		a.addMethodUser(key, userID)
		if user := a.userMetric(userID); user != nil {
			user.RevenueMicros += amount
		}
	}
	a.overview.RevenueMicros += amount
	a.dailyMetric(timestamp).RevenueMicros += amount
}

func (a *financeAccumulator) addSettlementRevenue(currency, method, provider string, amount, timestamp int64, userID int) {
	currency, validCurrency := financeNormalizeSettlementCurrency(currency)
	if !validCurrency || amount <= 0 {
		a.overview.UnclassifiedSettlementOrders++
		return
	}
	metric := a.settlementRevenue[currency]
	if metric == nil {
		metric = &financeCurrencyMetric{Currency: currency}
		a.settlementRevenue[currency] = metric
	}
	metric.AmountMicros += amount
	metric.Orders++
	if currency == model.FinanceCurrencyUSD {
		a.addRevenue(method, provider, amount, timestamp, userID)
	}
}

func (a *financeAccumulator) markUnclassifiedSettlement() {
	a.overview.UnclassifiedSettlementOrders++
}

func (a *financeAccumulator) addExpense(category, method, provider string, amount, timestamp int64, userID int) {
	a.addExpenseDelta(category, method, provider, amount, timestamp, userID)
}

// addExpenseDelta applies a signed ledger delta. Normal expense entries are
// positive, while a credit-direction reversal must remove the original cost
// from the dashboard. Keeping the signed update in one helper makes every
// aggregate (method, user, daily, and total) follow the ledger direction.
func (a *financeAccumulator) addExpenseDelta(category, method, provider string, amount, timestamp int64, userID int) {
	if amount == 0 {
		return
	}
	key := strings.TrimSpace(category) + "\x00" + strings.TrimSpace(method) + "\x00" + strings.TrimSpace(provider)
	metric := a.expenses[key]
	if metric == nil {
		metric = &financeMethodMetric{Method: strings.TrimSpace(method), Provider: strings.TrimSpace(provider)}
		a.expenses[key] = metric
	}
	metric.Category = strings.TrimSpace(category)
	metric.AmountMicros += amount
	if userID > 0 {
		if user := a.userMetric(userID); user != nil {
			user.ExpenseMicros += amount
		}
	}
	a.overview.ExpenseMicros += amount
	a.dailyMetric(timestamp).ExpenseMicros += amount
}

func (a *financeAccumulator) addRefund(method, provider string, amount, timestamp int64, userID int) {
	if amount <= 0 {
		return
	}
	key := strings.TrimSpace(method) + "\x00" + strings.TrimSpace(provider)
	metric := a.refunds[key]
	if metric == nil {
		metric = &financeMethodMetric{Method: strings.TrimSpace(method), Provider: strings.TrimSpace(provider), Category: model.FinanceSourceRefund}
		a.refunds[key] = metric
	}
	metric.AmountMicros += amount
	metric.Orders++
	a.overview.RefundMicros += amount
	a.dailyMetric(timestamp).RefundMicros += amount
	if userID > 0 {
		if user := a.userMetric(userID); user != nil {
			user.RefundMicros += amount
		}
	}
}

func (a *financeAccumulator) addUsage(userID int, timestamp int64, prompt, completion int, estimatedCost int64, priced bool) {
	if prompt < 0 {
		prompt = 0
	}
	if completion < 0 {
		completion = 0
	}
	total := int64(prompt + completion)
	a.overview.Tokens.PromptTokens += int64(prompt)
	a.overview.Tokens.CompletionTokens += int64(completion)
	a.overview.Tokens.TotalTokens += total
	a.overview.Tokens.Requests++
	a.overview.Tokens.EstimatedCostMicros += estimatedCost
	if !priced {
		a.overview.Tokens.UnpricedRequests++
	}
	daily := a.dailyMetric(timestamp)
	daily.TokenUnits += total
	daily.Requests++
	if user := a.userMetric(userID); user != nil {
		user.TokenUnits += total
		user.Requests++
		user.TokenCostMicros += estimatedCost
	}
	if estimatedCost > 0 {
		a.addExpense("token_cost", "", "", estimatedCost, timestamp, userID)
	}
}

func (a *financeAccumulator) finish() financeOverview {
	for key, metric := range a.methods {
		metric.Users = int64(len(a.methodUsers[key]))
		a.overview.RevenueByMethod = append(a.overview.RevenueByMethod, *metric)
	}
	for _, metric := range a.expenses {
		a.overview.ExpenseByMethod = append(a.overview.ExpenseByMethod, *metric)
	}
	for _, metric := range a.refunds {
		a.overview.RefundByMethod = append(a.overview.RefundByMethod, *metric)
	}
	for _, metric := range a.settlementRevenue {
		a.overview.SettlementRevenueByCurrency = append(a.overview.SettlementRevenueByCurrency, *metric)
	}
	// Keep a stable daily series even when a window has no transactions. This
	// lets the chart communicate "zero activity" instead of rendering an empty
	// plot with no dates.
	day := time.Unix(a.start, 0).UTC()
	day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	for ts := day.Unix(); ts < a.end; ts += 24 * 60 * 60 {
		a.dailyMetric(ts)
	}
	for _, metric := range a.daily {
		metric.ProfitMicros = metric.RevenueMicros - metric.RefundMicros - metric.ExpenseMicros
		a.overview.Daily = append(a.overview.Daily, *metric)
	}
	for _, metric := range a.users {
		a.overview.Users = append(a.overview.Users, *metric)
	}
	sort.Slice(a.overview.RevenueByMethod, func(i, j int) bool {
		return a.overview.RevenueByMethod[i].AmountMicros > a.overview.RevenueByMethod[j].AmountMicros
	})
	sort.Slice(a.overview.ExpenseByMethod, func(i, j int) bool {
		return a.overview.ExpenseByMethod[i].AmountMicros > a.overview.ExpenseByMethod[j].AmountMicros
	})
	sort.Slice(a.overview.RefundByMethod, func(i, j int) bool {
		return a.overview.RefundByMethod[i].AmountMicros > a.overview.RefundByMethod[j].AmountMicros
	})
	sort.Slice(a.overview.SettlementRevenueByCurrency, func(i, j int) bool {
		return a.overview.SettlementRevenueByCurrency[i].Currency < a.overview.SettlementRevenueByCurrency[j].Currency
	})
	sort.Slice(a.overview.Daily, func(i, j int) bool { return a.overview.Daily[i].Date < a.overview.Daily[j].Date })
	sort.Slice(a.overview.Users, func(i, j int) bool {
		left := a.overview.Users[i]
		right := a.overview.Users[j]
		leftActivity := left.ExpenseMicros + left.RevenueMicros + left.RefundMicros
		rightActivity := right.ExpenseMicros + right.RevenueMicros + right.RefundMicros
		if leftActivity != rightActivity {
			return leftActivity > rightActivity
		}
		// Map iteration order is intentionally unspecified. Use the stable user
		// id as a tie-breaker so the bounded top-N view does not flicker between
		// requests when users have the same financial activity.
		return left.UserID < right.UserID
	})
	if len(a.overview.Users) > financeDashboardMaxEntries {
		a.overview.UserMetricsTruncated = true
		a.overview.Users = a.overview.Users[:financeDashboardMaxEntries]
	}
	a.overview.NetRevenueMicros = a.overview.RevenueMicros - a.overview.RefundMicros
	a.overview.ProfitMicros = a.overview.NetRevenueMicros - a.overview.ExpenseMicros
	return a.overview
}

// attachFinanceUserLabels enriches the bounded aggregates without retaining
// full User rows while payment and usage sources are scanned.
func attachFinanceUserLabels(view *financeOverview) error {
	if view == nil || len(view.Users) == 0 {
		return nil
	}
	ids := make([]int, 0, len(view.Users))
	for _, metric := range view.Users {
		ids = append(ids, metric.UserID)
	}
	type userLabel struct {
		ID          int    `gorm:"column:id"`
		Username    string `gorm:"column:username"`
		DisplayName string `gorm:"column:display_name"`
	}
	labels := make([]userLabel, 0, len(ids))
	if err := model.DB.Model(&model.User{}).
		Select("id, username, display_name").
		Where("id IN ?", ids).
		Find(&labels).Error; err != nil {
		return err
	}
	byID := make(map[int]userLabel, len(labels))
	for _, label := range labels {
		byID[label.ID] = label
	}
	for index := range view.Users {
		if label, ok := byID[view.Users[index].UserID]; ok {
			view.Users[index].Username = label.Username
			view.Users[index].DisplayName = label.DisplayName
		}
	}
	return nil
}

func parseFinanceDashboardRange(c *gin.Context) (int64, int64, error) {
	now := time.Now().Unix()
	start, end := now-financeDashboardDefaultWindow, now
	for name, target := range map[string]*int64{"start_timestamp": &start, "end_timestamp": &end} {
		value := strings.TrimSpace(c.Query(name))
		if value == "" {
			continue
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed <= 0 {
			return 0, 0, fmt.Errorf("invalid %s", name)
		}
		*target = parsed
	}
	if start >= end || end-start > financeDashboardMaxWindow {
		return 0, 0, errors.New("invalid finance dashboard range")
	}
	return start, end, nil
}

func financeMethodFromTopUp(topUp model.TopUp) (string, string) {
	return financeNormalizePaymentSource(topUp.PaymentMethod, topUp.PaymentProvider)
}

func financeNormalizePaymentSource(methodValue, providerValue string) (string, string) {
	method, provider := strings.TrimSpace(methodValue), strings.TrimSpace(providerValue)
	if method == "" {
		method = provider
	}
	if provider == "" {
		provider = method
	}
	return method, provider
}

func financeMicrosFromFloat(value float64) int64 {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return int64(math.Round(value * 1_000_000))
}

func financeUsageIsCountable(other map[string]interface{}) bool {
	if other == nil {
		return true
	}
	source, _ := other["billing_source"].(string)
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "gift", "bonus", "checkin", "invite", "bounty", "linuxdo", "linux_do", "linuxdo_credit", "internal":
		return false
	default:
		return true
	}
}

func financeTopUpAmount(topUp model.TopUp) int64 {
	if topUp.SettledAmountMicros > 0 {
		return topUp.SettledAmountMicros
	}
	return topUp.ExpectedAmountMicros
}

func financeNormalizeSettlementCurrency(currency string) (string, bool) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if len(currency) != 3 {
		return "", false
	}
	for index := range len(currency) {
		if currency[index] < 'A' || currency[index] > 'Z' {
			return "", false
		}
	}
	return currency, true
}

func financePlatformPaymentSource(method, provider string) bool {
	return strings.EqualFold(strings.TrimSpace(method), model.PaymentMethodBalance) ||
		strings.EqualFold(strings.TrimSpace(provider), model.PaymentProviderBalance)
}

func financePaymentMethodAllowed(method, provider string, configs map[string]model.FinancePaymentMethod) bool {
	if !model.IsFinancialPaymentSource(method, provider) {
		return false
	}
	config, ok := configs[method]
	if !ok {
		config, ok = configs[provider]
	}
	return !ok || (config.Enabled && config.IncludeRevenue)
}

func loadFinancePaymentMethods() ([]model.FinancePaymentMethod, map[string]model.FinancePaymentMethod, error) {
	var configs []model.FinancePaymentMethod
	if err := model.DB.Order("method asc").Find(&configs).Error; err != nil {
		return nil, nil, err
	}
	seen := make(map[string]bool, len(configs))
	byMethod := make(map[string]model.FinancePaymentMethod, len(configs))
	for _, config := range configs {
		seen[config.Method] = true
		byMethod[config.Method] = config
	}
	known := []string{model.PaymentProviderStripe, model.PaymentProviderCreem, model.PaymentProviderEpay, model.PaymentProviderWaffo, model.PaymentProviderWaffoPancake}
	knownSet := make(map[string]struct{}, len(known))
	for _, method := range known {
		knownSet[method] = struct{}{}
	}
	observedSet := make(map[string]struct{}, financeDashboardMaxPaymentMethods)
	// Discover methods from every financial source. Payment methods are not
	// guaranteed to be stored in TopUp: subscription orders and manually
	// imported/refund ledger rows may be the only evidence of a provider, and
	// older integrations sometimes populated only payment_provider. The
	// normalized method follows the same method-then-provider rule used by the
	// revenue accumulator, so the selector cannot silently omit a real source.
	// Keep discovery server-side DISTINCT: loading every historical payment row
	// just to find a handful of method names would defeat the dashboard's memory
	// bound.
	appendObserved := func(source string, query *gorm.DB) error {
		const normalizedMethod = "COALESCE(NULLIF(TRIM(payment_method), ''), NULLIF(TRIM(payment_provider), ''))"
		observed := make([]string, 0, financeDashboardMaxPaymentMethods+1)
		if err := query.
			Where("payment_method <> '' OR payment_provider <> ''").
			Distinct(normalizedMethod).
			Order(normalizedMethod+" ASC").
			Limit(financeDashboardMaxPaymentMethods+1).
			Pluck(normalizedMethod, &observed).Error; err != nil {
			return err
		}
		if len(observed) > financeDashboardMaxPaymentMethods {
			return fmt.Errorf("%w: source=%s limit=%d", errFinancePaymentMethodsLimit, source, financeDashboardMaxPaymentMethods)
		}
		for _, method := range observed {
			method = strings.TrimSpace(method)
			if method != "" {
				if _, exists := observedSet[method]; !exists {
					if len(observedSet) >= financeDashboardMaxPaymentMethods {
						return fmt.Errorf("%w: global limit=%d", errFinancePaymentMethodsLimit, financeDashboardMaxPaymentMethods)
					}
					observedSet[method] = struct{}{}
				}
				if _, exists := knownSet[method]; exists {
					continue
				}
				known = append(known, method)
				knownSet[method] = struct{}{}
			}
		}
		return nil
	}
	if err := appendObserved("top_ups", model.DB.Model(&model.TopUp{}).Select("payment_method, payment_provider")); err != nil {
		return nil, nil, err
	}
	if err := appendObserved("subscription_orders", model.DB.Model(&model.SubscriptionOrder{}).Select("payment_method, payment_provider")); err != nil {
		return nil, nil, err
	}
	if err := appendObserved("finance_ledger_entries", model.DB.Model(&model.FinanceLedgerEntry{}).Select("payment_method, payment_provider")); err != nil {
		return nil, nil, err
	}
	for _, method := range known {
		method = strings.TrimSpace(method)
		if method == "" || seen[method] || method == model.PaymentProviderBalance {
			continue
		}
		config := model.FinancePaymentMethod{Method: method, Label: method, Enabled: true, IncludeRevenue: true}
		configs = append(configs, config)
		byMethod[method] = config
		seen[method] = true
	}
	sort.Slice(configs, func(i, j int) bool { return configs[i].Method < configs[j].Method })
	return configs, byMethod, nil
}

func financeBatchLimit(processed int) int {
	remaining := financeDashboardMaxSourceRows - processed
	if remaining <= 0 {
		return 0
	}
	if remaining < financeDashboardBatchSize {
		return remaining
	}
	return financeDashboardBatchSize
}

// iterateFinanceSource reads a source ordered by its timestamp and numeric primary
// key. Offset pagination becomes increasingly expensive for large windows and
// can repeat/skip rows when a new payment or log arrives while the dashboard
// is loading. The composite cursor keeps every batch bounded and stable.
func iterateFinanceSource[T any](base *gorm.DB, timestampColumn string, visit func(T) error) error {
	if base == nil || visit == nil {
		return gorm.ErrInvalidData
	}
	processed := 0
	var lastTimestamp int64
	var lastID int64
	for {
		limit := financeBatchLimit(processed)
		if limit == 0 {
			return nil
		}
		rows := make([]T, 0, limit)
		query := base.Session(&gorm.Session{}).
			Where("("+timestampColumn+" > ? OR ("+timestampColumn+" = ? AND id > ?))", lastTimestamp, lastTimestamp, lastID).
			Order(timestampColumn + " ASC, id ASC").
			Limit(limit)
		if err := query.Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for index := range rows {
			if err := visit(rows[index]); err != nil {
				return err
			}
		}
		processed += len(rows)
		lastTimestamp, lastID = financeSourceCursor(rows[len(rows)-1], timestampColumn)
		if len(rows) < limit {
			return nil
		}
	}
}

// iterateFinanceLogSource mirrors the ClickHouse logs table's physical order.
// ClickHouse intentionally leaves Log.id at its zero default and orders rows by
// (created_at, request_id), so a numeric id cursor can silently skip rows when
// a busy second spans more than one batch. Request IDs are assigned when a log
// is written and are stable across repeated reads.
func iterateFinanceLogSource(base *gorm.DB, visit func(model.Log) error) error {
	if base == nil || visit == nil {
		return gorm.ErrInvalidData
	}
	processed := 0
	var lastTimestamp int64
	var lastRequestID string
	for {
		limit := financeBatchLimit(processed)
		if limit == 0 {
			return nil
		}
		rows := make([]model.Log, 0, limit)
		query := base.Session(&gorm.Session{}).
			Where("(created_at > ? OR (created_at = ? AND request_id > ?))", lastTimestamp, lastTimestamp, lastRequestID).
			Order("created_at ASC, request_id ASC").
			Limit(limit)
		if err := query.Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for index := range rows {
			if err := visit(rows[index]); err != nil {
				return err
			}
		}
		processed += len(rows)
		last := rows[len(rows)-1]
		lastTimestamp, lastRequestID = last.CreatedAt, last.RequestId
		if len(rows) < limit {
			return nil
		}
	}
}

// financeSourceCursor extracts the two cursor fields without retaining a
// second copy of any source row. The concrete source types are intentionally
// kept here so the query helper remains generic while its public JSON shape
// stays unchanged.
func financeSourceCursor[T any](row T, timestampColumn string) (int64, int64) {
	switch value := any(row).(type) {
	case model.TopUp:
		if timestampColumn == financeCompletionTimestamp {
			timestamp := value.CompleteTime
			if timestamp <= 0 {
				timestamp = value.CreateTime
			}
			return timestamp, int64(value.Id)
		}
	case model.SubscriptionOrder:
		if timestampColumn == financeCompletionTimestamp {
			timestamp := value.CompleteTime
			if timestamp <= 0 {
				timestamp = value.CreateTime
			}
			return timestamp, int64(value.Id)
		}
	case financeSubscriptionPaymentEventRow:
		if timestampColumn == "created_time" {
			return value.CreatedTime, int64(value.Id)
		}
	case model.FinanceLedgerEntry:
		if timestampColumn == "occurred_at" {
			return value.OccurredAt, value.Id
		}
	}
	return 0, 0
}

// Some historical successful orders have no completion timestamp. Treat their
// creation time as the effective settlement time consistently for filtering,
// ordering, and display instead of silently dropping them from finance views.
const financeCompletionTimestamp = "COALESCE(NULLIF(complete_time, 0), create_time)"

func buildFinanceOverview(start, end int64, userFilter int, methodFilter string) (financeOverview, error) {
	methods, configMap, err := loadFinancePaymentMethods()
	if err != nil {
		return financeOverview{}, err
	}
	a := newFinanceAccumulator(start, end, methods)
	if methodFilter != "" {
		// Usage logs do not retain a payment-method dimension. Excluding them is
		// safer than attributing every user's token cost to the selected method;
		// the response explicitly tells callers that profit is incomplete.
		a.overview.CostAttribution = "unavailable_for_payment_method"
	}
	// Completing a subscription creates a TopUp mirror with the same trade_no
	// so the existing wallet/order paths keep their historical semantics. The
	// subscription order is the financial source of truth, however; excluding
	// its mirror here prevents one payment from being counted twice.
	tx := model.DB.Where("status = ? AND "+financeCompletionTimestamp+" >= ? AND "+financeCompletionTimestamp+" < ?", common.TopUpStatusSuccess, start, end).
		Where(`NOT EXISTS (
			SELECT 1 FROM subscription_orders AS subscription_order
			WHERE subscription_order.trade_no = top_ups.trade_no
			  AND subscription_order.status = ?
		)`, common.TopUpStatusSuccess)
	if userFilter > 0 {
		tx = tx.Where("user_id = ?", userFilter)
	}
	if err := iterateFinanceSource[model.TopUp](tx.Select("id, user_id, expected_amount_micros, settled_amount_micros, settlement_currency, money, payment_method, payment_provider, create_time, complete_time, status"), financeCompletionTimestamp, func(topUp model.TopUp) error {
		method, provider := financeMethodFromTopUp(topUp)
		if methodFilter != "" && method != methodFilter {
			return nil
		}
		if financePlatformPaymentSource(method, provider) {
			a.markUnclassifiedSettlement()
			return nil
		}
		if !financePaymentMethodAllowed(method, provider, configMap) {
			return nil
		}
		timestamp := topUp.CompleteTime
		if timestamp <= 0 {
			timestamp = topUp.CreateTime
		}
		a.addSettlementRevenue(topUp.SettlementCurrency, method, provider, financeTopUpAmount(topUp), timestamp, topUp.UserId)
		return nil
	}); err != nil {
		return financeOverview{}, err
	}

	// A payment event is the immutable per-cycle settlement source of truth. A
	// legacy/single-cycle order is counted only when no event exists, avoiding
	// both initial-payment duplication and lost recurring renewals.
	tx = model.DB.Where("status = ? AND "+financeCompletionTimestamp+" >= ? AND "+financeCompletionTimestamp+" < ?", common.TopUpStatusSuccess, start, end).
		Where(`NOT EXISTS (
			SELECT 1 FROM subscription_payment_events AS payment_event
			WHERE payment_event.subscription_order_id = subscription_orders.id
		)`)
	if userFilter > 0 {
		tx = tx.Where("user_id = ?", userFilter)
	}
	if err := iterateFinanceSource[model.SubscriptionOrder](tx.Select("id, user_id, expected_amount_micros, settlement_currency, payment_method, payment_provider, create_time, complete_time, status"), financeCompletionTimestamp, func(order model.SubscriptionOrder) error {
		method, provider := financeNormalizePaymentSource(order.PaymentMethod, order.PaymentProvider)
		if methodFilter != "" && method != methodFilter {
			return nil
		}
		if financePlatformPaymentSource(method, provider) {
			a.markUnclassifiedSettlement()
			return nil
		}
		if !financePaymentMethodAllowed(method, provider, configMap) {
			return nil
		}
		timestamp := order.CompleteTime
		if timestamp <= 0 {
			timestamp = order.CreateTime
		}
		a.addSettlementRevenue(order.SettlementCurrency, method, provider, order.ExpectedAmountMicros, timestamp, order.UserId)
		return nil
	}); err != nil {
		return financeOverview{}, err
	}

	eventSource := model.DB.Table("subscription_payment_events AS payment_event").
		Select(`payment_event.id AS id,
			subscription_order.user_id AS user_id,
			subscription_order.payment_method AS payment_method,
			payment_event.payment_provider AS payment_provider,
			payment_event.settlement_currency AS settlement_currency,
			payment_event.settlement_amount_micros AS settlement_amount_micros,
			payment_event.created_time AS created_time`).
		Joins("JOIN subscription_orders AS subscription_order ON subscription_order.id = payment_event.subscription_order_id").
		Where("subscription_order.status = ? AND payment_event.created_time >= ? AND payment_event.created_time < ?", common.TopUpStatusSuccess, start, end)
	if userFilter > 0 {
		eventSource = eventSource.Where("subscription_order.user_id = ?", userFilter)
	}
	// Wrap the joined source so the generic cursor's id/created_time columns are
	// unambiguous on SQLite, Postgres, and MySQL alike.
	eventQuery := model.DB.Table("(?) AS subscription_payment_source", eventSource)
	if err := iterateFinanceSource[financeSubscriptionPaymentEventRow](eventQuery, "created_time", func(event financeSubscriptionPaymentEventRow) error {
		method, provider := financeNormalizePaymentSource(event.PaymentMethod, event.PaymentProvider)
		if methodFilter != "" && method != methodFilter {
			return nil
		}
		if financePlatformPaymentSource(method, provider) {
			a.markUnclassifiedSettlement()
			return nil
		}
		if !financePaymentMethodAllowed(method, provider, configMap) {
			return nil
		}
		a.addSettlementRevenue(event.SettlementCurrency, method, provider, event.SettlementAmountMicros, event.CreatedTime, event.UserId)
		return nil
	}); err != nil {
		return financeOverview{}, err
	}
	tx = model.LOG_DB.Where("type = ? AND created_at >= ? AND created_at < ?", model.LogTypeConsume, start, end)
	if userFilter > 0 {
		tx = tx.Where("user_id = ?", userFilter)
	}
	if err := iterateFinanceLogSource(tx.Select("id, user_id, created_at, request_id, type, quota, prompt_tokens, completion_tokens, other"), func(log model.Log) error {
		if methodFilter != "" {
			return nil
		}
		other, _ := common.StrToMap(log.Other)
		if !financeUsageIsCountable(other) {
			return nil
		}
		price, priced := 0.0, false
		if raw, ok := other["model_price"].(float64); ok && raw > 0 {
			price, priced = raw, true
		}
		totalTokens := max(0, log.PromptTokens+log.CompletionTokens)
		cost := int64(math.Round(price * float64(totalTokens)))
		// Fixed-price requests (for example image/MJ/task calls) legitimately
		// have no token usage. Their consume log still carries the charged quota;
		// use the recorded per-call model price only for those durable charges.
		// A zero-quota log is left at zero so missing/failed usage cannot inflate
		// the platform cost estimate.
		if priced && totalTokens == 0 && log.Quota > 0 {
			cost = financeMicrosFromFloat(price)
		}
		a.addUsage(log.UserId, log.CreatedAt, log.PromptTokens, log.CompletionTokens, cost, priced)
		return nil
	}); err != nil {
		return financeOverview{}, err
	}
	// Older/manual ledger rows may have bypassed normalizeFinanceEntry and
	// persisted the otherwise equivalent lowercase spelling (for example,
	// "usd"). The dashboard is explicitly a USD view, so compare the stored
	// value canonically instead of silently dropping those financial events.
	tx = model.DB.Where("occurred_at >= ? AND occurred_at < ? AND UPPER(TRIM(currency)) = ?", start, end, model.FinanceCurrencyUSD)
	if userFilter > 0 {
		tx = tx.Where("user_id = ?", userFilter)
	}
	if methodFilter != "" {
		tx = tx.Where("COALESCE(NULLIF(TRIM(payment_method), ''), NULLIF(TRIM(payment_provider), '')) = ?", methodFilter)
	}
	if err := iterateFinanceSource[model.FinanceLedgerEntry](tx.Select("id, entry_type, category, amount_micros, currency, direction, payment_method, payment_provider, user_id, source_type, source_id, token_units, occurred_at, created_at, created_by, reversal_of_id"), "occurred_at", func(entry model.FinanceLedgerEntry) error {
		if entry.EntryType == model.FinanceEntryRevenue {
			method, provider := financeNormalizePaymentSource(entry.PaymentMethod, entry.PaymentProvider)
			if method == "" {
				return nil
			}
			if entry.Direction == model.FinanceDirectionCredit {
				if !financePaymentMethodAllowed(method, provider, configMap) {
					return nil
				}
				a.addRevenue(method, provider, entry.AmountMicros, entry.OccurredAt, derefFinanceUser(entry.UserId))
			} else {
				if !financePaymentMethodAllowed(method, provider, configMap) {
					return nil
				}
				a.addRefund(method, provider, entry.AmountMicros, entry.OccurredAt, derefFinanceUser(entry.UserId))
			}
		} else if entry.EntryType == model.FinanceEntryExpense || entry.EntryType == model.FinanceEntryTokenCost {
			amount := entry.AmountMicros
			if entry.Direction == model.FinanceDirectionCredit {
				amount = -amount
			}
			a.addExpenseDelta(entry.Category, entry.PaymentMethod, entry.PaymentProvider, amount, entry.OccurredAt, derefFinanceUser(entry.UserId))
		}
		return nil
	}); err != nil {
		return financeOverview{}, err
	}
	view := a.finish()
	if err := attachFinanceUserLabels(&view); err != nil {
		return financeOverview{}, err
	}
	return view, nil
}

func derefFinanceUser(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func financeOverviewHandler(c *gin.Context) {
	start, end, err := parseFinanceDashboardRange(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	userID := 0
	if rawUserID := strings.TrimSpace(c.Query("user_id")); rawUserID != "" {
		userID, err = strconv.Atoi(rawUserID)
		if err != nil || userID <= 0 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid user_id"})
			return
		}
	}
	method := strings.TrimSpace(c.Query("payment_method"))
	view, err := buildFinanceOverview(start, end, userID, method)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

func financeUsersHandler(c *gin.Context) {
	start, end, err := parseFinanceDashboardRange(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	view, err := buildFinanceOverview(start, end, 0, "")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"range":                  view.Range,
		"users":                  view.Users,
		"user_metrics_complete":  view.UserMetricsComplete,
		"user_metrics_truncated": view.UserMetricsTruncated,
		"user_metrics_limit":     view.UserMetricsLimit,
	})
}

func financeUserHandler(c *gin.Context) {
	userID, err := strconv.Atoi(c.Param("user_id"))
	if err != nil || userID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid user_id"})
		return
	}
	start, end, err := parseFinanceDashboardRange(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	view, err := buildFinanceOverview(start, end, userID, strings.TrimSpace(c.Query("payment_method")))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

// parseFinanceEntryCursor reads the stable descending ledger cursor. Offset
// pagination gets slower as the append-only table grows and can skip rows while
// new entries arrive; the timestamp/id pair keeps adjacent pages deterministic.
func parseFinanceEntryCursor(c *gin.Context) (occurredAt, entryID int64, err error) {
	rawOccurredAt := strings.TrimSpace(c.Query("before_occurred_at"))
	rawEntryID := strings.TrimSpace(c.Query("before_id"))
	if rawOccurredAt == "" && rawEntryID == "" {
		return 0, 0, nil
	}
	if rawOccurredAt == "" || rawEntryID == "" {
		return 0, 0, errors.New("before_occurred_at and before_id must be provided together")
	}
	occurredAt, err = strconv.ParseInt(rawOccurredAt, 10, 64)
	if err != nil || occurredAt <= 0 {
		return 0, 0, errors.New("before_occurred_at must be a positive integer")
	}
	entryID, err = strconv.ParseInt(rawEntryID, 10, 64)
	if err != nil || entryID <= 0 {
		return 0, 0, errors.New("before_id must be a positive integer")
	}
	return occurredAt, entryID, nil
}

type financeEntryInput struct {
	EntryType       string `json:"entry_type"`
	Category        string `json:"category"`
	AmountMicros    int64  `json:"amount_micros"`
	Currency        string `json:"currency"`
	PaymentMethod   string `json:"payment_method"`
	PaymentProvider string `json:"payment_provider"`
	UserID          *int   `json:"user_id"`
	Note            string `json:"note"`
	OccurredAt      int64  `json:"occurred_at"`
	IdempotencyKey  string `json:"idempotency_key"`
}

func createFinanceEntryHandler(c *gin.Context) {
	var input financeEntryInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid finance entry"})
		return
	}
	if strings.TrimSpace(input.EntryType) != model.FinanceEntryExpense {
		c.AbortWithStatusJSON(http.StatusUnprocessableEntity, gin.H{"success": false, "message": "manual entries must be expenses"})
		return
	}
	if input.OccurredAt == 0 {
		input.OccurredAt = time.Now().Unix()
	}
	entry, err := model.AppendFinanceLedgerEntry(&model.FinanceLedgerEntry{EntryType: model.FinanceEntryExpense, Category: input.Category, AmountMicros: input.AmountMicros, Currency: input.Currency, Direction: model.FinanceDirectionDebit, PaymentMethod: input.PaymentMethod, PaymentProvider: input.PaymentProvider, UserId: input.UserID, SourceType: model.FinanceSourceManual, Note: input.Note, OccurredAt: input.OccurredAt, CreatedBy: c.GetInt("id"), IdempotencyKey: input.IdempotencyKey})
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnprocessableEntity, gin.H{"success": false, "message": err.Error()})
		return
	}
	common.ApiSuccess(c, gin.H{"entry": entry})
}

func reverseFinanceEntryHandler(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("entry_id"), 10, 64)
	if err != nil || id <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid entry_id"})
		return
	}
	entry, err := model.ReverseFinanceLedgerEntry(id, c.GetInt("id"), time.Now().Unix())
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, model.ErrFinanceEntryNotFound) {
			status = http.StatusNotFound
		}
		c.AbortWithStatusJSON(status, gin.H{"success": false, "message": err.Error()})
		return
	}
	common.ApiSuccess(c, gin.H{"entry": entry})
}

func financeEntriesHandler(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit < 1 {
		limit = 1
	}
	if limit > financeDashboardMaxEntries {
		limit = financeDashboardMaxEntries
	}
	beforeOccurredAt, beforeID, err := parseFinanceEntryCursor(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	start, end, err := parseFinanceDashboardRange(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	userID := 0
	if rawUserID := strings.TrimSpace(c.Query("user_id")); rawUserID != "" {
		userID, err = strconv.Atoi(rawUserID)
		if err != nil || userID <= 0 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid user_id"})
			return
		}
	}
	method := strings.TrimSpace(c.Query("payment_method"))
	if len(method) > 64 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid payment_method"})
		return
	}
	query := model.DB.Order("occurred_at desc, id desc").Limit(limit + 1)
	query = query.Where("occurred_at >= ? AND occurred_at < ?", start, end)
	if beforeOccurredAt > 0 {
		query = query.Where("occurred_at < ? OR (occurred_at = ? AND id < ?)", beforeOccurredAt, beforeOccurredAt, beforeID)
	}
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}
	if method != "" {
		query = query.Where("COALESCE(NULLIF(TRIM(payment_method), ''), NULLIF(TRIM(payment_provider), '')) = ?", method)
	}
	if value := strings.TrimSpace(c.Query("entry_type")); value != "" {
		query = query.Where("entry_type = ?", value)
	}
	var entries []model.FinanceLedgerEntry
	if err := query.Find(&entries).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}
	// A payment receipt is not currently appended to this table for every
	// provider. Keep the boundary explicit: this endpoint is for durable
	// ledger events such as refunds, manual expenses, and reversals, not a
	// complete list of settled payments. The overview remains the source for
	// reconciled revenue totals across TopUp and subscription records.
	page := gin.H{
		"scope":    "append_only_ledger",
		"range":    financeRange{Start: start, End: end},
		"entries":  entries,
		"has_more": hasMore,
	}
	if hasMore && len(entries) > 0 {
		last := entries[len(entries)-1]
		page["next_before_occurred_at"] = last.OccurredAt
		page["next_before_id"] = last.Id
	}
	common.ApiSuccess(c, page)
}

type financePaymentMethodInput struct {
	Label          *string `json:"label"`
	Enabled        *bool   `json:"enabled"`
	IncludeRevenue *bool   `json:"include_revenue"`
}

func updateFinancePaymentMethodHandler(c *gin.Context) {
	method := strings.TrimSpace(c.Param("method"))
	if method == "" || len(method) > 64 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid payment method"})
		return
	}
	var input financePaymentMethodInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid payment method settings"})
		return
	}
	var config model.FinancePaymentMethod
	err := model.DB.Where("method = ?", method).First(&config).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		config = model.FinancePaymentMethod{Method: method, Label: method, Enabled: true, IncludeRevenue: true, CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix(), CreatedBy: c.GetInt("id")}
		err = nil
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if input.Label != nil {
		config.Label = strings.TrimSpace(*input.Label)
	}
	if input.Enabled != nil {
		config.Enabled = *input.Enabled
	}
	if input.IncludeRevenue != nil {
		config.IncludeRevenue = *input.IncludeRevenue
	}
	if config.Label == "" {
		config.Label = method
	}
	config.UpdatedAt = time.Now().Unix()
	config.CreatedBy = c.GetInt("id")
	if err := model.DB.Save(&config).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, config)
}

func listFinancePaymentMethodsHandler(c *gin.Context) {
	methods, _, err := loadFinancePaymentMethods()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"methods": methods})
}

func GetFinanceOverview(c *gin.Context)         { financeOverviewHandler(c) }
func GetFinanceUsers(c *gin.Context)            { financeUsersHandler(c) }
func GetFinanceUser(c *gin.Context)             { financeUserHandler(c) }
func ListFinanceEntries(c *gin.Context)         { financeEntriesHandler(c) }
func CreateFinanceEntry(c *gin.Context)         { createFinanceEntryHandler(c) }
func ReverseFinanceEntry(c *gin.Context)        { reverseFinanceEntryHandler(c) }
func ListFinancePaymentMethods(c *gin.Context)  { listFinancePaymentMethodsHandler(c) }
func UpdateFinancePaymentMethod(c *gin.Context) { updateFinancePaymentMethodHandler(c) }
