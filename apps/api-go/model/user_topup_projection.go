package model

import (
	"encoding/json"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// These facts are presentation-only. Never pass a projected value to a wallet,
// settlement, refund or access-policy mutation.
type adminTopupSource struct {
	ID                   int     `json:"id"`
	UserID               int     `json:"user_id"`
	Status               string  `json:"status"`
	CreditedQuota        *int64  `json:"credited_quota"`
	Amount               *int64  `json:"amount"`
	PlatformAmountMicros *int64  `json:"platform_amount_micros"`
	ExpectedAmountMicros *int64  `json:"expected_amount_micros"`
	SettledAmountMicros  *int64  `json:"settled_amount_micros"`
	RefundedQuota        *int64  `json:"refunded_quota"`
	RefundedAmountMicros *int64  `json:"refunded_amount_micros"`
	PaymentProvider      *string `json:"payment_provider"`
	PaymentMethod        *string `json:"payment_method"`
	SettlementCurrency   *string `json:"settlement_currency"`
	Money                *string `json:"money"`
	EffectiveQuota       int64   `json:"effective_credited_quota"`
}

type adminTopupRefundBasis struct {
	TopUpID                      int              `json:"top_up_id"`
	UserID                       int              `json:"user_id"`
	OriginalCreditedQuota        int64            `json:"original_credited_quota"`
	OriginalRefundedQuota        int64            `json:"original_refunded_quota"`
	OriginalRefundedAmountMicros int64            `json:"original_refunded_amount_micros"`
	OriginalPaidAmountMicros     int64            `json:"original_paid_amount_micros"`
	RefundableQuota              int64            `json:"refundable_quota"`
	RebasedDebitedQuota          int64            `json:"rebased_debited_quota"`
	Source                       adminTopupSource `json:"source"`
}

type adminTopupPlan struct {
	Version     int    `json:"version"`
	Kind        string `json:"kind"`
	MigrationID string `json:"migration_id"`
	PlanSHA256  string `json:"plan_sha256"`
	Complete    bool   `json:"has_complete_history"`
	State       string `json:"snapshot_state"`
	Anchor      int    `json:"usd_credit_conversion"`
	SnapshotAt  int64  `json:"snapshot_at"`
	Divisor     string `json:"divisor"`
	Rounding    string `json:"rounding"`
	ExactFactor struct {
		Numerator   json.RawMessage `json:"numerator"`
		Denominator json.RawMessage `json:"denominator"`
	} `json:"exact_factor"`
	FXSource struct {
		Kind  string `json:"kind"`
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"fx_source"`
	UserIDs              []int `json:"user_ids"`
	SnapshotAllUsers     bool  `json:"snapshot_all_users"`
	IncludePending       bool  `json:"include_pending_topups"`
	OrphanPendingUserIDs []int `json:"orphan_pending_user_ids"`
	Options              []struct {
		Key    string `json:"key"`
		Before string `json:"before"`
	} `json:"option_entries"`
	Refunds *[]adminTopupRefundBasis  `json:"refund_bases"`
	Pending *[]pendingTopUpCreditBase `json:"pending_bases"`
	Blocked *[]struct {
		TopUpID int `json:"top_up_id"`
	} `json:"blocked_pending_bases"`
	Noncash *[]struct {
		ID int `json:"id"`
	} `json:"noncash_topups"`
}

type adminTopupProjectedOrder struct {
	condition string
	quota     int64
}

type adminTopupProjector struct {
	invalid bool
	cutoff  int64
	dialect string
	orders  map[int]adminTopupProjectedOrder
	// Validated audit facts, not presentation quotas. Net eligibility separately verifies current refunds.
	refundFacts map[int]WalletTopUpCreditRebase
}

const maxAdminTopupAuditOrders = 10000

var adminTopupSHA = regexp.MustCompile(`^[a-f0-9]{64}$`)
var adminTopupText = regexp.MustCompile(`^[A-Za-z0-9_. -]{0,50}$`)
var adminTopupInteger = regexp.MustCompile(`^[0-9]+$`)
var adminTopupMigrationID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

func adminTopupFactorMatches(raw json.RawMessage, expected *big.Int) bool {
	text := strings.TrimSpace(string(raw))
	return len(text) <= 64 && adminTopupInteger.MatchString(text) && text == expected.String()
}

func (p *adminTopupPlan) factor() (*big.Rat, bool) {
	if p.Version != 1 || p.Kind != "offline_credit_balance_rebase_preview" ||
		!adminTopupMigrationID.MatchString(p.MigrationID) || !p.Complete || p.State != "frozen_writers_stopped" ||
		p.Anchor != 500000 || p.SnapshotAt <= 0 || len(p.Divisor) > 64 ||
		!usageDivisorPattern.MatchString(p.Divisor) ||
		(p.Rounding != "half-away-from-zero" && p.Rounding != "toward-zero") ||
		p.Refunds == nil || p.Pending == nil || p.Blocked == nil || p.Noncash == nil || len(p.UserIDs) == 0 {
		return nil, false
	}
	divisor, ok := new(big.Rat).SetString(p.Divisor)
	if !ok || divisor.Cmp(big.NewRat(1, 1)) <= 0 ||
		!adminTopupFactorMatches(p.ExactFactor.Numerator, divisor.Denom()) ||
		!adminTopupFactorMatches(p.ExactFactor.Denominator, divisor.Num()) ||
		p.FXSource.Kind != "frozen_production_option" || p.FXSource.Key != "USDExchangeRate" {
		return nil, false
	}
	if len(p.FXSource.Value) > 64 || !usageDivisorPattern.MatchString(p.FXSource.Value) {
		return nil, false
	}
	fx, ok := new(big.Rat).SetString(p.FXSource.Value)
	return divisor, ok && fx.Cmp(divisor) == 0
}

func (s *adminTopupSource) order() (TopUp, bool) {
	if s.ID <= 0 || s.UserID <= 0 || s.CreditedQuota == nil || s.Amount == nil ||
		s.PlatformAmountMicros == nil || s.ExpectedAmountMicros == nil || s.SettledAmountMicros == nil ||
		s.RefundedQuota == nil || s.RefundedAmountMicros == nil || s.PaymentProvider == nil ||
		s.PaymentMethod == nil || s.SettlementCurrency == nil || s.Money == nil {
		return TopUp{}, false
	}
	for _, value := range []int64{*s.CreditedQuota, *s.Amount, *s.PlatformAmountMicros,
		*s.ExpectedAmountMicros, *s.SettledAmountMicros, *s.RefundedQuota, *s.RefundedAmountMicros, s.EffectiveQuota} {
		if value < 0 || value > common.MaxWalletQuota {
			return TopUp{}, false
		}
	}
	for _, text := range []string{*s.PaymentProvider, *s.PaymentMethod, *s.SettlementCurrency} {
		if !adminTopupText.MatchString(text) {
			return TopUp{}, false
		}
	}
	if len(*s.Money) > 128 {
		return TopUp{}, false
	}
	money, err := strconv.ParseFloat(*s.Money, 64)
	if err != nil || math.IsNaN(money) || math.IsInf(money, 0) || money < 0 || money > float64(common.MaxWalletQuota)/1000000 {
		return TopUp{}, false
	}
	return TopUp{Id: s.ID, UserId: s.UserID, Status: s.Status, CreditedQuota: *s.CreditedQuota,
		Amount: *s.Amount, PlatformAmountMicros: *s.PlatformAmountMicros,
		ExpectedAmountMicros: *s.ExpectedAmountMicros, SettledAmountMicros: *s.SettledAmountMicros,
		RefundedQuota: *s.RefundedQuota, RefundedAmountMicros: *s.RefundedAmountMicros,
		PaymentProvider: *s.PaymentProvider, PaymentMethod: *s.PaymentMethod,
		SettlementCurrency: *s.SettlementCurrency, Money: money}, true
}

// A legacy amount fallback uses the frozen Q, never today's option or FX.
func adminTopupFrozenQuota(order *TopUp, plan *adminTopupPlan) (int64, bool) {
	if !knownExternalTopUpSource(order) && !epayHasImmutableSettlementSnapshot(order) {
		return 0, false
	}
	if order.CreditedQuota > 0 {
		return order.CreditedQuota, true
	}
	if order.PaymentProvider == PaymentProviderCreem || order.PaymentMethod == PaymentMethodCreem {
		return order.Amount, order.Amount > 0
	}
	var quota *big.Rat
	for _, option := range plan.Options {
		if option.Key == "QuotaPerUnit" {
			if quota != nil || len(option.Before) > 64 || !usageDivisorPattern.MatchString(option.Before) {
				return 0, false
			}
			var ok bool
			quota, ok = new(big.Rat).SetString(option.Before)
			if !ok || quota.Sign() <= 0 {
				return 0, false
			}
		}
	}
	if quota == nil || order.Amount <= 0 {
		return 0, false
	}
	result := new(big.Int).Mul(big.NewInt(order.Amount), quota.Num())
	result.Quo(result, quota.Denom())
	return result.Int64(), result.IsInt64() && result.Sign() > 0 && result.Cmp(big.NewInt(common.MaxWalletQuota)) <= 0
}

// Audited text is restricted to ASCII payment identifiers above. Quoting still
// escapes both quote styles used by the supported database dialects.
func adminTopupSQLText(value, dialect string) string {
	if dialect == "mysql" {
		value = strings.ReplaceAll(value, `\`, `\\`)
	}
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func adminTopupSourceCondition(order TopUp, dialect string, pending bool) string {
	clauses := []string{"top_ups.user_id = " + strconv.Itoa(order.UserId)}
	for column, value := range map[string]int64{"amount": order.Amount, "platform_amount_micros": order.PlatformAmountMicros} {
		clauses = append(clauses, "top_ups."+column+" = "+strconv.FormatInt(value, 10))
	}
	clauses = append(clauses, "top_ups.payment_provider = "+adminTopupSQLText(order.PaymentProvider, dialect),
		"top_ups.settlement_currency = "+adminTopupSQLText(order.SettlementCurrency, dialect),
		"top_ups.money = "+strconv.FormatFloat(order.Money, 'g', -1, 64))
	method := "top_ups.payment_method = " + adminTopupSQLText(order.PaymentMethod, dialect)
	if pending && order.PaymentProvider == PaymentProviderEpay && order.PaymentMethod == PaymentProviderEpay {
		method = "(" + method + " OR (top_ups.payment_method NOT IN ('ldc','linuxdo','linux_do','linuxdo_credit','balance')))"
	}
	clauses = append(clauses, method)
	if !pending {
		clauses = append(clauses,
			"top_ups.credited_quota = "+strconv.FormatInt(order.CreditedQuota, 10),
			"top_ups.expected_amount_micros = "+strconv.FormatInt(order.ExpectedAmountMicros, 10),
			"top_ups.settled_amount_micros = "+strconv.FormatInt(order.SettledAmountMicros, 10),
			"top_ups.refunded_quota >= "+strconv.FormatInt(order.RefundedQuota, 10),
			"top_ups.refunded_amount_micros >= "+strconv.FormatInt(order.RefundedAmountMicros, 10))
	} else {
		clauses = append(clauses, "top_ups.expected_amount_micros = "+strconv.FormatInt(expectedTopUpAmountMicros(&order), 10), "top_ups.settled_amount_micros > 0")
	}
	sort.Strings(clauses)
	return strings.Join(clauses, " AND ")
}

func loadAdminTopupProjector(tx *gorm.DB) (*adminTopupProjector, error) {
	p := &adminTopupProjector{orders: map[int]adminTopupProjectedOrder{}, refundFacts: map[int]WalletTopUpCreditRebase{}, dialect: tx.Dialector.Name()}
	exists, err := walletCreditAuditTableExists(tx, "wallet_credit_rebases")
	if err != nil || !exists {
		return p, err
	}
	var audits []struct {
		MigrationID string
		PlanSHA256  string `gorm:"column:plan_sha256"`
		Plan        string
	}
	// SELECT * tolerates an older partial audit schema, whose missing SHA makes
	// projection unavailable rather than breaking the legacy list DTO.
	if err := tx.Table("wallet_credit_rebases").Limit(129).Find(&audits).Error; err != nil {
		return nil, err
	}
	if len(audits) > 128 {
		p.invalid = true
		return p, nil
	}
	childExists, err := walletCreditAuditTableExists(tx, "wallet_topup_credit_rebases")
	if err != nil {
		return nil, err
	}
	children := map[int]WalletTopUpCreditRebase{}
	if childExists {
		columns, err := tx.Migrator().ColumnTypes(&WalletTopUpCreditRebase{})
		if err != nil {
			return nil, err
		}
		present := map[string]bool{}
		for _, column := range columns {
			present[strings.ToLower(column.Name())] = true
		}
		for _, name := range []string{"top_up_id", "user_id", "migration_id", "original_credited_quota", "original_refunded_quota", "original_refunded_amount_micros", "original_paid_amount_micros", "refundable_quota", "rebased_debited_quota"} {
			if !present[name] {
				p.invalid = true
				return p, nil
			}
		}
		var rows []WalletTopUpCreditRebase
		if err := tx.Select("top_up_id,user_id,migration_id,original_credited_quota,original_refunded_quota,original_refunded_amount_micros,original_paid_amount_micros,refundable_quota,rebased_debited_quota").Limit(maxAdminTopupAuditOrders + 1).Find(&rows).Error; err != nil {
			return nil, err
		}
		if len(rows) > maxAdminTopupAuditOrders {
			p.invalid = true
			return p, nil
		}
		for _, child := range rows {
			if child.TopUpID <= 0 || children[child.TopUpID].TopUpID != 0 {
				p.invalid = true
				return p, nil
			}
			children[child.TopUpID] = child
		}
	}
	seenUsers, seenOrders := map[int]bool{}, map[int]bool{}
	dialect := tx.Dialector.Name()
	for _, audit := range audits {
		var plan adminTopupPlan
		if len(audit.Plan) > 32*1024*1024 || json.Unmarshal([]byte(audit.Plan), &plan) != nil ||
			plan.MigrationID != audit.MigrationID || !adminTopupSHA.MatchString(audit.PlanSHA256) || plan.PlanSHA256 != audit.PlanSHA256 {
			p.invalid = true
			return p, nil
		}
		divisor, valid := plan.factor()
		if !valid {
			p.invalid = true
			return p, nil
		}
		users := map[int]bool{}
		for _, id := range plan.UserIDs {
			if id <= 0 || users[id] || seenUsers[id] {
				p.invalid = true
				return p, nil
			}
			users[id], seenUsers[id] = true, true
		}
		orphanUsers := map[int]bool{}
		for _, id := range plan.OrphanPendingUserIDs {
			if id <= 0 || users[id] || orphanUsers[id] || !plan.SnapshotAllUsers {
				p.invalid = true
				return p, nil
			}
			orphanUsers[id] = true
		}
		if plan.SnapshotAt > p.cutoff {
			p.cutoff = plan.SnapshotAt
		}
		claim := func(id int) bool {
			if id <= 0 || seenOrders[id] || len(seenOrders) >= maxAdminTopupAuditOrders {
				return false
			}
			seenOrders[id] = true
			p.orders[id] = adminTopupProjectedOrder{condition: "1 = 0"}
			return true
		}
		for _, base := range *plan.Refunds {
			if !claim(base.TopUpID) {
				p.invalid = true
				return p, nil
			}
			order, valid := base.Source.order()
			if !valid || order.Id != base.TopUpID || order.UserId != base.UserID || !users[base.UserID] || order.Status != common.TopUpStatusSuccess {
				continue
			}
			credited, valid := adminTopupFrozenQuota(&order, &plan)
			paid := topUpPaidAmountMicros(&order)
			if !valid || credited <= 0 || credited != base.OriginalCreditedQuota || credited != base.Source.EffectiveQuota ||
				paid <= 0 || paid != base.OriginalPaidAmountMicros || base.OriginalRefundedQuota != order.RefundedQuota ||
				base.OriginalRefundedAmountMicros != order.RefundedAmountMicros || order.RefundedQuota > credited ||
				order.RefundedAmountMicros > paid || base.RebasedDebitedQuota != 0 ||
				base.RefundableQuota != scaleReferralCredit(credited-order.RefundedQuota, divisor, plan.Rounding) {
				continue
			}
			child, found := children[base.TopUpID]
			if !found || child.UserID != base.UserID || child.MigrationID != plan.MigrationID ||
				child.OriginalCreditedQuota != credited || child.OriginalPaidAmountMicros != paid ||
				child.OriginalRefundedQuota != base.OriginalRefundedQuota || child.OriginalRefundedAmountMicros != base.OriginalRefundedAmountMicros ||
				child.RefundableQuota != base.RefundableQuota || child.RebasedDebitedQuota < 0 || child.RebasedDebitedQuota > child.RefundableQuota {
				continue
			}
			condition := adminTopupSourceCondition(order, dialect, false) +
				" AND top_ups.refunded_quota <= " + strconv.FormatInt(credited, 10) +
				" AND top_ups.refunded_amount_micros <= " + strconv.FormatInt(paid, 10) +
				" AND COALESCE(top_ups.pending_credit_rebase_key, '') = ''"
			p.orders[base.TopUpID] = adminTopupProjectedOrder{condition: condition, quota: scaleReferralCredit(credited, divisor, plan.Rounding)}
			p.refundFacts[base.TopUpID] = child
		}
		for _, base := range *plan.Pending {
			if !claim(base.TopUpID) {
				p.invalid = true
				return p, nil
			}
			// Use frozen source Q rather than pending.valid(), which intentionally
			// belongs to the live settlement path and does not accept success rows.
			var source adminTopupSource
			encoded, _ := json.Marshal(base.Source)
			if json.Unmarshal(encoded, &source) != nil {
				continue
			}
			order, valid := source.order()
			credited, known := adminTopupFrozenQuota(&order, &plan)
			orphan := base.ownerMissingAtSnapshot()
			orphanValid := !orphan || (orphanUsers[base.UserID] && order.Status == common.TopUpStatusFailed &&
				order.PaymentProvider == PaymentProviderWaffoPancake && base.Source.FailureReasonCode == string(PaymentOrderFailureCheckoutTimeout) &&
				order.CreditedQuota > 0 && order.ExpectedAmountMicros > 0 && order.SettledAmountMicros == 0 &&
				order.RefundedQuota == 0 && order.RefundedAmountMicros == 0)
			if !plan.IncludePending || !valid || !known || order.Id != base.TopUpID || order.UserId != base.UserID ||
				(!users[base.UserID] && !orphan) || !orphanValid || !base.ownerFlagValid() ||
				!pendingTopUpRecoverable(order.Status, order.PaymentProvider, base.Source.FailureReasonCode) ||
				credited != base.OriginalQuota || source.EffectiveQuota != credited || base.EffectiveQuota <= 0 ||
				base.EffectiveQuota != scaleReferralCredit(credited, divisor, plan.Rounding) ||
				base.Source.PendingCreditRebaseKey == nil || *base.Source.PendingCreditRebaseKey != "" ||
				base.Source.PendingCreditRebaseOriginal == nil || *base.Source.PendingCreditRebaseOriginal != 0 ||
				base.Source.PendingCreditRebaseEffective == nil || *base.Source.PendingCreditRebaseEffective != 0 {
				continue
			}
			condition := adminTopupSourceCondition(order, dialect, true) +
				" AND top_ups.pending_credit_rebase_key = " + adminTopupSQLText(plan.MigrationID, dialect) +
				" AND top_ups.pending_credit_rebase_original_quota = " + strconv.FormatInt(base.OriginalQuota, 10) +
				" AND top_ups.pending_credit_rebase_effective_quota = " + strconv.FormatInt(base.EffectiveQuota, 10) +
				" AND top_ups.credited_quota = " + strconv.FormatInt(base.EffectiveQuota, 10) +
				" AND top_ups.complete_time > " + strconv.FormatInt(plan.SnapshotAt, 10)
			p.orders[base.TopUpID] = adminTopupProjectedOrder{condition: condition, quota: base.EffectiveQuota}
		}
		for _, source := range *plan.Noncash {
			if !claim(source.ID) {
				p.invalid = true
				return p, nil
			}
		}
		for _, base := range *plan.Blocked {
			if !claim(base.TopUpID) {
				p.invalid = true
				return p, nil
			}
		}
	}
	for id := range children {
		if !seenOrders[id] {
			p.invalid = true
		}
	}
	return p, nil
}

func (p *adminTopupProjector) quotaSQL() string {
	if p.invalid || p.cutoff <= 0 {
		if p.dialect == "mysql" {
			return "CAST(NULL AS SIGNED)"
		}
		return "CAST(NULL AS BIGINT)"
	}
	ids := make([]int, 0, len(p.orders))
	for id := range p.orders {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	var sql strings.Builder
	sql.WriteString("CASE ")
	for _, id := range ids {
		order := p.orders[id]
		sql.WriteString("WHEN top_ups.id = " + strconv.Itoa(id) + " THEN CASE WHEN " + order.condition + " THEN " + strconv.FormatInt(order.quota, 10) + " ELSE NULL END ")
	}
	// An unmatched historical row or a lost pending marker is not a new grant.
	// Only orders created/completed after the audited freeze, with an immutable
	// positive credit/monetary quote, can retain their present credit unit.
	sql.WriteString("WHEN top_ups.create_time > " + strconv.FormatInt(p.cutoff, 10) +
		" AND top_ups.complete_time > " + strconv.FormatInt(p.cutoff, 10) +
		" AND top_ups.credited_quota > 0 AND top_ups.credited_quota <= " + strconv.FormatInt(common.MaxWalletQuota, 10) +
		" AND top_ups.expected_amount_micros > 0 AND TRIM(COALESCE(top_ups.settlement_currency, '')) <> ''" +
		" AND COALESCE(top_ups.pending_credit_rebase_key, '') = ''" +
		" AND COALESCE(top_ups.pending_credit_rebase_original_quota, 0) = 0" +
		" AND COALESCE(top_ups.pending_credit_rebase_effective_quota, 0) = 0" +
		" AND (top_ups.payment_provider IN ('epay','stripe','creem','waffo','waffo_pancake')" +
		" OR (COALESCE(top_ups.payment_provider, '') = '' AND top_ups.payment_method IN ('stripe','creem','waffo','waffo_pancake','alipay','wxpay')))" +
		" THEN top_ups.credited_quota ELSE NULL END")
	return sql.String()
}

func adminTopupPaymentBasis(settled, historical int64) string {
	switch {
	case settled > 0 && historical > 0:
		return "mixed"
	case settled > 0:
		return "settled"
	case historical > 0:
		return "historical"
	default:
		return "none"
	}
}

func populateAdminUserTopups(tx *gorm.DB, users []*User) error {
	ids := make([]int, 0, len(users))
	byID := make(map[int]*UserTopupSummary, len(users))
	for _, user := range users {
		if user == nil || user.Id <= 0 {
			continue
		}
		ids = append(ids, user.Id)
		user.TopupSummary = &UserTopupSummary{Methods: []UserTopupMethod{}, QuotaProjectionAvailable: true, PaymentBasis: "none"}
		byID[user.Id] = user.TopupSummary
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := adminUserTopupRows(tx)
	if err != nil {
		return err
	}
	availability, normalized := adminUserTopupQuotaTotalsSQL()
	var aggregates []userTopupAggregate
	if err := tx.Table("(?) AS admin_topup_rows", rows).
		Select("user_id, payment_method, payment_provider, settlement_currency, COALESCE(SUM(raw_quota), 0) AS credited_quota, "+
			normalized+" AS normalized_quota, "+availability+" AS quota_projection_available, "+
			"COALESCE(SUM(money_micros), 0) AS money_micros, COUNT(*) AS orders, "+
			"COALESCE(SUM(settled_money_micros), 0) AS settled_money_micros, "+
			"COALESCE(SUM(historical_money_micros), 0) AS historical_money_micros, "+
			"SUM(settled_orders) AS settled_orders, SUM(historical_orders) AS historical_orders").
		Where("user_id IN ?", ids).
		Group("user_id, payment_method, payment_provider, settlement_currency").
		Order("user_id ASC, payment_method ASC, payment_provider ASC, settlement_currency ASC").
		Scan(&aggregates).Error; err != nil {
		return err
	}
	allMoney, settledMoney, historicalMoney := map[int]map[string]int64{}, map[int]map[string]int64{}, map[int]map[string]int64{}
	add := func(totals map[int]map[string]int64, id int, currency string, amount int64) {
		if totals[id] == nil {
			totals[id] = map[string]int64{}
		}
		totals[id][currency] += amount
	}
	for _, row := range aggregates {
		summary := byID[row.UserID]
		if summary == nil {
			continue
		}
		method := UserTopupMethod{Method: strings.TrimSpace(row.PaymentMethod), Provider: strings.TrimSpace(row.PaymentProvider),
			SettlementCurrency: row.SettlementCurrency, Quota: row.CreditedQuota, MoneyMicros: row.MoneyMicros, Orders: row.Orders,
			NormalizedQuota: row.NormalizedQuota, QuotaProjectionAvailable: row.QuotaProjectionAvailable == 1,
			SettledMoneyMicros: row.SettledMoneyMicros, HistoricalMoneyMicros: row.HistoricalMoneyMicros,
			SettledOrders: row.SettledOrders, HistoricalOrders: row.HistoricalOrders,
			PaymentBasis: adminTopupPaymentBasis(row.SettledOrders, row.HistoricalOrders)}
		summary.Quota += method.Quota
		summary.Orders += method.Orders
		summary.Methods = append(summary.Methods, method)
		summary.SettledOrders += method.SettledOrders
		summary.HistoricalOrders += method.HistoricalOrders
		if !method.QuotaProjectionAvailable || method.NormalizedQuota > common.MaxWalletQuota-summary.NormalizedQuota {
			summary.QuotaProjectionAvailable = false
		} else {
			summary.NormalizedQuota += method.NormalizedQuota
		}
		add(allMoney, row.UserID, row.SettlementCurrency, row.MoneyMicros)
		if row.SettledOrders > 0 {
			add(settledMoney, row.UserID, row.SettlementCurrency, row.SettledMoneyMicros)
		}
		if row.HistoricalOrders > 0 {
			add(historicalMoney, row.UserID, row.SettlementCurrency, row.HistoricalMoneyMicros)
		}
	}
	categoryTotal := func(totals map[string]int64) int64 {
		if len(totals) != 1 {
			return 0
		}
		for currency, amount := range totals {
			if currency != "UNKNOWN" {
				return amount
			}
		}
		return 0
	}
	for id, summary := range byID {
		if !summary.QuotaProjectionAvailable {
			summary.NormalizedQuota = 0
		}
		summary.PaymentBasis = adminTopupPaymentBasis(summary.SettledOrders, summary.HistoricalOrders)
		if totals := allMoney[id]; len(totals) > 1 {
			summary.Currency = "MULTIPLE"
		} else {
			for currency, amount := range totals {
				summary.Currency, summary.MoneyMicros = currency, amount
			}
		}
		summary.SettledMoneyMicros = categoryTotal(settledMoney[id])
		summary.HistoricalMoneyMicros = categoryTotal(historicalMoney[id])
	}
	return nil
}
