package deploycli

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const schemaMaintenanceRPCProtocol = "lmm-schema-maintenance-controller-rpc-v1"

// This identity must come from the immutable plan and the actual daemon
// generation. Transport identity does not authorize a schema operation.
type schemaMaintenanceRPCBinding struct {
	PlanSHA256         string `json:"plan_sha256"`
	TransitionID       string `json:"transition_id"`
	DaemonPID          int    `json:"daemon_pid"`
	DaemonInvocationID string `json:"daemon_invocation_id"`
	DaemonExeSHA256    string `json:"daemon_exe_sha256"`
	DaemonStartTicks   uint64 `json:"daemon_start_ticks"`
}

type schemaMaintenanceRPCRequest struct {
	Protocol string                      `json:"protocol"`
	Binding  schemaMaintenanceRPCBinding `json:"binding"`
	Action   string                      `json:"action"`
	Nonce    string                      `json:"nonce"`
}

type schemaMaintenanceRPCReply struct {
	Protocol string                      `json:"protocol"`
	Binding  schemaMaintenanceRPCBinding `json:"binding"`
	Action   string                      `json:"action"`
	Nonce    string                      `json:"nonce"`
	OK       bool                        `json:"ok"`
	Result   json.RawMessage             `json:"result"`
	Error    string                      `json:"error"`
}

type schemaMaintenanceRPCHandler func(context.Context, schemaMaintenanceRPCRequest) (schemaMaintenanceRPCReply, error)

func schemaMaintenanceRPCActionTimeout(action string) (time.Duration, error) {
	switch action {
	case "worker":
		return 10 * time.Minute, nil
	case "close", "capture-closed", "stop", "capture-stopped", "claim", "status", "release", "reopen":
		return 30 * time.Second, nil
	default:
		return 0, errors.New("unknown schema maintenance RPC action")
	}
}

func validateSchemaMaintenanceRPCBinding(binding schemaMaintenanceRPCBinding) error {
	if !productionSHA256Pattern.MatchString(binding.PlanSHA256) ||
		!productionIDPattern.MatchString(binding.TransitionID) || binding.DaemonPID <= 1 ||
		!existingSchemaInvocationPattern.MatchString(binding.DaemonInvocationID) ||
		!productionSHA256Pattern.MatchString(binding.DaemonExeSHA256) || binding.DaemonStartTicks == 0 {
		return errors.New("schema maintenance RPC generation binding is incomplete")
	}
	return nil
}
