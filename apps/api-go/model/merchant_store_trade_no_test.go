package model

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMerchantStoreTradeNoRandomEncodingAndRejection(t *testing.T) {
	// Bytes 248-255 must be rejected instead of biasing the first characters.
	entropy := append([]byte{248, 249, 250, 251, 252, 253, 254, 255}, bytes.Repeat([]byte{61}, storeTradeNoLength)...)
	trade, err := storeRandomTradeNo(bytes.NewReader(entropy))
	require.NoError(t, err)
	require.Equal(t, "MS"+strings.Repeat("z", 30), trade)
	require.True(t, storeValidTradeNo(trade))

	_, err = storeRandomTradeNo(bytes.NewReader(nil))
	require.ErrorIs(t, err, io.EOF)
}

func TestMerchantStoreTradeNoUsesFullAlphabet(t *testing.T) {
	for _, start := range []byte{0, 30, 60, 124, 186} {
		trade, err := storeRandomTradeNo(bytes.NewReader(bytes.Repeat([]byte{start}, storeTradeNoLength)))
		require.NoError(t, err)
		require.Equal(t, "MS"+strings.Repeat(string(storeTradeNoAlphabet[int(start)%62]), 30), trade)
	}
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		trade, err := storeRandomTradeNo(rand.Reader)
		require.NoError(t, err)
		require.Len(t, trade, 32)
		require.True(t, storeValidTradeNo(trade))
		require.False(t, seen[trade])
		seen[trade] = true
	}
}

func TestMerchantStoreTradeNoPreservesLegacyAndRejectsUnsafeInput(t *testing.T) {
	for _, trade := range []string{"MS" + strings.Repeat("a", 30), "MS" + strings.Repeat("b9", 15), "MS" + strings.Repeat("Az0", 10)} {
		require.True(t, storeValidTradeNo(trade), trade)
	}
	for _, trade := range []string{"", "ms" + strings.Repeat("a", 30), "MS" + strings.Repeat("a", 29), "MS" + strings.Repeat("a", 31), "MS" + strings.Repeat("a", 29) + "-", "MS" + strings.Repeat("a", 29) + "_", "MS" + strings.Repeat("a", 29) + " ", "MS" + strings.Repeat("a", 27) + "中"} {
		require.False(t, storeValidTradeNo(trade), trade)
	}
}

func tradeNoTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := marketTestDB(t)
	require.NoError(t, db.AutoMigrate(&MerchantStoreOrder{}))
	return db
}

func TestMerchantStoreTradeNoDatabaseCollisionRetries(t *testing.T) {
	db := tradeNoTestDB(t)
	checkMerchantStoreTradeNoCollisionRetries(t, db)
}

func TestMerchantStoreTradeNoPostgresCollisionRetries(t *testing.T) {
	db, _, _, _ := merchantStorePGDB(t)
	checkMerchantStoreTradeNoCollisionRetries(t, db)
}

func checkMerchantStoreTradeNoCollisionRetries(t *testing.T, db *gorm.DB) {
	t.Helper()
	oldTrade := "MS" + strings.Repeat("a", 30)
	old := MerchantStoreOrder{ID: "old", TradeNo: oldTrade, PickupTokenHash: "old-token"}
	require.NoError(t, db.Create(&old).Error)
	order := MerchantStoreOrder{ID: "new", PickupTokenHash: "new-token"}
	calls := 0
	newTrade := "MS" + strings.Repeat("Z", 30)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return storeCreateOrderWithTradeNoGenerator(tx, &order, func() (string, error) {
			calls++
			if calls == 1 {
				return oldTrade, nil
			}
			return newTrade, nil
		})
	}))
	require.Equal(t, 2, calls)
	require.Equal(t, newTrade, order.TradeNo)
	var orders []MerchantStoreOrder
	require.NoError(t, db.Order("id").Find(&orders).Error)
	require.Len(t, orders, 2)
	var kept MerchantStoreOrder
	require.NoError(t, db.First(&kept, "id = ?", old.ID).Error)
	require.Equal(t, oldTrade, kept.TradeNo)
}

func TestMerchantStoreTradeNoCollisionLimitAndEntropyFailuresDoNotInsert(t *testing.T) {
	db := tradeNoTestDB(t)
	trade := "MS" + strings.Repeat("a", 30)
	old := MerchantStoreOrder{ID: "old", TradeNo: trade, PickupTokenHash: "old-token"}
	require.NoError(t, db.Create(&old).Error)
	order := MerchantStoreOrder{ID: "new", PickupTokenHash: "new-token"}
	calls := 0
	err := db.Transaction(func(tx *gorm.DB) error {
		return storeCreateOrderWithTradeNoGenerator(tx, &order, func() (string, error) {
			calls++
			return trade, nil
		})
	})
	require.ErrorIs(t, err, ErrMerchantStoreConflict)
	require.Equal(t, storeTradeNoRetries, calls)
	entropyErr := errors.New("entropy unavailable")
	err = storeCreateOrderWithTradeNoGenerator(db, &order, func() (string, error) { return "", entropyErr })
	require.ErrorIs(t, err, entropyErr)
	var count int64
	require.NoError(t, db.Model(&MerchantStoreOrder{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestMerchantStoreTradeNoOtherConstraintFailureDoesNotRetry(t *testing.T) {
	db := tradeNoTestDB(t)
	old := MerchantStoreOrder{ID: "old", TradeNo: "MS" + strings.Repeat("a", 30), PickupTokenHash: "shared-token"}
	require.NoError(t, db.Create(&old).Error)
	order := MerchantStoreOrder{ID: "new", PickupTokenHash: "shared-token"}
	calls := 0
	err := db.Transaction(func(tx *gorm.DB) error {
		return storeCreateOrderWithTradeNoGenerator(tx, &order, func() (string, error) {
			calls++
			return "MS" + strings.Repeat("Z", 30), nil
		})
	})
	require.Error(t, err)
	require.Equal(t, 1, calls)
	var count int64
	require.NoError(t, db.Model(&MerchantStoreOrder{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestMerchantStoreTradeNoCheckoutIsRandomAndReplayStable(t *testing.T) {
	f := newStoreFixture(t, "balance")
	input := f.checkout("buyer-controlled-request", "balance")
	input.PickupEmail = "store-buyer@example.test"
	order, created, err := CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	require.True(t, created)
	require.True(t, storeValidTradeNo(order.TradeNo))
	require.Len(t, order.TradeNo, 32)
	require.NotEqual(t, "MS"+storeHash("order:" + fmtStoreActor(input.BuyerID) + ":" + input.RequestKey)[:30], order.TradeNo)

	replay, created, err := CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, order.ID, replay.ID)
	require.Equal(t, order.TradeNo, replay.TradeNo)
	found, err := GetMerchantStorePaymentOrderByTradeNo(order.TradeNo)
	require.NoError(t, err)
	require.Equal(t, order.ID, found.ID)

	input.RequestKey = "another-buyer-controlled-request"
	next, created, err := CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	require.True(t, created)
	require.NotEqual(t, order.TradeNo, next.TradeNo)
}

func TestMerchantStoreTradeNoLegacyLookupRemainsExact(t *testing.T) {
	db := tradeNoTestDB(t)
	old := MerchantStoreOrder{ID: "old", TradeNo: "MS" + strings.Repeat("ab09ef", 5), PickupTokenHash: "old-token"}
	require.NoError(t, db.Create(&old).Error)
	found, err := GetMerchantStorePaymentOrderByTradeNo(old.TradeNo)
	require.NoError(t, err)
	require.Equal(t, old.ID, found.ID)
	_, err = GetMerchantStorePaymentOrderByTradeNo(strings.ToUpper(old.TradeNo))
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}
