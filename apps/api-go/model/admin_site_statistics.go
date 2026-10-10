/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
package model

import (
	"context"
	"database/sql"
	"math/big"
	"sort"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// Decimal strings preserve exact all-site totals beyond a browser's safe
// integer range. These are read projections, never another financial ledger.
type AdminSiteStatistics struct {
	AsOf                int64                   `json:"as_of"`
	CreditsPerUSD       int64                   `json:"credits_per_usd"`
	TotalUsedCredits    string                  `json:"total_used_credits"`
	TotalBalanceCredits string                  `json:"total_balance_credits"`
	Recharge            AdminRechargeStatistics `json:"recharge"`
}

type AdminRechargeStatistics struct {
	Currencies        []AdminRechargeCurrency `json:"currencies"`
	VirtualUnits      []AdminRechargeCurrency `json:"virtual_units"`
	ConfirmedOrders   int64                   `json:"confirmed_orders"`
	UnconfirmedOrders int64                   `json:"unconfirmed_orders"`
	InvalidOrders     int64                   `json:"invalid_orders"`
}

type AdminRechargeCurrency struct {
	Currency             string `json:"currency"`
	GrossAmountMicros    string `json:"gross_amount_micros"`
	RefundedAmountMicros string `json:"refunded_amount_micros"`
	NetAmountMicros      string `json:"net_amount_micros"`
	Orders               int64  `json:"orders"`
}

type adminRechargeAccumulator struct {
	gross, refunded big.Int
	orders          int64
}

func adminRechargeCurrency(raw string) (string, bool) {
	currency := strings.ToUpper(strings.TrimSpace(raw))
	if len(currency) != 3 {
		return "", false
	}
	for i := range len(currency) {
		if currency[i] < 'A' || currency[i] > 'Z' {
			return "", false
		}
	}
	return currency, true
}

// GetAdminSiteStatistics uses the current wallet balance and the same immutable
// historical usage projection as the personal API for integer credits, and
// successful external wallet top-ups for original payment units. Discounts,
// grants, wallet transfers and store revenue cannot become payment evidence.
// The original currency and settled micros are never reconstructed from
// credited quota, expected quotes, the legacy Money float, or current FX.
func GetAdminSiteStatistics(ctx context.Context) (*AdminSiteStatistics, error) {
	if DB == nil {
		return nil, gorm.ErrInvalidDB
	}
	result := AdminSiteStatistics{CreditsPerUSD: common.FixedCreditsPerUSD,
		Recharge: AdminRechargeStatistics{Currencies: make([]AdminRechargeCurrency, 0), VirtualUnits: make([]AdminRechargeCurrency, 0)}}
	options := &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}
	if DB.Dialector.Name() == "sqlite" {
		options.Isolation = sql.LevelSerializable
	}
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		projector, err := LoadUsageProjector(tx)
		if err != nil {
			return err
		}
		var balance, used big.Int
		// Retained disabled/deleted accounts remain part of site history and
		// existing balance obligations. IDs only join the audit projection;
		// select no names, contact information or other personal fields.
		rows, err := tx.Unscoped().Model(&User{}).Select("id,quota,used_quota").Rows()
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, consumed int
			var quota int64
			if err = rows.Scan(&id, &quota, &consumed); err != nil {
				_ = rows.Close()
				return err
			}
			projection, err := projector.User(id, consumed)
			if err != nil {
				_ = rows.Close()
				return err
			}
			balance.Add(&balance, big.NewInt(quota))
			used.Add(&used, big.NewInt(int64(projection.NormalizedUsedQuota)))
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		result.TotalBalanceCredits, result.TotalUsedCredits = balance.String(), used.String()
		currencies := make(map[string]*adminRechargeAccumulator)
		virtualUnits := make(map[string]*adminRechargeAccumulator)
		// Subscription completion can create a TopUp mirror. Its payment is a
		// subscription purchase, not a second wallet recharge, in any status.
		query := tx.Model(&TopUp{}).Select("COALESCE(payment_method,'') AS payment_method,COALESCE(payment_provider,'') AS payment_provider,COALESCE(settlement_currency,'') AS settlement_currency,settled_amount_micros,refunded_amount_micros,expected_amount_micros,credited_quota").
			Where("status = ?", common.TopUpStatusSuccess).
			Where("NOT EXISTS (SELECT 1 FROM subscription_orders AS subscription_order WHERE subscription_order.trade_no = top_ups.trade_no)")
		rows, err = query.Rows()
		if err != nil {
			return err
		}
		for rows.Next() {
			var order TopUp
			if err = rows.Scan(&order.PaymentMethod, &order.PaymentProvider, &order.SettlementCurrency, &order.SettledAmountMicros, &order.RefundedAmountMicros, &order.ExpectedAmountMicros, &order.CreditedQuota); err != nil {
				_ = rows.Close()
				return err
			}
			order.PaymentMethod = strings.ToLower(strings.TrimSpace(order.PaymentMethod))
			order.PaymentProvider = strings.ToLower(strings.TrimSpace(order.PaymentProvider))
			currency, valid := adminRechargeCurrency(order.SettlementCurrency)
			// The ePay LDC adapter is an external virtual-unit payment, not
			// cash. Only its immutable pre-payment snapshot and successful
			// settled counter qualify; legacy floats never reconstruct it.
			virtual := currency == "LDC" && order.PaymentProvider == PaymentProviderEpay &&
				isLegacyLinuxDOCreditTopUp(&order)
			if !virtual && !IsFinancialPaymentSource(order.PaymentMethod, order.PaymentProvider) {
				continue
			}
			if (virtual && !EpayHasImmutableSettlementSnapshot(&order)) ||
				(!virtual && (currency == "LDC" || !knownExternalTopUpSource(&order))) {
				result.Recharge.UnconfirmedOrders++
				continue
			}
			if order.SettledAmountMicros < 0 || order.RefundedAmountMicros < 0 || order.RefundedAmountMicros > order.SettledAmountMicros {
				result.Recharge.InvalidOrders++
				continue
			}
			if !valid || order.SettledAmountMicros == 0 {
				result.Recharge.UnconfirmedOrders++
				continue
			}
			group := currencies
			if virtual {
				group = virtualUnits
			}
			metric := group[currency]
			if metric == nil {
				metric = &adminRechargeAccumulator{}
				group[currency] = metric
			}
			metric.gross.Add(&metric.gross, big.NewInt(order.SettledAmountMicros))
			metric.refunded.Add(&metric.refunded, big.NewInt(order.RefundedAmountMicros))
			metric.orders++
			result.Recharge.ConfirmedOrders++
		}
		err = rows.Err()
		closeErr = rows.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		result.Recharge.Currencies = adminRechargeRows(currencies)
		result.Recharge.VirtualUnits = adminRechargeRows(virtualUnits)
		result.AsOf = common.GetTimestamp()
		return nil
	}, options)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func adminRechargeRows(groups map[string]*adminRechargeAccumulator) []AdminRechargeCurrency {
	rows := make([]AdminRechargeCurrency, 0, len(groups))
	for currency, metric := range groups {
		net := new(big.Int).Sub(&metric.gross, &metric.refunded)
		rows = append(rows, AdminRechargeCurrency{Currency: currency, GrossAmountMicros: metric.gross.String(), RefundedAmountMicros: metric.refunded.String(), NetAmountMicros: net.String(), Orders: metric.orders})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Currency < rows[j].Currency })
	return rows
}
