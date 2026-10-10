package common

import "context"

type imageDeliveryHooksKey struct{}

// ImageDeliveryHooks is installed by a trusted internal caller, never decoded
// from an image request. Retain delivery data before normal model billing, and
// publish its completion only after that billing succeeds.
type ImageDeliveryHooks struct {
	BeforeBilling func() error
	AfterBilling  func(error) error
}

func WithImageDeliveryHooks(ctx context.Context, hooks ImageDeliveryHooks) context.Context {
	return context.WithValue(ctx, imageDeliveryHooksKey{}, hooks)
}

func ImageDeliveryHooksFromContext(ctx context.Context) ImageDeliveryHooks {
	hooks, _ := ctx.Value(imageDeliveryHooksKey{}).(ImageDeliveryHooks)
	return hooks
}
