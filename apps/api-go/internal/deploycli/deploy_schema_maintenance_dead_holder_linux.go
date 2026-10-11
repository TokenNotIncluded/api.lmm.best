//go:build linux

package deploycli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func schemaMaintenanceDeadHolderProcessGone(generation schemaMaintenanceRPCBinding, unit map[string]string) error {
	if err := schemaMaintenanceDeadHolderUnitGone(generation, unit); err != nil {
		return err
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", generation.DaemonPID))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("original schema daemon process state is inaccessible")
	}
	end := bytes.LastIndexByte(raw, ')')
	if end < 0 {
		return errors.New("original schema daemon process state is malformed")
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) < 20 {
		return errors.New("original schema daemon process fields are incomplete")
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || start == 0 {
		return errors.New("original schema daemon start identity is unknown")
	}
	if start != generation.DaemonStartTicks {
		return errors.New("original schema daemon PID was reused; independent process proof is required")
	} // Never signal a reused PID.
	if fields[0] == "Z" || fields[0] == "X" || fields[0] == "x" {
		return nil
	}
	return errors.New("original schema daemon generation is still alive")
}
