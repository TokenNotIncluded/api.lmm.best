package setting

import (
	"encoding/json"
	"errors"

	"github.com/LIghtJUNction/api.lmm.best/internal/commerceimport"
)

const MerchantStoreCommerceImportEnabledOption = "MerchantStoreCommerceImportEnabled"
const MerchantStoreCommerceImportTrustedOriginsOption = "MerchantStoreCommerceImportTrustedOrigins"

func ValidateCommerceImportOption(key, value string) error {
	switch key {
	case MerchantStoreCommerceImportEnabledOption:
		if value != "true" && value != "false" {
			return errors.New("commerce import activation must be true or false")
		}
	case MerchantStoreCommerceImportTrustedOriginsOption:
		var origins []string
		if len(value) > 32<<10 || json.Unmarshal([]byte(value), &origins) != nil || origins == nil || len(origins) > 50 {
			return errors.New("commerce trusted origins must be an array of up to 50 public HTTPS origins")
		}
		seen := map[string]bool{}
		for _, origin := range origins {
			if commerceimport.ValidateOrigin(origin) != nil || seen[origin] {
				return errors.New("commerce trusted origins must be unique public HTTPS origins")
			}
			seen[origin] = true
		}
	}
	return nil
}
