//go:build !linux

package deploycli

func merchantStoreMonotonicUS() (uint64, error) {
	return 0, errMerchantStartupBudget
}
