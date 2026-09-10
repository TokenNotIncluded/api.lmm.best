package appcli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const publicServiceStatusFilename = "service-status.json"

type publicServiceStatus struct {
	State               string     `json:"state"`
	Service             string     `json:"service"`
	Message             string     `json:"message,omitempty"`
	DeploymentID        string     `json:"deployment_id"`
	UpdatedAt           time.Time  `json:"updated_at"`
	EstimatedRecoveryAt *time.Time `json:"estimated_recovery_at,omitempty"`
}

func serviceStateForPhase(phase string) string {
	switch phase {
	case "MUTATION_PENDING":
		return "maintenance"
	case "MIGRATING":
		return "migration"
	case "DEPLOYING", "DEPLOYING_GO", "DEPLOYING_WEB":
		return "deploying"
	case "OBSERVING", "AWAITING_CONFIRMATION", "CONFIRMING":
		return "checking"
	case "ROLLBACK_REQUIRED", "ROLLING_BACK":
		return "recovering"
	case "CONFIRMED", "ROLLED_BACK", "FAILED_PREARM":
		return "ready"
	default:
		return ""
	}
}

func publicServiceText(value string, maximum int) bool {
	return strings.TrimSpace(value) != "" && len([]rune(value)) <= maximum && !strings.ContainsFunc(value, unicode.IsControl)
}

func writePublicServiceStatus(root string, status publicServiceStatus) error {
	switch status.State {
	case "maintenance", "migration", "deploying", "recovering", "checking", "ready":
	default:
		return errors.New("invalid public service state")
	}
	if !productionIDPattern.MatchString(status.DeploymentID) || !publicServiceText(status.Service, 120) ||
		(status.Message != "" && !publicServiceText(status.Message, 512)) || status.UpdatedAt.IsZero() {
		return errors.New("invalid public service status")
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return errors.New("public status root must be absolute and canonical")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(root))
	if err != nil || parent != filepath.Dir(root) {
		return errors.New("public status parent is unsafe")
	}
	if err := ensureRealDirectory(root, 0o755); err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || canonical != root {
		return errors.New("public status root is not canonical")
	}
	target := filepath.Join(root, publicServiceStatusFilename)
	if info, err := os.Lstat(target); err == nil {
		uid, links, owned := deploymentFileOwnership(info)
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || !owned || uid != uint32(os.Geteuid()) || links != 1 {
			return errors.New("public status target is unsafe")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		return err
	}
	return writeAtomicRegularFile(target, append(encoded, '\n'), 0o644)
}

func (runtime *productionRuntime) publishServicePhase(deploymentID, phase string) error {
	state := serviceStateForPhase(phase)
	if state == "" || runtime.paths.FrontendRoot == "" {
		return nil
	}
	status := publicServiceStatus{State: state, Service: "LMM Best", DeploymentID: deploymentID, UpdatedAt: runtime.now().UTC()}
	path := filepath.Join(runtime.paths.FrontendRoot, publicServiceStatusFilename)
	if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o022 == 0 && info.Size() <= 8192 {
		uid, links, owned := deploymentFileOwnership(info)
		if !owned || uid != uint32(os.Geteuid()) || links != 1 {
			return errors.New("public status ownership is unsafe")
		}
		if data, err := os.ReadFile(path); err == nil {
			var previous publicServiceStatus
			if json.Unmarshal(data, &previous) == nil && previous.DeploymentID == deploymentID {
				if publicServiceText(previous.Service, 120) {
					status.Service = previous.Service
				}
				status.EstimatedRecoveryAt = previous.EstimatedRecoveryAt
				if previous.State == state && publicServiceText(previous.Message, 512) {
					status.Message = previous.Message
				}
			}
		}
	}
	return writePublicServiceStatus(runtime.paths.FrontendRoot, status)
}

func runProductionMaintenance(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("production maintenance", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var status publicServiceStatus
	var expected, confirm string
	flags.StringVar(&status.DeploymentID, "deployment-id", "", "public deployment identifier")
	flags.StringVar(&status.State, "state", "maintenance", "maintenance, migration, deploying, recovering, checking, or ready")
	flags.StringVar(&status.Service, "service", "LMM Best", "public affected service or model name")
	flags.StringVar(&status.Message, "message", "", "public explanation; never include credentials or internal errors")
	flags.StringVar(&expected, "expected-recovery-at", "", "optional operator estimate in RFC3339; omitted means unknown")
	flags.StringVar(&confirm, "confirm", "", "must equal api.lmm.best")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		return ExitOK
	} else if err != nil || flags.NArg() != 0 {
		return ExitUsage
	}
	if confirm != "api.lmm.best" {
		_, _ = fmt.Fprintln(stderr, "--confirm must equal api.lmm.best")
		return ExitUsage
	}
	runtime := defaultProductionRuntime()
	if err := runtime.assertProductionMutation(); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return ExitError
	}
	status.UpdatedAt = runtime.now().UTC()
	if expected != "" {
		parsed, err := time.Parse(time.RFC3339, expected)
		if err != nil || !parsed.After(status.UpdatedAt) {
			_, _ = fmt.Fprintln(stderr, "expected recovery must be a future RFC3339 timestamp")
			return ExitUsage
		}
		status.EstimatedRecoveryAt = &parsed
	}
	if err := writePublicServiceStatus(runtime.paths.FrontendRoot, status); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return ExitError
	}
	_, _ = fmt.Fprintln(stdout, "public_service_status=updated")
	return ExitOK
}
