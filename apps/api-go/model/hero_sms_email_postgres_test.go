package model

import (
	"os"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// pi-lens-ignore: ast-grep:go-test-functions
func TestHeroSMSPostgresLedgerAndIdempotency(t *testing.T) {
	dsn := os.Getenv("HERO_SMS_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("HERO_SMS_POSTGRES_TEST_DSN is not configured")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{TranslateError: true})
	require.NoError(t, err)
	previousDB := DB
	previousRedis := common.RedisEnabled
	previousOptions := common.OptionMap
	DB = db
	common.RedisEnabled = false
	common.OptionMap = map[string]string{}
	t.Cleanup(func() {
		DB = previousDB
		common.RedisEnabled = previousRedis
		common.OptionMap = previousOptions
	})

	require.NoError(t, db.AutoMigrate(&User{}, &Option{}, &HeroSMSEmailOrder{}, &HeroSMSEmailActivation{}, &HeroSMSEmailQuotaLedger{}, &HeroSMSProviderPurchaseLease{}))
	user := User{Id: 9001, Username: "hero-postgres-user", Password: "password123", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 1000, Group: "default", AffCode: "hero-postgres-aff"}
	require.NoError(t, db.Create(&user).Error)

	order := HeroSMSEmailOrder{ID: "postgres-order", UserID: user.Id, Operation: "purchase", IdempotencyKeyHash: "postgres-idem", RequestPayloadHash: "postgres-payload", DomainID: "postgres-domain", Site: "demo.com", Domain: "mail.test", Quantity: 2, Status: HeroSMSEmailOrderStatusPendingProvider, ChargeQuota: 101}
	activations := []HeroSMSEmailActivation{
		{ID: "postgres-activation-1", UserID: user.Id, Slot: 1, Status: HeroSMSEmailActivationStatusPendingProvider, DomainID: order.DomainID, Site: order.Site, Domain: order.Domain, ChargeQuota: 51},
		{ID: "postgres-activation-2", UserID: user.Id, Slot: 2, Status: HeroSMSEmailActivationStatusPendingProvider, DomainID: order.DomainID, Site: order.Site, Domain: order.Domain, ChargeQuota: 50},
	}
	newQuota, err := reserveHeroSMSEmailQuota(&order, activations)
	require.NoError(t, err)
	require.Equal(t, 899, newQuota)

	duplicate := order
	duplicate.ID = "postgres-order-duplicate"
	duplicateQuota, err := reserveHeroSMSEmailQuota(&duplicate, activations)
	require.Error(t, err)
	require.Zero(t, duplicateQuota)
	var afterReserve User
	require.NoError(t, db.First(&afterReserve, user.Id).Error)
	require.Equal(t, 899, afterReserve.Quota)

	var storedActivations []HeroSMSEmailActivation
	require.NoError(t, db.Where("order_id = ?", order.ID).Order("slot asc").Find(&storedActivations).Error)
	require.Len(t, storedActivations, 2)
	for range 2 {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			return heroSMSRefundActivationTx(tx, &order, &storedActivations[0], 51, "postgres-refund")
		}))
	}
	var afterRefund User
	require.NoError(t, db.First(&afterRefund, user.Id).Error)
	require.Equal(t, 950, afterRefund.Quota)
	var storedOrder HeroSMSEmailOrder
	require.NoError(t, db.First(&storedOrder, "id = ?", order.ID).Error)
	require.Equal(t, 51, storedOrder.RefundedQuota)
	var refundLedgers int64
	require.NoError(t, db.Model(&HeroSMSEmailQuotaLedger{}).Where("order_id = ? AND entry_type = ?", order.ID, HeroSMSEmailLedgerRefund).Count(&refundLedgers).Error)
	require.Equal(t, int64(1), refundLedgers)
}
