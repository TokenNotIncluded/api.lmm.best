//go:build linux

package appcli

import (
	"math"

	"golang.org/x/sys/unix"
)

func merchantStoreMonotonicUS() (uint64, error) {
	var stamp unix.Timespec
	if unix.ClockGettime(unix.CLOCK_MONOTONIC, &stamp) != nil || stamp.Sec < 0 || stamp.Nsec < 0 || stamp.Nsec >= 1e9 || uint64(stamp.Sec) > (math.MaxUint64-uint64(stamp.Nsec)/1000)/1000000 {
		return 0, errMerchantStartupBudget
	}
	return uint64(stamp.Sec)*1000000 + uint64(stamp.Nsec)/1000, nil
}
