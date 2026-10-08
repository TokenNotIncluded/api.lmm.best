package service

import (
	"context"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
)

// RunCommerceImportMaintenance erases expired integration secrets in bounded
// batches even when the administrator has disabled new connections. Existing
// stock, orders, mappings and financial records are outside its model scope.
func RunCommerceImportMaintenance(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if model.CommerceImportSupported() {
			if err := model.ClearExpiredCommerceImportSecrets(200); err != nil {
				common.SysError("commerce import secret cleanup could not complete")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
