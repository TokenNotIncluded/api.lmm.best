//go:build !linux

package deploycli

import "errors"

func (runtime *productionRuntime) writeSchemaMaintenanceGuardianAdopted(*productionSchemaMaintenanceHost, schemaMaintenanceRPCBinding) error {
	return errors.New("schema guardian original adoption requires Linux SCM_RIGHTS and native process identities")
}

func (runtime *productionRuntime) checkSchemaMaintenanceGuardianReadopted(*productionSchemaMaintenanceHost, schemaMaintenanceRPCBinding) error {
	return errors.New("schema guardian recovery comparison requires Linux SCM_RIGHTS and native process identities")
}
