package service

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"os"
	"strings"
)

const MerchantStoreExtorePurpose = "store_extore_import"
const merchantExtoreEnvelope = "merchant:"

func EncryptMerchantStoreExtoreFlow(value string) (string, error) {
	if strings.TrimSpace(os.Getenv("MERCHANT_STORE_ENCRYPTION_KEY")) != "" {
		encrypted, err := common.EncryptPersistentString(MerchantStoreExtorePurpose, "MERCHANT_STORE_ENCRYPTION_KEY", "", value)
		if err != nil {
			return "", err
		}
		return merchantExtoreEnvelope + encrypted, nil
	}
	return common.EncryptPersistentString(MerchantStoreExtorePurpose, "CRYPTO_SECRET", "SESSION_SECRET", value)
}

func DecryptMerchantStoreExtoreFlow(value string) (string, error) {
	if strings.HasPrefix(value, merchantExtoreEnvelope) {
		return common.DecryptPersistentString(MerchantStoreExtorePurpose, "MERCHANT_STORE_ENCRYPTION_KEY", "", strings.TrimPrefix(value, merchantExtoreEnvelope))
	}
	// Existing v1 flows use the original CRYPTO_SECRET / SESSION_SECRET choice.
	return common.DecryptPersistentString(MerchantStoreExtorePurpose, "CRYPTO_SECRET", "SESSION_SECRET", value)
}
