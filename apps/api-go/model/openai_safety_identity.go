package model

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var ErrOpenAISafetyIdentityUnavailable = errors.New("private safety identity unavailable")

// OpenAIPrivateSafetyIdentifier identifies an authenticated internal account
// without disclosing its numeric ID or any profile fields. The installation
// key is persisted by the existing conflict-safe risk-key initializer, so all
// instances sharing the database use the same identity across restarts.
// No key is cached: a database switch cannot reuse another installation's key.
func OpenAIPrivateSafetyIdentifier(ctx context.Context, userID int) (string, error) {
	if userID <= 0 || DB == nil {
		return "", ErrOpenAISafetyIdentityUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	// The initializer may INSERT the private key. Never allow GORM's SQL/error
	// logger to interpolate that value into a log, including initialization errors.
	db := DB.Session(&gorm.Session{Logger: DB.Logger.LogMode(gormlogger.Silent)}).WithContext(ctx)
	secret, err := getAssistantGiftRiskSecret(db)
	if err != nil {
		return "", ErrOpenAISafetyIdentityUnavailable
	}
	// A distinct, fixed purpose domain separates provider-facing identity from
	// email/network risk hashes made with the same durable installation key.
	return common.GenerateHMACWithKey([]byte(secret), "openai-safety-identifier-v1:"+strconv.Itoa(userID)), nil
}
