//go:build !linux

package appcli

import "errors"

func verifyMerchantStoreSocketDirectory(_ string, _ uint32, _ bool) error {
	return errors.New("merchant holder Unix sockets require Linux directory verification")
}
