// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package middleware

import "github.com/gin-gonic/gin"

// Authenticated chat must not consume the login/payment IP limit. Separating
// receipts from messages also keeps reading a busy inbox from blocking replies.
func MerchantStoreSupportRateLimit(scope string) gin.HandlerFunc {
	var maximum int
	switch scope {
	case "create":
		maximum = 20
	case "message":
		maximum = 60
	case "receipt":
		maximum = 180
	case "manage":
		maximum = 30
	default:
		panic("unknown merchant store support rate limit scope")
	}
	return userRateLimitFactory(maximum, 60, "STORE_SUPPORT:"+scope)
}
