package deploycli

import "context"

// This private dependency boundary lets transaction-component tests exercise
// their pre-existing drain/billing/recovery behavior with a separate explicit
// authority fixture. Production constructors leave it nil and always execute
// the strict artifact, environment, live-session and ACTIVE-owner checks.
// No CLI argument, environment variable, plan field or serialized state can
// select a replacement authority.
type productionMerchantStoreAuthority interface {
	Qualify(context.Context, productionWorkspace, productionManifest, bool, bool) error
	Ensure(context.Context, productionWorkspace, productionManifest) error
	Request(context.Context, productionWorkspace, productionManifest, bool) error
}
