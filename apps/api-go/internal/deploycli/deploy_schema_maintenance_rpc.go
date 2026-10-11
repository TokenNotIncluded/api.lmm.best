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
		return 15 * time.Minute, nil
	case "close", "claim", "capture-recovery", "capture-ready", "capture-startable", "status", "capture-outcome", "capture-recovery-profile", "install-recovery-hook", "prepare-abort", "check-abort-prestate", "capture-abort-startable", "capture-abort-ready", "capture-abort-binding", "install-abort-binding":
		// One full capsule qualification checks both signed providers, each with
		// a two-minute signature, 35-second status and five-minute verifier bound.
		return 20 * time.Minute, nil
	case "capture-backup-source":
		return 5 * time.Minute, nil
	case "backup-export":
		return 15 * time.Minute, nil
	case "release", "create-abort-binding":
		return 45 * time.Minute, nil // Two ordered full origin qualifications.
	case "finish", "finish-abort":
		return 65 * time.Minute, nil // Both origins, then the exact local generation.
	case "start-recovery", "start-abort-old":
		return 65 * time.Minute, nil // Both origins, native startup, local ready proof.
	case "reopen", "reopen-abort":
		return 110 * time.Minute, nil // Both origins before and after, plus local qualification.
	case "stop":
		return 3 * time.Minute, nil
	case "capture-closed", "capture-stopped", "check-schema-owner":
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
