package deploycli

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
)

// Only these fixed diagnostics may cross the backup RPC/SSH boundary. Never
// retain or unwrap a cause: it can contain a DSN, subprocess stderr or row data.
var schemaMaintenanceBackupStages = map[string]bool{
	"UNCLASSIFIED": true, "CONTROLLER_EXPORT_TRANSPORT": true, "AUTHORITY": true, "RUNNER_LEASE_PLAN": true,
	"STOPPED_ORIGINS": true, "STOP_BINDINGS": true, "STATE_DIRECTORY": true,
	"RECIPIENT": true, "CIPHER_RESERVE": true, "CIPHER_RESERVE_SYNC": true,
	"SOURCE_BEFORE": true, "OLD_CAPSULE": true, "CAPSULE_ENVIRONMENT": true,
	"ENVIRONMENT_ENTRY": true, "ENVIRONMENT_DUPLICATE": true, "DATABASE_COMMAND": true,
	"DUMP_ENVIRONMENT": true, "STREAM_AUTHORITY": true, "STREAM_ENVIRONMENT": true,
	"STREAM_PG_DUMP_EXECUTABLE": true, "STREAM_AGE_EXECUTABLE": true,
	"PIPE_DUMP_STDOUT": true, "PIPE_AGE_STDIN": true, "PIPE_AGE_START": true,
	"PIPE_DUMP_START": true, "PIPE_WAIT": true, "PIPE_TIMEOUT": true,
	"PIPE_CANCELED": true, "PIPE_ARCHIVE_HEADER": true, "STREAM_EXECUTE": true,
	"CIPHER_SYNC_CLOSE": true, "ARCHIVE_DECLARATION": true, "CIPHER_VERIFY": true,
	"SOURCE_AFTER": true, "SOURCE_COMPARE": true, "STOPPED_ORIGINS_AFTER": true,
	"STOPPED_ORIGINS_COMPARE": true, "HOST_AFTER": true, "PLAN_AFTER": true,
	"SET_VALIDATE": true, "SET_CHRONOLOGY": true, "SET_SERIALIZE": true,
	"SET_SIZE": true, "SET_WRITE": true,
}

type schemaMaintenanceBackupFailure struct {
	stage                    string
	dump, age, copy, context string
}

func schemaMaintenanceBackupClass(value string) string {
	switch value {
	case "ok", "failed", "signal", "timeout", "canceled":
		return value
	case "", "not_run":
		return "not_run"
	}
	if strings.HasPrefix(value, "exit_") {
		n, err := strconv.Atoi(strings.TrimPrefix(value, "exit_"))
		if err == nil && n >= 0 && n <= 255 && value == "exit_"+strconv.Itoa(n) {
			return value
		}
	}
	return "failed"
}

func (e *schemaMaintenanceBackupFailure) Error() string {
	stage := e.stage
	if !schemaMaintenanceBackupStages[stage] {
		stage = "UNCLASSIFIED"
	}
	return "schema backup failed stage=" + stage + " dump=" + schemaMaintenanceBackupClass(e.dump) +
		" age=" + schemaMaintenanceBackupClass(e.age) + " copy=" + schemaMaintenanceBackupClass(e.copy) +
		" context=" + schemaMaintenanceBackupClass(e.context) + " reconcile_without_retry"
}

func schemaMaintenanceBackupFail(stage string) error {
	return &schemaMaintenanceBackupFailure{stage: stage}
}

func schemaMaintenanceBackupObserved(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ProcessState != nil {
		if !exit.ProcessState.Exited() {
			return "signal"
		}
		return schemaMaintenanceBackupClass("exit_" + strconv.Itoa(exit.ExitCode()))
	}
	return "failed"
}

func schemaMaintenanceBackupStreamFailure(stage string, dump, age, copy, ctx error) error {
	// These are observations, not a claim about causality: an age failure can
	// cause the existing cleanup path to kill dump and close the copy pipes.
	startStage := stage
	if errors.Is(ctx, context.DeadlineExceeded) {
		stage = "PIPE_TIMEOUT"
	} else if errors.Is(ctx, context.Canceled) {
		stage = "PIPE_CANCELED"
	}
	result := &schemaMaintenanceBackupFailure{stage, schemaMaintenanceBackupObserved(dump), schemaMaintenanceBackupObserved(age), schemaMaintenanceBackupObserved(copy), schemaMaintenanceBackupObserved(ctx)}
	if startStage == "PIPE_AGE_START" {
		result.dump, result.copy = "not_run", "not_run"
	} else if startStage == "PIPE_DUMP_START" {
		result.copy = "not_run"
	}
	return result
}

func schemaMaintenanceBackupSafeFailure(err error, fallback string) error {
	if safe, ok := err.(*schemaMaintenanceBackupFailure); ok && safe != nil {
		// Rebuild even this private type using only the same finite vocabulary.
		return schemaMaintenanceBackupParseFailure(safe.Error(), fallback)
	}
	return schemaMaintenanceBackupFail(fallback)
}

func schemaMaintenanceBackupParseFailure(raw, fallback string) error {
	words := strings.Fields(raw)
	if len(words) == 9 && strings.Join(words[:3], " ") == "schema backup failed" && words[8] == "reconcile_without_retry" {
		keys := []string{"stage=", "dump=", "age=", "copy=", "context="}
		values := make([]string, len(keys))
		for i, key := range keys {
			if !strings.HasPrefix(words[i+3], key) {
				return schemaMaintenanceBackupFail(fallback)
			}
			values[i] = strings.TrimPrefix(words[i+3], key)
		}
		safe := &schemaMaintenanceBackupFailure{values[0], values[1], values[2], values[3], values[4]}
		if schemaMaintenanceBackupStages[safe.stage] && raw == safe.Error() {
			return safe
		}
	}
	return schemaMaintenanceBackupFail(fallback)
}

func schemaMaintenanceBackupTransportFailure(err error) error {
	// The runner returns no stdout for a failed SSH command. Accept only this
	// exact RPC CLI shell and rebuild the fixed code, never forward stderr or
	// mistake an SSH exit/signal for the pg_dump or age process result.
	const prefix = "command ssh failed: exit status 1: schema-maintenance rpc: schema maintenance RPC backup-export failed without retry: "
	if err != nil && strings.HasPrefix(err.Error(), prefix) {
		return schemaMaintenanceBackupParseFailure(strings.TrimPrefix(err.Error(), prefix), "CONTROLLER_EXPORT_TRANSPORT")
	}
	return schemaMaintenanceBackupFail("CONTROLLER_EXPORT_TRANSPORT")
}
