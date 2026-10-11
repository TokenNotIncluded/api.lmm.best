package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// waffoPancakeRequestKey identifies one local operation across provider retries.
// Include its merchant and immutable business identity in values. Do not use a
// clock window or a random value: a timeout must not create a second operation.
func waffoPancakeRequestKey(operation string, values ...string) string {
	// Marshaling a string slice cannot fail. JSON preserves field boundaries,
	// unlike concatenation, and no credential is included in the key.
	encoded, _ := json.Marshal(append([]string{operation}, values...))
	sum := sha256.Sum256(encoded)
	return "lmm_" + hex.EncodeToString(sum[:])
}

// waffoPancakeReportedAmount keeps an explicit zero distinct from missing
// payment-channel evidence. A modern price snapshot is not proof of a charge
// or refund. Only a legacy payload without that snapshot may use legacyAmount.
func waffoPancakeReportedAmount(reported *string, legacyAmount string, hasPriceSnapshot bool) string {
	if reported != nil {
		return *reported
	}
	if hasPriceSnapshot {
		return ""
	}
	return legacyAmount
}
