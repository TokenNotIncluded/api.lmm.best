package deploycli

import "context"

// Historical transaction-component fixtures intentionally describe releases
// without a merchant gate. They do not qualify those releases for deployment.
// This explicit replacement isolates their original billing/drain/recovery
// assertions; strict production authority tests use the default nil dependency.
type historicalMerchantAuthorityComponentFixture struct{}

func (historicalMerchantAuthorityComponentFixture) Qualify(context.Context, productionWorkspace, productionManifest, bool, bool) error {
	return nil
}
func (historicalMerchantAuthorityComponentFixture) Ensure(context.Context, productionWorkspace, productionManifest) error {
	return nil
}
func (historicalMerchantAuthorityComponentFixture) Request(context.Context, productionWorkspace, productionManifest, bool) error {
	return nil
}
