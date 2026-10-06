package model

import (
	"crypto/rand"
	"errors"
	"io"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	storeTradeNoAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	storeTradeNoLength   = 30
	storeTradeNoRetries  = 5
)

// Keep provider-facing order numbers within the 32-character Epay boundary.
// Thirty independent Base62 characters carry approximately 178.6 random bits.
func storeRandomTradeNo(reader io.Reader) (string, error) {
	var randomByte [1]byte
	var code [storeTradeNoLength]byte
	for i := 0; i < len(code); {
		if _, err := io.ReadFull(reader, randomByte[:]); err != nil {
			return "", err
		}
		// 248 is the largest multiple of 62 below 256. Reject the remainder
		// so every character has the same probability.
		if randomByte[0] >= 248 {
			continue
		}
		code[i] = storeTradeNoAlphabet[int(randomByte[0])%len(storeTradeNoAlphabet)]
		i++
	}
	return "MS" + string(code[:]), nil
}

func storeValidTradeNo(trade string) bool {
	if len(trade) != 2+storeTradeNoLength || !strings.HasPrefix(trade, "MS") {
		return false
	}
	for i := 2; i < len(trade); i++ {
		ch := trade[i]
		if !(ch >= '0' && ch <= '9' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z') {
			return false
		}
	}
	return true
}

func storeCreateOrderWithRandomTradeNo(tx *gorm.DB, order *MerchantStoreOrder) error {
	return storeCreateOrderWithTradeNoGenerator(tx, order, func() (string, error) {
		return storeRandomTradeNo(rand.Reader)
	})
}

func storeCreateOrderWithTradeNoGenerator(tx *gorm.DB, order *MerchantStoreOrder, generate func() (string, error)) error {
	for attempt := 0; attempt < storeTradeNoRetries; attempt++ {
		trade, err := generate()
		if err != nil {
			return err
		}
		if !storeValidTradeNo(trade) {
			return ErrMerchantStoreInput
		}
		order.TradeNo = trade
		// Let the database's unique index arbitrate concurrent collisions.
		// DO NOTHING leaves PostgreSQL transactions usable for the retry.
		result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "trade_no"}}, DoNothing: true}).Create(order)
		if result.Error != nil {
			return result.Error
		}
		// MySQL's DO NOTHING emulation can report a matched row depending on
		// clientFoundRows. Verify the actual inserted identity, not RowsAffected.
		var persisted MerchantStoreOrder
		err = tx.Select("id", "trade_no").Where("id = ?", order.ID).Take(&persisted).Error
		if err == nil {
			if persisted.TradeNo != trade {
				return ErrMerchantStoreConflict
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var collisions int64
		if err := tx.Model(&MerchantStoreOrder{}).Where("trade_no = ?", trade).Count(&collisions).Error; err != nil {
			return err
		}
		if collisions == 0 {
			// Another unique field conflicted; changing the public number
			// must not disguise that error or repeat financial side effects.
			return ErrMerchantStoreConflict
		}
	}
	return ErrMerchantStoreConflict
}
