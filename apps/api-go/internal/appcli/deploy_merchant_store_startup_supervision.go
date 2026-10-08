package appcli

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// This is a total application startup budget, leaving cleanup room inside the
// existing 300-second caller. systemd may rearm its timer at command boundaries;
// we deliberately never renew the budget when another startup phase begins.
const merchantStoreStartupLimit = 240 * time.Second

type merchantStoreStartupBudget struct {
	startedUS  uint64
	deadlineUS uint64
	controlPID int
	holder     *merchantStoreCreatedStartupHolder
	handoff    *bool
}

type merchantStoreCreatedStartupHolder struct {
	pid        int
	invocation string
}

type merchantStoreStartupBudgetKey struct{}

var (
	errMerchantStartupDeadline = errors.New("per-start supervision: startup deadline expired")
	errMerchantStartupCanceled = errors.New("per-start supervision: startup canceled")
	errMerchantStartupTarget   = errors.New("per-start supervision: original pre-start service generation was lost")
	errMerchantStartupBudget   = errors.New("per-start supervision: finite loaded startup budget is unavailable")
)

type merchantStorePortableProofError struct{ stage string }

func (err merchantStorePortableProofError) Error() string {
	return "portable holder proof failed at " + err.stage
}

// systemctl formats USec durations with systemd's timespan suffixes, rather
// than printing the D-Bus uint64, including fractional seconds/milliseconds.
func merchantStoreSystemdTimespanUS(value string) (uint64, error) {
	units := []struct {
		suffix string
		us     uint64
	}{{"month", 2629800_000000}, {"min", 60_000000}, {"ms", 1000}, {"us", 1}, {"y", 31557600_000000}, {"w", 604800_000000}, {"d", 86400_000000}, {"h", 3600_000000}, {"s", 1000000}}
	var total uint64
	fields := strings.Fields(value)
	if len(fields) == 0 || len(fields) > len(units) {
		return 0, errMerchantStartupBudget
	}
	for _, field := range fields {
		matched := false
		for _, unit := range units {
			if !strings.HasSuffix(field, unit.suffix) {
				continue
			}
			number := strings.TrimSuffix(field, unit.suffix)
			whole, fraction, decimal := strings.Cut(number, ".")
			if whole == "" || strings.Trim(whole, "0123456789") != "" || decimal && (fraction == "" || len(fraction) > 6 || strings.Trim(fraction, "0123456789") != "") {
				return 0, errMerchantStartupBudget
			}
			n, err := strconv.ParseUint(whole, 10, 64)
			if err != nil || n > (math.MaxUint64-total)/unit.us {
				return 0, errMerchantStartupBudget
			}
			total += n * unit.us
			if decimal {
				divisor := uint64(1)
				for range fraction {
					divisor *= 10
				}
				if unit.us%divisor != 0 {
					return 0, errMerchantStartupBudget
				}
				part, _ := strconv.ParseUint(fraction, 10, 64)
				extra := part * (unit.us / divisor)
				if total > math.MaxUint64-extra {
					return 0, errMerchantStartupBudget
				}
				total += extra
			}
			matched = true
			break
		}
		if !matched {
			return 0, errMerchantStartupBudget
		}
	}
	if total == 0 || total == math.MaxUint64 {
		return 0, errMerchantStartupBudget
	}
	return total, nil
}

func (runtime *productionRuntime) merchantStoreStartupState(ctx context.Context, service string) (map[string]string, error) {
	keys := []string{"InvocationID", "ActiveState", "SubState", "MainPID", "ControlPID", "TimeoutStartUSec", "InactiveExitTimestampMonotonic"}
	output, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"show", service, "--all", "--property=" + strings.Join(keys, ",")}, Timeout: 15 * time.Second, OutputLimit: 4096})
	if err != nil {
		return nil, errMerchantStartupTarget
	}
	state := make(map[string]string, len(keys))
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, errMerchantStartupBudget
		}
		if _, exists := state[key]; exists {
			return nil, errMerchantStartupBudget
		}
		state[key] = value
	}
	if len(state) != len(keys) {
		return nil, errMerchantStartupBudget
	}
	for _, key := range keys {
		if _, ok := state[key]; !ok {
			return nil, errMerchantStartupBudget
		}
	}
	return state, nil
}

func merchantStoreStartupStateDeadline(state map[string]string) (uint64, uint64, error) {
	start, err := strconv.ParseUint(state["InactiveExitTimestampMonotonic"], 10, 64)
	timeout, timeoutErr := merchantStoreSystemdTimespanUS(state["TimeoutStartUSec"])
	if err != nil || start == 0 || strings.Trim(state["InactiveExitTimestampMonotonic"], "0123456789") != "" || timeoutErr != nil {
		return 0, 0, errMerchantStartupBudget
	}
	limit := uint64(merchantStoreStartupLimit / time.Microsecond)
	if timeout > limit {
		timeout = limit
	}
	if start > math.MaxUint64-timeout {
		return 0, 0, errMerchantStartupBudget
	}
	return start, start + timeout, nil
}

func (runtime *productionRuntime) merchantStoreMonotonicUS() (uint64, error) {
	if runtime.startupMonotonicUS != nil {
		return runtime.startupMonotonicUS()
	}
	return merchantStoreMonotonicUS()
}

func merchantStoreControlAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	body, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	end := strings.LastIndexByte(string(body), ')')
	if err != nil || end < 0 {
		return false
	}
	fields := strings.Fields(string(body[end+1:]))
	return len(fields) > 0 && fields[0] != "Z" && fields[0] != "X"
}

func merchantStoreStartupActivating(state map[string]string, invocation string, pid int) bool {
	return state["InvocationID"] == invocation && state["ActiveState"] == "activating" && state["SubState"] == "start-pre" && state["MainPID"] == "0" && state["ControlPID"] == strconv.Itoa(pid) && merchantStoreControlAlive(pid)
}

func (runtime *productionRuntime) bindMerchantStoreStartupBudget(ctx context.Context, c productionMerchantStoreCapsule, invocation string, received uint64, controlPID int) (context.Context, context.CancelFunc, error) {
	state, err := runtime.merchantStoreStartupState(ctx, c.Service)
	if err != nil || !existingSchemaInvocationPattern.MatchString(invocation) || !merchantStoreStartupActivating(state, invocation, controlPID) {
		return nil, nil, errMerchantStartupTarget
	}
	start, deadline, err := merchantStoreStartupStateDeadline(state)
	if err != nil {
		return nil, nil, err
	}
	if received != 0 && received < deadline {
		deadline = received
	}
	// Take the Go timer anchor before sampling the host's monotonic clock.
	// Conversion overhead can shorten the context, never extend the deadline.
	anchor := time.Now()
	now, err := runtime.merchantStoreMonotonicUS()
	if err != nil || now < start {
		return nil, nil, errMerchantStartupBudget
	}
	if now >= deadline {
		return nil, nil, errMerchantStartupDeadline
	}
	remaining := time.Duration(deadline-now) * time.Microsecond
	if callerDeadline, ok := ctx.Deadline(); ok {
		callerRemaining := time.Until(callerDeadline)
		if callerRemaining <= 0 {
			return nil, nil, errMerchantStartupDeadline
		}
		if callerRemaining < remaining {
			remaining = callerRemaining
			deadline = now + uint64(remaining/time.Microsecond)
		}
	}
	bounded, cancel := context.WithDeadline(ctx, anchor.Add(remaining))
	handoff := false
	budget := merchantStoreStartupBudget{startedUS: start, deadlineUS: deadline, controlPID: controlPID, holder: &merchantStoreCreatedStartupHolder{}, handoff: &handoff}
	return context.WithValue(bounded, merchantStoreStartupBudgetKey{}, budget), cancel, nil
}

func (runtime *productionRuntime) merchantStoreStartupGuard(ctx context.Context, c productionMerchantStoreCapsule, invocation string, claiming bool) error {
	budget, ok := ctx.Value(merchantStoreStartupBudgetKey{}).(merchantStoreStartupBudget)
	if !ok {
		return errMerchantStartupBudget
	}
	checkTime := func(deadline uint64) error {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return errMerchantStartupDeadline
		}
		if ctx.Err() != nil {
			return errMerchantStartupCanceled
		}
		now, err := runtime.merchantStoreMonotonicUS()
		if err != nil || now < budget.startedUS {
			return errMerchantStartupBudget
		}
		if now >= deadline {
			return errMerchantStartupDeadline
		}
		return nil
	}
	if err := checkTime(budget.deadlineUS); err != nil {
		return err
	}
	state, err := runtime.merchantStoreStartupState(ctx, c.Service)
	if err != nil || state["InvocationID"] != invocation {
		return errMerchantStartupTarget
	}
	if claiming {
		if !merchantStoreStartupActivating(state, invocation, budget.controlPID) {
			return errMerchantStartupTarget
		}
	} else if !merchantStoreStartupLifecycle(state, invocation, budget) {
		return errMerchantStartupTarget
	}
	start, deadline, err := merchantStoreStartupStateDeadline(state)
	if err != nil || start != budget.startedUS {
		return errMerchantStartupBudget
	}
	if deadline > budget.deadlineUS {
		deadline = budget.deadlineUS
	}
	return checkTime(deadline)
}

func merchantStoreStartupLifecycle(state map[string]string, invocation string, budget merchantStoreStartupBudget) bool {
	if merchantStoreStartupActivating(state, invocation, budget.controlPID) {
		return true
	}
	// Only a successfully written Held reply begins the normal handoff. Checks
	// which grant Held still require the original live ExecStartPre process.
	if budget.handoff == nil || !*budget.handoff || state["InvocationID"] != invocation {
		return false
	}
	if state["ActiveState"] == "active" && state["SubState"] == "running" {
		pid, err := strconv.Atoi(state["MainPID"])
		return err == nil && pid > 1
	}
	if state["ActiveState"] != "activating" {
		return false
	}
	switch state["SubState"] {
	case "start-pre":
		// The manager may not yet have reaped the just-completed control PID.
		return state["MainPID"] == "0" && (state["ControlPID"] == "0" || state["ControlPID"] == strconv.Itoa(budget.controlPID))
	case "start", "start-post":
		return true
	}
	return false
}

// The check after claim cannot make systemd and PostgreSQL atomic. If the
// target changes during COMMIT, retain ACTIVE and refuse to publish authority.
func merchantStoreGuardedStartupClaim(ctx context.Context, guard func(context.Context) error, claim func(context.Context, func(context.Context) error) error) error {
	if err := guard(ctx); err != nil {
		return err
	}
	if err := claim(ctx, guard); err != nil {
		return err
	}
	if err := guard(ctx); err != nil {
		return fmt.Errorf("%w; post-claim target invalid; ACTIVE owner remains permanently fenced", err)
	}
	return nil
}

func merchantStoreBoundConnection(ctx context.Context, conn net.Conn) func() {
	deadline := time.Now().Add(90 * time.Second)
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	return func() { stop() }
}

func (runtime *productionRuntime) waitMerchantStoreStartupHolder(ctx context.Context, c productionMerchantStoreCapsule, invocation string, probe func(context.Context) error) error {
	last := "receipt-pending"
	for {
		if err := runtime.merchantStoreStartupGuard(ctx, c, invocation, true); err != nil {
			return fmt.Errorf("%w; holder handshake stage=%s", err, last)
		}
		if err := probe(ctx); err == nil {
			return runtime.merchantStoreStartupGuard(ctx, c, invocation, true)
		} else {
			var proof merchantStorePortableProofError
			if errors.As(err, &proof) {
				last = proof.stage
			} else {
				last = "ownership-proof-pending"
			}
		}
		// No SQL, nonce, environment or arbitrary child error is printed.
		if runtime.startupWait != nil {
			if err := runtime.startupWait(ctx, 250*time.Millisecond); err != nil {
				return errMerchantStartupCanceled
			}
		} else {
			timer := time.NewTimer(250 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
	}
}

func (runtime *productionRuntime) captureMerchantStoreStartupHolder(ctx context.Context, c productionMerchantStoreCapsule, unit, provider string, args []string, holder *merchantStoreCreatedStartupHolder) error {
	state, err := runtime.billingUnitState(ctx, unit)
	pid, pidErr := strconv.Atoi(state["MainPID"])
	if err != nil || pidErr != nil || pid <= 1 || state["ExecMainPID"] != state["MainPID"] || state["Restart"] != "no" || state["ActiveState"] != "active" || state["SubState"] != "running" || !existingSchemaInvocationPattern.MatchString(state["InvocationID"]) || sha256MustEqual(filepath.Join("/proc", strconv.Itoa(pid), "exe"), c.Writer.Candidate.PayloadSHA256) != nil {
		return errors.New("per-start supervision: newly created holder generation could not be proved")
	}
	command, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	expected := strings.Join(append([]string{provider}, args...), "\x00") + "\x00"
	if err != nil || string(command) != expected {
		return errors.New("per-start supervision: holder command does not belong to this pre-start process")
	}
	holder.pid, holder.invocation = pid, state["InvocationID"]
	return nil
}

// Never clear the durable owner. A failed hook stops only the exact child it
// created, using a separate bounded cleanup context after startup cancellation.
func (runtime *productionRuntime) cancelMerchantStoreStartupHolder(c productionMerchantStoreCapsule, invocation string, holder *merchantStoreCreatedStartupHolder) error {
	if holder == nil || holder.pid == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	unit := merchantStoreStartUnit(c.DeploymentID, invocation)
	state, err := runtime.billingUnitState(ctx, unit)
	if errors.Is(merchantStoreStartupProcessGone(holder.pid), os.ErrNotExist) {
		return nil
	}
	if err != nil || state["MainPID"] != strconv.Itoa(holder.pid) || state["ExecMainPID"] != strconv.Itoa(holder.pid) || state["InvocationID"] != holder.invocation || state["Restart"] != "no" || sha256MustEqual(filepath.Join("/proc", strconv.Itoa(holder.pid), "exe"), c.Writer.Candidate.PayloadSHA256) != nil {
		return errors.New("per-start supervision: exact child cancellation could not be proved; retained evidence requires review")
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"stop", unit}, Timeout: 10 * time.Second}); err != nil || !errors.Is(merchantStoreStartupProcessGone(holder.pid), os.ErrNotExist) {
		return errors.New("per-start supervision: exact child termination was not confirmed; retained evidence requires review")
	}
	return nil
}

func merchantStoreStartupProcessGone(pid int) error {
	_, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
	return err
}
