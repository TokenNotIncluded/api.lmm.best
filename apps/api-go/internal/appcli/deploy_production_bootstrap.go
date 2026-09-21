package appcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Bootstrap is the only operation that must run before the candidate operator
// has been staged. Select the installed, package-bound protocol through help;
// never retry a mutating command using a different spelling after an SSH error.
func (runtime *productionReleaseRuntime) bootstrapRemoteWorkspace(ctx context.Context, plan productionReleasePlan) (productionWorkspaceResult, error) {
	if !productionIDPattern.MatchString(plan.DeploymentID) {
		return productionWorkspaceResult{}, errors.New("invalid bootstrap deployment ID")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	provider := filepath.Join(filepath.Dir(productionOperatorBinary), backendGoName)
	if err := runtime.verifyRemoteProviderEntrypoint(ctx, plan.TargetAlias, productionOperatorBinary, provider, plan.GoRollback.PayloadSHA256); err != nil {
		return productionWorkspaceResult{}, fmt.Errorf("verify installed rollback provider before bootstrap: %w", err)
	}
	help, err := runtime.ssh(ctx, plan.TargetAlias, 30*time.Second, productionOperatorBinary, "help")
	if err != nil {
		return productionWorkspaceResult{}, fmt.Errorf("inspect installed deployment protocol: %w", err)
	}
	protocol, err := productionBootstrapProtocol(string(help))
	if err != nil {
		return productionWorkspaceResult{}, err
	}
	expected := filepath.Join(defaultProductionPaths().WorkRoot, plan.DeploymentID)
	exists, err := runtime.remoteDirectoryExists(ctx, plan.TargetAlias, expected)
	if err != nil {
		return productionWorkspaceResult{}, err
	}
	if exists {
		return runtime.inspectBootstrapWorkspace(ctx, plan)
	}
	output, createErr := runtime.ssh(ctx, plan.TargetAlias, 2*time.Minute,
		productionOperatorBinary, protocol, "production", "workspace", "create", "--deployment-id", plan.DeploymentID)
	if createErr != nil {
		// The server may have completed before SSH disconnected. Read the exact
		// native markers instead of dispatching create twice or inventing state.
		workspace, inspectErr := runtime.inspectBootstrapWorkspace(ctx, plan)
		if inspectErr != nil {
			return productionWorkspaceResult{}, fmt.Errorf("workspace creation needs reconciliation: create=%v inspect=%w", createErr, inspectErr)
		}
		return workspace, nil
	}
	var workspace productionWorkspaceResult
	if json.Unmarshal(output, &workspace) != nil || workspace.DeploymentID != plan.DeploymentID || !workspace.TransactionSet || workspace.Workspace != expected || workspace.Transaction != defaultProductionPaths().TransactionLock {
		return productionWorkspaceResult{}, errors.New("target workspace response is invalid")
	}
	return workspace, nil
}

func productionBootstrapProtocol(help string) (string, error) {
	if len(help) > 32<<10 {
		return "", errors.New("installed deployment help is oversized")
	}
	current, legacy := false, false
	for _, line := range strings.Split(help, "\n") {
		switch strings.TrimSpace(line) {
		case "/usr/bin/lmm-api-deploy build|frontend|production ...":
			current = true
		case "lmm-api deploy production plan [signed candidate and rollback inputs]":
			legacy = true
		}
	}
	if current == legacy {
		return "", errors.New("installed deployment protocol is unsupported or ambiguous")
	}
	if legacy {
		return "deploy", nil
	}
	return "operator", nil
}

// Inspect only an unactivated native workspace. A manifest or status belongs to
// a different recovery phase and must never be relabelled WORKSPACE_CREATED.
func (runtime *productionReleaseRuntime) inspectBootstrapWorkspace(ctx context.Context, plan productionReleasePlan) (productionWorkspaceResult, error) {
	paths := defaultProductionPaths()
	root := filepath.Join(paths.WorkRoot, plan.DeploymentID)
	for _, directory := range []string{filepath.Dir(paths.WorkRoot), paths.WorkRoot, root, filepath.Join(root, "staging"), filepath.Join(root, "state"), paths.TransactionLock} {
		if _, err := runtime.bootstrapRemoteEntry(ctx, plan.TargetAlias, directory, true); err != nil {
			return productionWorkspaceResult{}, err
		}
	}
	for _, name := range []string{productionManifestFilename, productionStatusFilename} {
		path := filepath.Join(root, "state", name)
		if _, err := runtime.ssh(ctx, plan.TargetAlias, 30*time.Second, "test", "!", "-e", path); err != nil {
			return productionWorkspaceResult{}, errors.New("bootstrap workspace already has activation evidence or cannot be inspected")
		}
		if _, err := runtime.ssh(ctx, plan.TargetAlias, 30*time.Second, "test", "!", "-L", path); err != nil {
			return productionWorkspaceResult{}, errors.New("bootstrap workspace activation evidence is unsafe")
		}
	}
	marker, err := runtime.bootstrapRemoteEntry(ctx, plan.TargetAlias, filepath.Join(root, productionWorkspaceMarker), false)
	if err != nil {
		return productionWorkspaceResult{}, err
	}
	prefix := "format=1\ndeployment_id=" + plan.DeploymentID + "\nrole=target\ncreated_at_utc="
	created, valid := strings.CutPrefix(marker, prefix)
	stamp, stampErr := time.Parse(time.RFC3339, strings.TrimSuffix(created, "\n"))
	if !valid || !strings.HasSuffix(created, "\n") || stampErr != nil || stamp.Location() != time.UTC {
		return productionWorkspaceResult{}, errors.New("bootstrap workspace native marker is invalid")
	}
	transaction, err := runtime.bootstrapRemoteEntry(ctx, plan.TargetAlias, filepath.Join(paths.TransactionLock, productionTransactionMarker), false)
	if err != nil || transaction != "format=1\ndeployment_id="+plan.DeploymentID+"\nstatus=ACTIVE\n" {
		return productionWorkspaceResult{}, errors.New("bootstrap workspace does not own the active native transaction")
	}
	return productionWorkspaceResult{DeploymentID: plan.DeploymentID, Workspace: root, Transaction: paths.TransactionLock, TransactionSet: true}, nil
}

func (runtime *productionReleaseRuntime) bootstrapRemoteEntry(ctx context.Context, alias, path string, directory bool) (string, error) {
	metadata, err := runtime.ssh(ctx, alias, 30*time.Second, "stat", "-c", "%u:%f:%h:%s", "--", path)
	parts := strings.Split(strings.TrimSpace(string(metadata)), ":")
	if err != nil || len(parts) != 4 || parts[0] != "0" {
		return "", errors.New("bootstrap entry must be root-owned and inspectable")
	}
	mode, modeErr := strconv.ParseUint(parts[1], 16, 32)
	if modeErr != nil || mode&0o7022 != 0 {
		return "", errors.New("bootstrap entry permissions are unsafe")
	}
	if directory {
		if mode&0o170000 != 0o040000 || mode&0o700 != 0o700 {
			return "", errors.New("bootstrap entry is not a private native directory")
		}
		return "", nil
	}
	size, sizeErr := strconv.ParseInt(parts[3], 10, 64)
	if mode&0o170000 != 0o100000 || mode&0o777 != 0o600 || parts[2] != "1" || sizeErr != nil || size < 1 || size > 4096 {
		return "", errors.New("bootstrap marker is not a private bounded regular file")
	}
	raw, err := runtime.ssh(ctx, alias, 30*time.Second, "head", "-c", "4097", "--", path)
	if err != nil || int64(len(raw)) != size {
		return "", errors.New("bootstrap marker changed or could not be read completely")
	}
	return string(raw), nil
}
