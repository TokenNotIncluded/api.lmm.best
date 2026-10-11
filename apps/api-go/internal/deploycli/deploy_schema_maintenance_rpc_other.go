//go:build !linux

package deploycli

import (
	"context"
	"errors"
)

func serveSchemaMaintenanceRPC(context.Context, string, schemaMaintenanceRPCBinding, schemaMaintenanceRPCHandler) error {
	return errors.New("schema maintenance controller RPC requires Linux peer and process identity")
}

func callSchemaMaintenanceRPC(context.Context, string, schemaMaintenanceRPCBinding, string) (schemaMaintenanceRPCReply, error) {
	return schemaMaintenanceRPCReply{}, errors.New("schema maintenance controller RPC requires Linux peer and process identity")
}

func schemaMaintenanceRPCSelfBinding(string, string, string) (schemaMaintenanceRPCBinding, error) {
	return schemaMaintenanceRPCBinding{}, errors.New("schema maintenance controller RPC requires Linux peer and process identity")
}
