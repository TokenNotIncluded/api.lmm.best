package model

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"gorm.io/gorm"
)

type heroSMSOptionWriteContextKey struct{}

func heroSMSOptionWriteContext() context.Context {
	return context.WithValue(context.TODO(), heroSMSOptionWriteContextKey{}, true)
}

func (option *Option) BeforeSave(tx *gorm.DB) error {
	if option == nil || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(option.Key)), "hero_sms.") {
		return nil
	}
	allowed, _ := tx.Statement.Context.Value(heroSMSOptionWriteContextKey{}).(bool)
	if !allowed {
		return errors.New("HeroSMS settings must be managed via /api/option/hero-sms")
	}
	return nil
}

func heroSMSOptionValue(key string, fallback string) string {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	value, exists := common.OptionMap[key]
	if !exists || strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func heroSMSConfiguredAPIKey() (string, error) {
	ciphertext := heroSMSOptionValue(setting.HeroSMSOptionAPIKey, "")
	if ciphertext == "" {
		return "", nil
	}
	apiKey, err := common.DecryptPersistentString(
		"hero_sms.api_key",
		"HERO_SMS_ENCRYPTION_KEY",
		"CRYPTO_SECRET",
		ciphertext,
	)
	if err != nil {
		return "", fmt.Errorf("decrypt HeroSMS API key: %w", err)
	}
	return apiKey, nil
}

func heroSMSPurchasingEnabled() bool {
	enabled, err := strconv.ParseBool(heroSMSOptionValue(setting.HeroSMSOptionEnabled, "false"))
	return err == nil && enabled
}

func updateHeroSMSOptionCache(values map[string]string) {
	common.OptionMapRWMutex.Lock()
	defer common.OptionMapRWMutex.Unlock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	for key, value := range values {
		common.OptionMap[key] = value
	}
}

func deleteHeroSMSAPIKeyFromCache() {
	common.OptionMapRWMutex.Lock()
	defer common.OptionMapRWMutex.Unlock()
	delete(common.OptionMap, setting.HeroSMSOptionAPIKey)
}
