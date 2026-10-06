package model

import (
	"encoding/json"
	"errors"
	"math/big"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Activation is deliberately independent of the older writer parser. The
// central operator command owns raising the runtime capability and floor.
func MerchantStoreRefundRequiresWriter(tx *gorm.DB) error {
	var option Option
	q := tx
	if tx.Dialector.Name() != "sqlite" {
		q = q.Clauses(clause.Locking{Strength: "SHARE"})
	}
	if e := q.Where("key = ?", MerchantStoreWriterCapabilityOption).First(&option).Error; e != nil || option.Key != MerchantStoreWriterCapabilityOption || option.Value != "4" {
		return ErrMerchantStoreWriterFrozen
	}
	return nil
}

func storeRefundText(s string, required bool) bool {
	return utf8.ValidString(s) && len(s) <= 4096 && (!required || strings.TrimSpace(s) != "") && !strings.ContainsRune(s, 0)
}
func storeRefundInputValid(in MerchantStoreRefundInput) bool {
	if len(in.RequestKey) < 1 || len(in.RequestKey) > 64 || !storeRefundText(in.Reason, true) || in.Quantity < 0 || in.AmountQuota < 0 || in.AmountMinor < 0 || len(in.StockIDs) > 1000 {
		return false
	}
	for _, r := range in.RequestKey {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	switch in.Mode {
	case "full":
		return in.Quantity == 0 && len(in.StockIDs) == 0 && in.AmountQuota == 0 && in.AmountMinor == 0
	case "quantity":
		if in.Quantity < 1 || in.Quantity > 1000 || in.AmountQuota != 0 || in.AmountMinor != 0 || len(in.StockIDs) > 0 && len(in.StockIDs) != in.Quantity {
			return false
		}
		seen := map[string]bool{}
		for _, id := range in.StockIDs {
			if len(id) != 36 || seen[id] {
				return false
			}
			seen[id] = true
		}
		return true
	case "amount":
		return in.Quantity == 0 && len(in.StockIDs) == 0 && (in.AmountQuota > 0) != (in.AmountMinor > 0)
	}
	return false
}
func storeRefundAccess(tx *gorm.DB, o *MerchantStoreOrder, actor int, decision bool) (string, error) {
	u, e := storeUser(tx, actor, common.RoleCommonUser)
	if e != nil {
		return "", e
	}
	if decision {
		if actor == o.SellerID {
			return "seller", nil
		}
		if u.Role >= common.RoleRootUser {
			return "root", nil
		}
		return "", ErrMerchantStoreDenied
	}
	if actor == o.BuyerID {
		return "buyer", nil
	}
	if actor == o.SellerID {
		return "seller", nil
	}
	if u.Role >= common.RoleRootUser {
		return "root", nil
	}
	return "", ErrMerchantStoreDenied
}
func storeRefundProof(tx *gorm.DB, o *MerchantStoreOrder, actor int, p MerchantStoreRefundPickupProof) error {
	if p.OrderID != o.ID || !storeTokenValid(p.Token) || o.PickupTokenHash != storeHash(p.Token) || len(p.Code) > 72 {
		return ErrMerchantStoreDenied
	}
	if o.PickupLoginRequired {
		if actor != o.BuyerID {
			return ErrMerchantStoreDenied
		}
		if _, e := storeUser(tx, actor, common.RoleCommonUser); e != nil {
			return e
		}
	}
	if (o.PickupCodeRequired || o.PickupCodeHash != "") && bcrypt.CompareHashAndPassword([]byte(o.PickupCodeHash), []byte(p.Code)) != nil {
		return ErrMerchantStoreDenied
	}
	return nil
}
func storeRefundPaid(o *MerchantStoreOrder) bool {
	return o.Quantity > 0 && marketQuotaValid(o.PriceQuota) && o.PaidAt > 0 && (o.Status == "paid" || o.Status == "refund_pending" || o.Status == "refunded")
}
func storeRefundPartialSupported(o *MerchantStoreOrder, b *MerchantStoreRefundPaymentBasis) bool {
	return o.PaymentMethod == "balance" || b != nil && (o.PaymentMethod == "platform:waffo_pancake" || o.PaymentMethod == "external:waffo_pancake")
}
func storeRefundBasis(tx *gorm.DB, o *MerchantStoreOrder) (*MerchantStoreRefundPaymentBasis, error) {
	var b MerchantStoreRefundPaymentBasis
	e := tx.First(&b, "order_id = ?", o.ID).Error
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if b.AmountMinor <= 0 || b.Currency != o.Currency || b.ReceiptReference != o.ProviderTradeID {
		return nil, ErrMerchantStoreConflict
	}
	return &b, nil
}
func storeRefundRows(tx *gorm.DB, o *MerchantStoreOrder) ([]MerchantStoreRefund, error) {
	rows := []MerchantStoreRefund{}
	if e := tx.Where("order_id = ?", o.ID).Order("created_at ASC,id ASC").Find(&rows).Error; e != nil {
		return nil, e
	}
	items := []MerchantStoreRefundItem{}
	if e := tx.Where("order_id = ?", o.ID).Order("stock_id ASC").Find(&items).Error; e != nil {
		return nil, e
	}
	byRefund := make(map[string][]string, len(rows))
	for _, item := range items {
		byRefund[item.RefundID] = append(byRefund[item.RefundID], item.StockID)
	}
	for i := range rows {
		rows[i].StockIDs = byRefund[rows[i].ID]
		if rows[i].StockIDs == nil {
			rows[i].StockIDs = []string{}
		}
	}
	return rows, nil
}

func storeRefundActive(status string) bool {
	return status == "requested" || status == "awaiting_provider" || status == "reconciliation_required" || status == "completed"
}

type storeRefundUsage struct {
	completed, reserved             int
	nativeCompleted, nativeReserved int64
	stockIDs                        map[string]bool
	quantity                        int
	quantityQuota                   int
	quantityNative                  int64
}

func storeRefundTotals(rows []MerchantStoreRefund, o *MerchantStoreOrder, b *MerchantStoreRefundPaymentBasis) (storeRefundUsage, error) {
	u := storeRefundUsage{stockIDs: map[string]bool{}}
	for _, r := range rows {
		if !storeRefundActive(r.Status) {
			continue
		}
		if r.PrincipalQuota < 0 || r.PrincipalQuota == 0 && r.AmountMinor == 0 || r.AmountMinor < 0 {
			return u, ErrMerchantStoreConflict
		}
		if r.Status == "completed" {
			u.completed += r.PrincipalQuota
			u.nativeCompleted += r.AmountMinor
		} else {
			u.reserved += r.PrincipalQuota
			u.nativeReserved += r.AmountMinor
		}
		if r.Mode == "quantity" {
			u.quantity += r.Quantity
			u.quantityQuota += r.PrincipalQuota
			u.quantityNative += r.AmountMinor
		}
		for _, id := range r.StockIDs {
			if u.stockIDs[id] {
				return u, ErrMerchantStoreConflict
			}
			u.stockIDs[id] = true
		}
	}
	if u.completed < 0 || u.reserved < 0 || u.completed > o.PriceQuota-u.reserved || b != nil && (u.nativeCompleted < 0 || u.nativeReserved < 0 || u.nativeCompleted > b.AmountMinor-u.nativeReserved) {
		return u, ErrMerchantStoreConflict
	}
	return u, nil
}

// The stock row is the fulfillment truth. Pending refund items are reservations,
// and amount adjustments have no rows in this state.
func MerchantStoreCompletedRefundQuantity(tx *gorm.DB, o *MerchantStoreOrder) (int64, error) {
	var n int64
	e := storeOrderStock(tx, o).Model(&MerchantStoreStock{}).Where("state = ?", "refunded").Count(&n).Error
	if e == nil && (n < 0 || n > int64(o.Quantity)) {
		e = ErrMerchantStoreConflict
	}
	return n, e
}
func storeRefundHeldStockIDs(tx *gorm.DB, o *MerchantStoreOrder) (map[string]bool, error) {
	ids := []string{}
	active := tx.Model(&MerchantStoreRefund{}).Select("id").Where("order_id = ? AND status IN ?", o.ID, []string{"awaiting_provider", "reconciliation_required"})
	if e := tx.Model(&MerchantStoreRefundItem{}).Where("order_id = ? AND refund_id IN (?)", o.ID, active).Pluck("stock_id", &ids).Error; e != nil {
		return nil, e
	}
	result := map[string]bool{}
	for _, id := range ids {
		result[id] = true
	}
	return result, nil
}

func storeRefundEligible(tx *gorm.DB, o *MerchantStoreOrder, u storeRefundUsage) ([]MerchantStoreStock, error) {
	var stock []MerchantStoreStock
	if e := storeOrderStock(tx, o).Where("state = ?", "delivered").Order("position ASC,id ASC").Find(&stock).Error; e != nil {
		return nil, e
	}
	retired, e := MerchantStoreCompletedRefundQuantity(tx, o)
	if e != nil {
		return nil, e
	}
	if len(stock)+int(retired) != o.Quantity {
		return nil, ErrMerchantStoreConflict
	}
	result := []MerchantStoreStock{}
	for _, s := range stock {
		if !u.stockIDs[s.ID] {
			result = append(result, s)
		}
	}
	return result, nil
}
func storeRefundView(tx *gorm.DB, o *MerchantStoreOrder) (*MerchantStoreRefundView, error) {
	if !storeRefundPaid(o) {
		return nil, ErrMerchantStoreConflict
	}
	b, e := storeRefundBasis(tx, o)
	if e != nil {
		return nil, e
	}
	rows, e := storeRefundRows(tx, o)
	if e != nil {
		return nil, e
	}
	u, e := storeRefundTotals(rows, o, b)
	if e != nil {
		return nil, e
	}
	items, e := storeRefundEligible(tx, o, u)
	if e != nil {
		return nil, e
	}
	n, e := MerchantStoreCompletedRefundQuantity(tx, o)
	if e != nil {
		return nil, e
	}
	v := &MerchantStoreRefundView{OrderID: o.ID, ProductTitle: o.ProductTitle, VariantName: o.VariantName, PaymentMethod: o.PaymentMethod, Currency: o.Currency, PrincipalQuota: o.PriceQuota, RefundedQuota: u.completed, ReservedQuota: u.reserved, RemainingQuota: o.PriceQuota - u.completed - u.reserved, Quantity: o.Quantity, RefundedQuantity: n, EligibleItems: []MerchantStoreRefundEligibleItem{}, SupportsQuantity: storeRefundPartialSupported(o, b), SupportsAmount: storeRefundPartialSupported(o, b), NativeBasisVerified: b != nil, Refunds: rows}
	if v.SupportsQuantity {
		for count := 1; count <= len(items); count++ {
			quota := storeRefundFloor(o.PriceQuota, int64(u.quantity+count), int64(o.Quantity)) - u.quantityQuota
			valid := quota > 0 && quota <= v.RemainingQuota
			if b != nil {
				native := int64(storeRefundFloor(int(b.AmountMinor), int64(u.quantity+count), int64(o.Quantity))) - u.quantityNative
				quota = storeRefundFloor(o.PriceQuota, u.nativeCompleted+u.nativeReserved+native, b.AmountMinor) - u.completed - u.reserved
				valid = native > 0 && native <= b.AmountMinor-u.nativeCompleted-u.nativeReserved && quota >= 0 && quota <= v.RemainingQuota
			}
			if valid {
				v.MaxQuantity = count
			}
		}
	}
	for _, s := range items {
		v.EligibleItems = append(v.EligibleItems, MerchantStoreRefundEligibleItem{s.ID, s.Position})
	}
	if b != nil {
		total, done, left := b.AmountMinor, u.nativeCompleted, b.AmountMinor-u.nativeCompleted-u.nativeReserved
		v.AmountMinor = &total
		v.RefundedAmountMinor = &done
		v.RemainingAmountMinor = &left
	}
	return v, nil
}
func GetMerchantStoreRefunds(actor int, id string) (*MerchantStoreRefundView, error) {
	var result *MerchantStoreRefundView
	e := storeOrderTx(id, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		if _, e := storeRefundAccess(tx, o, actor, false); e != nil {
			return e
		}
		var e error
		result, e = storeRefundView(tx, o)
		return e
	})
	return result, e
}
func GetMerchantStoreRefundsWithPickupProof(actor int, p MerchantStoreRefundPickupProof) (*MerchantStoreRefundView, error) {
	var result *MerchantStoreRefundView
	e := storeOrderTx(p.OrderID, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		if e := storeRefundProof(tx, o, actor, p); e != nil {
			return e
		}
		var e error
		result, e = storeRefundView(tx, o)
		return e
	})
	return result, e
}
func storeRefundFloor(principal int, native, total int64) int {
	value := new(big.Int).Mul(big.NewInt(int64(principal)), big.NewInt(native))
	value.Div(value, big.NewInt(total))
	return int(value.Int64())
}
func storeRefundCreate(tx *gorm.DB, o *MerchantStoreOrder, actor int, role string, in MerchantStoreRefundInput) (*MerchantStoreRefund, error) {
	encoded, _ := json.Marshal(in)
	digest := storeHash(string(encoded))
	id := storeHash(o.ID + ":" + fmtStoreActor(actor) + ":" + role + ":" + in.RequestKey)
	var existing MerchantStoreRefund
	if e := tx.First(&existing, "id = ?", id).Error; e == nil {
		if existing.InputDigest != digest || existing.RequestedBy != actor {
			return nil, ErrMerchantStoreConflict
		}
		existing.StockIDs = []string{}
		e = tx.Model(&MerchantStoreRefundItem{}).Where("refund_id = ? AND order_id = ?", id, o.ID).Order("stock_id ASC").Pluck("stock_id", &existing.StockIDs).Error
		return &existing, e
	} else if !errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, e
	}
	if !storeRefundPaid(o) || o.Status == "refunded" {
		return nil, ErrMerchantStoreConflict
	}
	b, e := storeRefundBasis(tx, o)
	if e != nil {
		return nil, e
	}
	rows, e := storeRefundRows(tx, o)
	if e != nil {
		return nil, e
	}
	u, e := storeRefundTotals(rows, o, b)
	if e != nil {
		return nil, e
	}
	left := o.PriceQuota - u.completed - u.reserved
	if left <= 0 {
		return nil, ErrMerchantStoreConflict
	}
	eligible, e := storeRefundEligible(tx, o, u)
	if e != nil {
		return nil, e
	}
	r := &MerchantStoreRefund{ID: id, OrderID: o.ID, RequestKey: in.RequestKey, InputDigest: digest, Mode: in.Mode, Reason: in.Reason, Status: "requested", RequestedBy: actor, RequestedRole: role, RetainedFeeQuota: o.FeeQuota, CreatedAt: common.GetTimestamp(), Currency: o.Currency, StockIDs: []string{}}
	var selected []MerchantStoreStock
	if in.Mode == "full" {
		r.PrincipalQuota = left
		selected = eligible
		if b != nil {
			r.AmountMinor = b.AmountMinor - u.nativeCompleted - u.nativeReserved
		}
	} else {
		if !storeRefundPartialSupported(o, b) {
			return nil, ErrMerchantStoreRefundUnsupported
		}
		if in.Mode == "quantity" {
			if in.Quantity > len(eligible) {
				return nil, ErrMerchantStoreConflict
			}
			if len(in.StockIDs) == 0 {
				selected = eligible[:in.Quantity]
			} else {
				wanted := map[string]bool{}
				for _, id := range in.StockIDs {
					wanted[id] = true
				}
				for _, s := range eligible {
					if wanted[s.ID] {
						selected = append(selected, s)
					}
				}
				if len(selected) != in.Quantity {
					return nil, ErrMerchantStoreConflict
				}
			}
			if o.PaymentMethod == "balance" {
				r.PrincipalQuota = storeRefundFloor(o.PriceQuota, int64(u.quantity+in.Quantity), int64(o.Quantity)) - u.quantityQuota
			} else {
				r.AmountMinor = int64(storeRefundFloor(int(b.AmountMinor), int64(u.quantity+in.Quantity), int64(o.Quantity))) - u.quantityNative
			}
		} else {
			if o.PaymentMethod == "balance" {
				if in.AmountQuota <= 0 || in.AmountMinor != 0 {
					return nil, ErrMerchantStoreInput
				}
				r.PrincipalQuota = in.AmountQuota
			} else {
				if in.AmountMinor <= 0 || in.AmountQuota != 0 {
					return nil, ErrMerchantStoreInput
				}
				r.AmountMinor = in.AmountMinor
			}
		}
		if o.PaymentMethod != "balance" {
			if r.AmountMinor <= 0 || r.AmountMinor > b.AmountMinor-u.nativeCompleted-u.nativeReserved {
				return nil, ErrMerchantStoreConflict
			}
			r.PrincipalQuota = storeRefundFloor(o.PriceQuota, u.nativeCompleted+u.nativeReserved+r.AmountMinor, b.AmountMinor) - u.completed - u.reserved
		}
	}
	if r.PrincipalQuota < 0 || r.PrincipalQuota == 0 && r.AmountMinor == 0 || r.PrincipalQuota > left {
		return nil, ErrMerchantStoreConflict
	}
	// A final amount adjustment also retires every remaining card. Otherwise the
	// terminal order would leave usable paid inventory outside the refund audit.
	if r.PrincipalQuota == left && u.reserved == 0 {
		selected = eligible
	}
	r.Quantity = len(selected)
	if e := tx.Create(r).Error; e != nil {
		return nil, e
	}
	for _, s := range selected {
		item := MerchantStoreRefundItem{ID: storeHash(r.ID + ":" + s.ID), RefundID: r.ID, OrderID: o.ID, StockID: s.ID, CreatedAt: r.CreatedAt}
		if e := tx.Create(&item).Error; e != nil {
			return nil, e
		}
		r.StockIDs = append(r.StockIDs, s.ID)
	}
	sort.Strings(r.StockIDs)
	return r, storeEvent(tx, actor, o.ID, "refund_requested")
}
func RequestMerchantStoreRefund(actor int, id string, in MerchantStoreRefundInput) (*MerchantStoreRefund, error) {
	if !storeRefundInputValid(in) {
		return nil, ErrMerchantStoreInput
	}
	var result *MerchantStoreRefund
	e := storeOrderTx(id, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		if e := MerchantStoreRefundRequiresWriter(tx); e != nil {
			return e
		}
		if e := marketLockUsers(tx, actor, o.SellerID, o.BuyerID); e != nil {
			return e
		}
		role, e := storeRefundAccess(tx, o, actor, false)
		if e != nil {
			return e
		}
		if actor != o.BuyerID {
			return ErrMerchantStoreDenied
		}
		result, e = storeRefundCreate(tx, o, actor, role, in)
		return e
	})
	return result, e
}
func RequestMerchantStoreRefundWithPickupProof(actor int, p MerchantStoreRefundPickupProof, in MerchantStoreRefundInput) (*MerchantStoreRefund, error) {
	if !storeRefundInputValid(in) {
		return nil, ErrMerchantStoreInput
	}
	var result *MerchantStoreRefund
	e := storeOrderTx(p.OrderID, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		if e := MerchantStoreRefundRequiresWriter(tx); e != nil {
			return e
		}
		if e := storeRefundProof(tx, o, actor, p); e != nil {
			return e
		}
		var e error
		result, e = storeRefundCreate(tx, o, o.BuyerID, "buyer", in)
		return e
	})
	return result, e
}
func storeRefundLoad(tx *gorm.DB, o *MerchantStoreOrder, id string) (*MerchantStoreRefund, error) {
	var r MerchantStoreRefund
	if e := tx.First(&r, "id = ? AND order_id = ?", id, o.ID).Error; e != nil {
		return nil, e
	}
	r.StockIDs = []string{}
	e := tx.Model(&MerchantStoreRefundItem{}).Where("refund_id = ? AND order_id = ?", id, o.ID).Order("stock_id ASC").Pluck("stock_id", &r.StockIDs).Error
	return &r, e
}
func storeRefundComplete(tx *gorm.DB, o *MerchantStoreOrder, r *MerchantStoreRefund, quota int) error {
	if quota < 0 || quota == 0 && r.AmountMinor == 0 {
		return ErrMerchantStoreConflict
	}
	var completed int
	if e := tx.Model(&MerchantStoreRefund{}).Where("order_id = ? AND status = ?", o.ID, "completed").Select("COALESCE(SUM(principal_quota),0)").Scan(&completed).Error; e != nil {
		return e
	}
	if completed > o.PriceQuota-quota {
		return ErrMerchantStoreConflict
	}
	if completed+quota == o.PriceQuota {
		var all []MerchantStoreStock
		if e := storeOrderStock(tx, o).Where("state = ?", "delivered").Find(&all).Error; e != nil {
			return e
		}
		selected := map[string]bool{}
		for _, id := range r.StockIDs {
			selected[id] = true
		}
		for _, stock := range all {
			if !selected[stock.ID] {
				item := MerchantStoreRefundItem{ID: storeHash(r.ID + ":" + stock.ID), RefundID: r.ID, OrderID: o.ID, StockID: stock.ID, CreatedAt: common.GetTimestamp()}
				if e := tx.Create(&item).Error; e != nil {
					return e
				}
				r.StockIDs = append(r.StockIDs, stock.ID)
			}
		}
		r.Quantity = len(r.StockIDs)
	}
	from, to, kind := o.SellerID, o.BuyerID, "refund"
	if o.PaymentMethod != "balance" {
		to = 0
		kind = "refund_external"
	}
	if e := marketLockUsers(tx, o.SellerID, o.BuyerID); e != nil {
		return e
	}
	if o.PaymentMethod == "balance" && o.SellerID == o.BuyerID {
		if e := ApplyWalletQuotaDelta(tx, o.SellerID, 0); e != nil {
			return e
		}
	} else if o.PaymentMethod == "balance" || strings.HasPrefix(o.PaymentMethod, "platform:") {
		var seller User
		if e := tx.First(&seller, o.SellerID).Error; e != nil {
			return e
		}
		if seller.Quota < quota {
			return ErrMerchantStoreBalance
		}
		if e := ApplyWalletQuotaDelta(tx, o.SellerID, -quota); e != nil {
			return e
		}
		if o.PaymentMethod == "balance" {
			if e := ApplyWalletQuotaDelta(tx, o.BuyerID, quota); e != nil {
				return e
			}
		}
	} else {
		from = 0
	}
	if e := tx.Create(&MerchantStoreTransfer{ID: r.ID + ":refund", OrderID: o.ID, FromUserID: from, ToUserID: to, Kind: kind, Quota: quota, CreatedAt: common.GetTimestamp()}).Error; e != nil {
		return e
	}
	if len(r.StockIDs) > 0 {
		update := storeOrderStock(tx, o).Model(&MerchantStoreStock{}).Where("id IN ? AND state = ?", r.StockIDs, "delivered").Update("state", "refunded")
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != int64(len(r.StockIDs)) {
			return ErrMerchantStoreConflict
		}
	}
	r.PrincipalQuota = quota
	r.Status = "completed"
	r.CompletedAt = common.GetTimestamp()
	if e := tx.Model(r).Updates(map[string]any{"principal_quota": quota, "quantity": r.Quantity, "status": r.Status, "completed_at": r.CompletedAt, "provider_refund_reference": r.ProviderRefundReference, "provider_evidence_hash": r.ProviderEvidenceHash}).Error; e != nil {
		return e
	}
	return storeRefundOrderStatus(tx, o)
}

// Reassign only outstanding quota reservations after cancellation or a native
// completion. Their native amounts stay immutable; completed credits remain
// the cumulative floor of actual returned native money, regardless of order.
func storeRefundNormalizeNativeReservations(tx *gorm.DB, o *MerchantStoreOrder) error {
	b, e := storeRefundBasis(tx, o)
	if e != nil || b == nil {
		return e
	}
	rows, e := storeRefundRows(tx, o)
	if e != nil {
		return e
	}
	var native int64
	var quota int
	for _, r := range rows {
		if r.Status == "completed" {
			if r.AmountMinor > b.AmountMinor-native {
				return ErrMerchantStoreConflict
			}
			native += r.AmountMinor
			quota += r.PrincipalQuota
		}
	}
	if quota != storeRefundFloor(o.PriceQuota, native, b.AmountMinor) {
		return ErrMerchantStoreConflict
	}
	for _, r := range rows {
		if !storeRefundActive(r.Status) || r.Status == "completed" {
			continue
		}
		if r.AmountMinor <= 0 || r.AmountMinor > b.AmountMinor-native {
			return ErrMerchantStoreConflict
		}
		native += r.AmountMinor
		next := storeRefundFloor(o.PriceQuota, native, b.AmountMinor)
		if e := tx.Model(&r).Update("principal_quota", next-quota).Error; e != nil {
			return e
		}
		quota = next
	}
	return nil
}

func storeRefundOrderStatus(tx *gorm.DB, o *MerchantStoreOrder) error {
	if e := storeRefundNormalizeNativeReservations(tx, o); e != nil {
		return e
	}
	rows, e := storeRefundRows(tx, o)
	if e != nil {
		return e
	}
	b, e := storeRefundBasis(tx, o)
	if e != nil {
		return e
	}
	u, e := storeRefundTotals(rows, o, b)
	if e != nil {
		return e
	}
	status := "paid"
	if u.completed == o.PriceQuota {
		status = "refunded"
		var stock []MerchantStoreStock
		if e := storeOrderStock(tx, o).Where("state = ?", "delivered").Find(&stock).Error; e != nil {
			return e
		}
		if len(stock) > 0 {
			return ErrMerchantStoreConflict
		}
	} else {
		for _, r := range rows {
			if r.Status == "awaiting_provider" || r.Status == "reconciliation_required" {
				status = "refund_pending"
				break
			}
		}
	}
	o.Status = status
	return tx.Model(o).Update("status", status).Error
}
func storeRefundDecision(tx *gorm.DB, o *MerchantStoreOrder, r *MerchantStoreRefund, actor int, in MerchantStoreRefundDecision) error {
	if e := marketLockUsers(tx, actor, o.SellerID, o.BuyerID); e != nil {
		return e
	}
	if _, e := storeRefundAccess(tx, o, actor, true); e != nil {
		return e
	}
	desired := "rejected"
	if in.Decision == "approve" {
		desired = "completed"
		if o.PaymentMethod != "balance" {
			desired = "awaiting_provider"
		}
	}
	if r.Status == desired || in.Decision == "approve" && r.Status == "completed" {
		return nil
	}
	if r.Status != "requested" || !storeRefundPaid(o) {
		return ErrMerchantStoreConflict
	}
	r.DecisionBy = actor
	r.DecisionReason = in.Reason
	r.DecidedAt = common.GetTimestamp()
	if e := tx.Model(r).Updates(map[string]any{"decision_by": r.DecisionBy, "decision_reason": r.DecisionReason, "decided_at": r.DecidedAt}).Error; e != nil {
		return e
	}
	if desired == "completed" {
		if e := storeRefundComplete(tx, o, r, r.PrincipalQuota); e != nil {
			return e
		}
	} else {
		r.Status = desired
		if e := tx.Model(r).Update("status", desired).Error; e != nil {
			return e
		}
		if e := storeRefundOrderStatus(tx, o); e != nil {
			return e
		}
	}
	return storeEvent(tx, actor, o.ID, "refund_"+in.Decision)
}
func DecideMerchantStoreRefund(actor int, id, refundID string, in MerchantStoreRefundDecision) (*MerchantStoreRefund, error) {
	if (in.Decision != "approve" && in.Decision != "reject") || !storeRefundText(in.Reason, false) {
		return nil, ErrMerchantStoreInput
	}
	var result *MerchantStoreRefund
	e := storeOrderTx(id, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		if e := MerchantStoreRefundRequiresWriter(tx); e != nil {
			return e
		}
		var e error
		result, e = storeRefundLoad(tx, o, refundID)
		if e != nil {
			return e
		}
		return storeRefundDecision(tx, o, result, actor, in)
	})
	if e == nil {
		marketInvalidate(actor)
		var o MerchantStoreOrder
		if DB.First(&o, "id = ?", id).Error == nil {
			marketInvalidate(o.SellerID, o.BuyerID)
		}
	}
	return result, e
}
func ProactivelyRefundMerchantStoreOrder(actor int, id string, in MerchantStoreRefundInput) (*MerchantStoreRefund, error) {
	if !storeRefundInputValid(in) {
		return nil, ErrMerchantStoreInput
	}
	var result *MerchantStoreRefund
	e := storeOrderTx(id, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		if e := MerchantStoreRefundRequiresWriter(tx); e != nil {
			return e
		}
		if e := marketLockUsers(tx, actor, o.SellerID, o.BuyerID); e != nil {
			return e
		}
		role, e := storeRefundAccess(tx, o, actor, true)
		if e != nil {
			return e
		}
		result, e = storeRefundCreate(tx, o, actor, role, in)
		if e != nil {
			return e
		}
		return storeRefundDecision(tx, o, result, actor, MerchantStoreRefundDecision{Decision: "approve", Reason: in.Reason})
	})
	if e == nil {
		var o MerchantStoreOrder
		if DB.First(&o, "id = ?", id).Error == nil {
			marketInvalidate(o.SellerID, o.BuyerID)
		}
	}
	return result, e
}
func CancelMerchantStoreRefund(actor int, id, refundID string) (*MerchantStoreRefund, error) {
	var result *MerchantStoreRefund
	e := storeOrderTx(id, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		if e := MerchantStoreRefundRequiresWriter(tx); e != nil {
			return e
		}
		if _, e := storeRefundAccess(tx, o, actor, false); e != nil {
			return e
		}
		if actor != o.BuyerID {
			return ErrMerchantStoreDenied
		}
		var e error
		result, e = storeRefundLoad(tx, o, refundID)
		if e != nil {
			return e
		}
		if result.Status == "cancelled" {
			return nil
		}
		if result.Status != "requested" {
			return ErrMerchantStoreConflict
		}
		result.Status = "cancelled"
		if e := tx.Model(result).Update("status", result.Status).Error; e != nil {
			return e
		}
		if e := storeRefundOrderStatus(tx, o); e != nil {
			return e
		}
		return storeEvent(tx, actor, id, "refund_cancelled")
	})
	return result, e
}
