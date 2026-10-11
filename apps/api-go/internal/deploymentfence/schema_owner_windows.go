//go:build windows

package deploymentfence

import "errors"

func readSchemaMaintenanceOwnerFile(string) ([]byte, error) {
	return nil, errors.New("native schema maintenance owner requires Unix file ownership")
}
