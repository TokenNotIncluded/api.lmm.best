package appcli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type productionDispatchEvidence struct {
	Format          int    `json:"format"`
	DeploymentID    string `json:"deployment_id"`
	Unit            string `json:"unit"`
	UnitLoadState   string `json:"unit_load_state"`
	UnitPresent     bool   `json:"unit_present"`
	ManifestPresent bool   `json:"manifest_present"`
	StatusPresent   bool   `json:"status_present"`
}

func runProductionDispatchEvidence(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("deploy production dispatch-evidence", flag.ContinueOnError)
	flags.SetOutput(stderr)
	workspacePath := flags.String("workspace", "", "canonical target workspace")
	unit := flags.String("unit", "", "exact activation unit")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitUsage
	}
	if flags.NArg() != 0 || *workspacePath == "" || *unit == "" {
		_, _ = fmt.Fprintln(stderr, "--workspace and --unit are required")
		return ExitUsage
	}
	runtime := defaultProductionRuntime()
	evidence, err := runtime.productionDispatchEvidence(context.Background(), *workspacePath, *unit)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production dispatch-evidence: %v\n", ProgramName, err)
		return ExitError
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production dispatch-evidence: encode result: %v\n", ProgramName, err)
		return ExitError
	}
	_, _ = stdout.Write(append(encoded, '\n'))
	return ExitOK
}

func (runtime *productionRuntime) productionDispatchEvidence(ctx context.Context, workspacePath, unit string) (productionDispatchEvidence, error) {
	workspace, err := runtime.openWorkspace(workspacePath)
	if err != nil {
		return productionDispatchEvidence{}, err
	}
	expectedUnit := productionActivationUnit(workspace.id)
	if unit != expectedUnit {
		return productionDispatchEvidence{}, errors.New("activation unit does not match the workspace deployment")
	}
	manifestPresent, err := dispatchEvidenceFilePresent(workspace.manifestPath)
	if err != nil {
		return productionDispatchEvidence{}, fmt.Errorf("inspect activation manifest: %w", err)
	}
	statusPresent, err := dispatchEvidenceFilePresent(workspace.statusPath)
	if err != nil {
		return productionDispatchEvidence{}, fmt.Errorf("inspect activation status: %w", err)
	}
	output, err := runtime.runner.Run(ctx, productionCommand{
		Name:    commandSystemctl,
		Args:    []string{"show", "--property=LoadState", "--value", "--", unit},
		Timeout: 30 * time.Second,
		Env:     append(os.Environ(), "LC_ALL=C"),
	})
	if err != nil {
		return productionDispatchEvidence{}, fmt.Errorf("inspect activation unit: %w", err)
	}
	loadState := strings.TrimSpace(string(output))
	if loadState == "" || strings.ContainsAny(loadState, "\r\n\t ") {
		return productionDispatchEvidence{}, errors.New("activation unit returned an invalid load state")
	}
	return productionDispatchEvidence{
		Format:          1,
		DeploymentID:    workspace.id,
		Unit:            unit,
		UnitLoadState:   loadState,
		UnitPresent:     loadState != "not-found",
		ManifestPresent: manifestPresent,
		StatusPresent:   statusPresent,
	}, nil
}

func dispatchEvidenceFilePresent(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("evidence path is not a regular file")
	}
	return true, nil
}

func productionActivationUnit(deploymentID string) string {
	return "lmm-api-deploy-" + deploymentID + ".service"
}
