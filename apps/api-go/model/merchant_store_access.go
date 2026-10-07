package model

import (
	"errors"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

var ErrMerchantStoreLoginRequired = errors.New("store purchase requires an account")

// Empty visibility is the migration representation of an older product. Once
// written by the new editor, visibility is canonical and test_mode is an alias.
func MerchantStoreProductVisibility(p *MerchantStoreProduct) string {
	if p.Visibility != "" {
		return p.Visibility
	}
	if p.TestMode {
		return "private"
	}
	return "public"
}

const storeVisibilitySQL = `(CASE WHEN visibility <> '' THEN visibility WHEN test_mode THEN 'private' ELSE 'public' END)`

// Apply this before sorting, counts and pagination. actor must come from the
// server's authentication middleware, never from a request viewer ID. A guest
// token does not make the viewer a registered account.
func MerchantStoreVisibleProductsForViewer(tx *gorm.DB, actor int) *gorm.DB {
	return storeProductsForViewer(tx, actor, []string{"published"}, []string{"draft", "pending", "published"})
}

// Saved collections retain temporarily paused products while using the same
// account and privacy boundary. This scope never grants checkout permission.
func MerchantStoreRetainedProductsForViewer(tx *gorm.DB, actor int) *gorm.DB {
	return storeProductsForViewer(tx, actor, []string{"published", "paused"}, []string{"draft", "pending", "published", "paused"})
}

func storeProductsForViewer(tx *gorm.DB, actor int, sharedStatuses, privateStatuses []string) *gorm.DB {
	active, err := storeAccessReadActive(tx)
	if err != nil {
		query := tx.Model(&MerchantStoreProduct{})
		query.AddError(err)
		return query
	}
	if actor < 1 {
		actor = 0
	}
	enabledActor := `EXISTS (SELECT 1 FROM users AS viewer WHERE viewer.id = ? AND viewer.status = ? AND viewer.role >= ? AND viewer.deleted_at IS NULL)`
	visibility := `(CASE WHEN test_mode THEN 'private' ELSE 'public' END)`
	if active {
		visibility = storeVisibilitySQL
	}
	return tx.Model(&MerchantStoreProduct{}).
		Where("EXISTS (SELECT 1 FROM users WHERE users.id = merchant_store_products.seller_id AND users.status = ? AND users.role >= ? AND users.deleted_at IS NULL)", common.UserStatusEnabled, common.RoleCommonUser).
		Where("("+visibility+" = 'public' AND status IN ?) OR ("+visibility+" = 'registered' AND status IN ? AND "+enabledActor+") OR ("+visibility+" = 'private' AND seller_id = ? AND status IN ? AND "+enabledActor+")", sharedStatuses, sharedStatuses, actor, common.UserStatusEnabled, common.RoleCommonUser, actor, privateStatuses, actor, common.UserStatusEnabled, common.RoleCommonUser)
}

// Only a known earlier floor permits the legacy-column reader. An unknown or
// damaged gate must not reclassify stored registered products as public.
func storeAccessReadActive(tx *gorm.DB) (bool, error) {
	required, err := storeWriterGateRow(tx.Session(&gorm.Session{NewDB: true}), "")
	if err != nil || required > MerchantStoreWriterCapability {
		return false, ErrMerchantStoreWriterFrozen
	}
	return required >= 5, nil
}

func storeAccessActive(tx *gorm.DB) bool {
	required, e := storeWriterGateRow(tx.Session(&gorm.Session{NewDB: true}), "")
	return e == nil && required >= 5 && required <= MerchantStoreWriterCapability
}

// The candidate reader/writer can run before the separate access DDL phase.
// All product/order saves, including callbacks and AI updates, preserve the
// old schema until the formal capability floor activates these columns.
func (p *MerchantStoreProduct) BeforeSave(tx *gorm.DB) error {
	required, err := storeWriterGateRow(tx.Session(&gorm.Session{NewDB: true}), "")
	if err != nil || required < 6 || required > MerchantStoreWriterCapability {
		tx.Statement.Omits = append(tx.Statement.Omits, "category_id")
	}
	if err != nil || required < 4 || required > MerchantStoreWriterCapability {
		tx.Statement.Omits = append(tx.Statement.Omits, "max_quantity_per_order", "max_quantity_per_buyer")
	}
	if err != nil || required < 5 || required > MerchantStoreWriterCapability {
		tx.Statement.Omits = append(tx.Statement.Omits, "visibility", "purchase_login_required")
	}
	return nil
}

func (o *MerchantStoreOrder) BeforeSave(tx *gorm.DB) error {
	required, err := storeWriterGateRow(tx.Session(&gorm.Session{NewDB: true}), "")
	if err != nil || required < 4 || required > MerchantStoreWriterCapability {
		tx.Statement.Omits = append(tx.Statement.Omits, "original_price_quota", "discount_quota", "discount_bps", "promotion_id", "promotion_code")
	}
	if err != nil || required < 5 || required > MerchantStoreWriterCapability {
		tx.Statement.Omits = append(tx.Statement.Omits, "guest_id", "seller_terms_version", "seller_terms_content", "seller_terms_accepted_at")
	}
	return nil
}

func MerchantStoreAccessSupported() bool {
	return storeAccessSupported(DB)
}

func storeAccessSupported(tx *gorm.DB) bool {
	required, e := storeWriterGateRow(tx, "")
	if e != nil || required < 5 || required > MerchantStoreWriterCapability {
		return false
	}
	for _, item := range []any{&MerchantStoreGuest{}, &MerchantStoreSellerTerms{}, &MerchantStoreTermsAcceptance{}, &MerchantStoreGuestEmailVerification{}} {
		if !tx.Migrator().HasTable(item) {
			return false
		}
	}
	for _, check := range []struct {
		model  any
		column string
	}{
		{&MerchantStoreProduct{}, "visibility"}, {&MerchantStoreProduct{}, "purchase_login_required"},
		{&MerchantStoreOrder{}, "guest_id"}, {&MerchantStoreOrder{}, "seller_terms_version"},
		{&MerchantStoreOrder{}, "seller_terms_content"}, {&MerchantStoreOrder{}, "seller_terms_accepted_at"},
	} {
		if !tx.Migrator().HasColumn(check.model, check.column) {
			return false
		}
	}
	return true
}

func MerchantStoreAccessRequiresWriter(tx *gorm.DB) error {
	required, e := storeWriterGateRow(tx, "SHARE")
	if e != nil || required < 5 || required > MerchantStoreWriterCapability {
		return ErrMerchantStoreWriterFrozen
	}
	return nil
}

func storeApplyProductAccess(tx *gorm.DB, p *MerchantStoreProduct, in MerchantStoreProductInput, creating bool) error {
	if !storeAccessActive(tx) {
		p.Visibility = ""
		p.PurchaseLoginRequired = true
	}
	visibility := MerchantStoreProductVisibility(p)
	if in.Visibility != nil {
		visibility = *in.Visibility
	}
	if in.TestMode != nil {
		if in.Visibility != nil && *in.TestMode != (visibility == "private") {
			return ErrMerchantStoreInput
		}
		if *in.TestMode {
			visibility = "private"
		} else if visibility == "private" {
			// False means nonprivate; old editors must preserve registered.
			visibility = "public"
		}
	}
	if visibility != "public" && visibility != "registered" && visibility != "private" {
		return ErrMerchantStoreInput
	}
	if in.Visibility != nil || in.PurchaseLoginRequired != nil {
		if e := MerchantStoreAccessRequiresWriter(tx); e != nil {
			return e
		}
	}
	if creating {
		p.PurchaseLoginRequired = true
	}
	if in.PurchaseLoginRequired != nil {
		p.PurchaseLoginRequired = *in.PurchaseLoginRequired
	}
	if !p.PurchaseLoginRequired && in.PickupLoginRequired {
		return ErrMerchantStoreInput
	}
	p.Visibility, p.TestMode = visibility, visibility == "private"
	return nil
}

// Shared checkout/quote policy. The server resolves the guest bearer; callers
// cannot turn an arbitrary guest ID or viewer ID into purchase authorization.
func CheckMerchantStoreProductPurchaseAccess(tx *gorm.DB, p *MerchantStoreProduct, actor int, guestToken string) (string, error) {
	if actor < 0 {
		return "", ErrMerchantStoreDenied
	}
	if actor > 0 {
		if guestToken != "" {
			return "", ErrMerchantStoreDenied
		}
		if _, e := storeUser(tx, actor, common.RoleCommonUser); e != nil {
			return "", e
		}
		return "", storeProductNewBuyer(p, actor)
	}
	if e := storeProductNewBuyer(p, 0); e != nil {
		return "", e
	}
	if p.PickupLoginRequired {
		return "", ErrMerchantStoreLoginRequired
	}
	if e := MerchantStoreAccessRequiresWriter(tx); e != nil {
		return "", e
	}
	guest, e := ResolveMerchantStoreGuest(tx, guestToken)
	if e != nil {
		return "", e
	}
	return guest.ID, nil
}
