//go:build !linux

package appcli

func merchantStoreMonotonicUS() (uint64, error) {
	return 0, errMerchantStartupBudget
}
