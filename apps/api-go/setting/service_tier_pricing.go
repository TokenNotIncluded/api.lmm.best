package setting

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/pkg/servicetier"
)

// Each request owns its decoded snapshot. There is no default price catalog.
func ServiceTierPricing() (servicetier.Policy, servicetier.Catalog, error) {
	common.OptionMapRWMutex.RLock()
	policyValue := common.OptionMap[servicetier.PolicyOption]
	catalogValue := common.OptionMap[servicetier.CatalogOption]
	common.OptionMapRWMutex.RUnlock()
	policy := servicetier.DefaultPolicy()
	var catalog servicetier.Catalog
	var err error
	if policyValue != "" {
		policy, err = servicetier.DecodePolicy(policyValue)
		if err != nil {
			return policy, catalog, err
		}
	}
	if catalogValue != "" {
		catalog, err = servicetier.DecodeCatalog(catalogValue)
	}
	return policy, catalog, err
}
func ValidateServiceTierOption(key, value string) error {
	switch key {
	case servicetier.PolicyOption:
		_, err := servicetier.DecodePolicy(value)
		return err
	case servicetier.CatalogOption:
		_, err := servicetier.DecodeCatalog(value)
		return err
	}
	return nil
}
