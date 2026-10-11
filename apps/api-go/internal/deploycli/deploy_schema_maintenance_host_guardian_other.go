//go:build !linux

package deploycli

import (
	"context"
	"errors"
)

func receiveSchemaMaintenanceHostLocks(context.Context, productionSchemaMaintenanceHostBinding, uint32) (productionSchemaMaintenanceHostLease, error) {
	return nil, errors.New("schema maintenance lock adoption requires Linux SCM_RIGHTS")
}
