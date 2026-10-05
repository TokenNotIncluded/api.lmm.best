package appcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Bootstrap is the only operation that must run before the candidate operator
// has been staged. Select the installed, package-bound protocol without help;
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
	protocol, err := runtime.productionBootstrapProtocol(ctx, plan.TargetAlias, plan.GoRollback)
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
	createArgs := []string{productionOperatorBinary, protocol, "production", "workspace", "create", "--deployment-id", plan.DeploymentID}
	if plan.MaintenanceHandoff != nil && plan.MaintenanceHandoff.StoppedWriter != nil {
		createArgs = append(createArgs, "--maintenance-handoff", productionRemoteHandoffPath(*plan.MaintenanceHandoff), "--maintenance-handoff-sha256", plan.MaintenanceHandoff.SHA256)
	}
	output, createErr := runtime.ssh(ctx, plan.TargetAlias, 2*time.Minute, createArgs...)
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

// These revisions are the immutable go-v0.2.52 through go-v0.2.62 release
// commits. Their signed release metadata is checked before bootstrap, and the
// installed provider is checked against that package's payload digest above.
// 0.2.52 exposes deploy; 0.2.53 and later expose operator. Older binaries do
// not have the read-only capabilities command, so only these exact identities
// may use the compatibility path.
var productionBootstrapReleaseProtocols = map[string]struct {
	revision string
	protocol string
}{
	"go-v0.2.52": {"6d38798659035152e0b98ef23622fae3dfdfc0fe", "deploy"},
	"go-v0.2.53": {"09009b7382690aada64adf8b825e5b675a59ca47", "operator"},
	"go-v0.2.54": {"d6a2dbbc7f23d983ae17515cb4663b34377a0b70", "operator"},
	"go-v0.2.55": {"ae5bf90d7bf3cc97a8a1cb5d3a69ffec4b592267", "operator"},
	"go-v0.2.56": {"fd80de4d2ea718687cf6f14718129a200f2d6a04", "operator"},
	"go-v0.2.57": {"a3aa2e6c7eda5aef9f02b3ef6d206c571d711038", "operator"},
	"go-v0.2.58": {"9b08cb689518a85257c27e95d2dfff241043d40e", "operator"},
	"go-v0.2.59": {"46b2bfe1953a217ab6013833e7b81a71c5410470", "operator"},
	"go-v0.2.60": {"6c9e4dae914a99a91420548ea17945af2bc0a135", "operator"},
	"go-v0.2.61": {"afddecb3951e6dc189c37fa02eea3debe013c0af", "operator"},
	"go-v0.2.62": {"4b64392ee0b241ac738ed0635ba662716c5b9ea8", "operator"},
}

type productionBootstrapCapabilities struct {
	Format          int    `json:"format"`
	WorkspaceCreate string `json:"workspace_create"`
}

// Future operators publish this read-only machine contract. The compatibility
// table above is needed only for signed releases that predate the command.
func runProductionBootstrapCapabilities(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		_, _ = fmt.Fprintln(stderr, "capabilities does not accept arguments")
		return ExitUsage
	}
	if err := json.NewEncoder(stdout).Encode(productionBootstrapCapabilities{Format: 1, WorkspaceCreate: "operator"}); err != nil {
		return ExitError
	}
	return ExitOK
}

func (runtime *productionReleaseRuntime) productionBootstrapProtocol(ctx context.Context, alias string, pkg productionReleasePackagePlan) (string, error) {
	version, err := packageReleaseVersion(pkg.Version)
	if err != nil || pkg.Name != productionAURPackageName || pkg.ReleaseTag != "go-v"+version || !productionRevisionPattern.MatchString(pkg.GitRevision) {
		return "", errors.New("installed deployment package identity is invalid")
	}
	if known, ok := productionBootstrapReleaseProtocols[pkg.ReleaseTag]; ok {
		if pkg.GitRevision != known.revision {
			return "", errors.New("installed deployment release revision is unsupported")
		}
		return known.protocol, nil
	}
	output, err := runtime.ssh(ctx, alias, 30*time.Second, productionOperatorBinary, "operator", "capabilities")
	if err != nil {
		return "", fmt.Errorf("inspect installed deployment capabilities: %w", err)
	}
	return parseProductionBootstrapCapabilities(output)
}

func parseProductionBootstrapCapabilities(output []byte) (string, error) {
	if len(output) == 0 || len(output) > 4096 {
		return "", errors.New("installed deployment capabilities are empty or oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return "", errors.New("installed deployment capabilities are invalid JSON")
	}
	var capabilities productionBootstrapCapabilities
	seen := make(map[string]bool, 2)
	for decoder.More() {
		key, err := decoder.Token()
		field, ok := key.(string)
		if err != nil || !ok || seen[field] {
			return "", errors.New("installed deployment capabilities are ambiguous")
		}
		seen[field] = true
		switch field {
		case "format":
			err = decoder.Decode(&capabilities.Format)
		case "workspace_create":
			err = decoder.Decode(&capabilities.WorkspaceCreate)
		default:
			return "", errors.New("installed deployment capabilities contain an unsupported field")
		}
		if err != nil {
			return "", errors.New("installed deployment capabilities contain an invalid value")
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return "", errors.New("installed deployment capabilities are incomplete")
	}
	if _, err := decoder.Token(); err != io.EOF || len(seen) != 2 || capabilities.Format != 1 || capabilities.WorkspaceCreate != "operator" {
		return "", errors.New("installed deployment protocol is unsupported or ambiguous")
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
