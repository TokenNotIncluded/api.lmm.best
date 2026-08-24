package appcli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	productionTargetAlias             = "ArchDmit"
	productionOffhostAlias            = "archczy"
	productionOffhostExpectedHost     = "archczy"
	productionOffhostRoot             = "/home/arch/.local/state/lmm-api-production-backups"
	productionReleasePlanFormat       = 1
	productionReleaseStateFormat      = 1
	productionReleasePlanFilename     = "release-plan.json"
	productionReleasePlanHashFilename = "release-plan.sha256"
	productionReleaseStateFilename    = "release-state.json"
	productionReleaseRepository       = "https://github.com/LIghtJUNction/api.lmm.best"
	productionReleaseOIDCIssuer       = "https://token.actions.githubusercontent.com"
)

type productionReleasePlanOptions struct {
	Repo                     string
	Workspace                string
	DeploymentID             string
	GoPackage                string
	GoReleaseAsset           string
	GoReleaseBundle          string
	GoRollbackPackage        string
	GoRollbackReleaseAsset   string
	GoRollbackReleaseBundle  string
	WebPackage               string
	WebReleaseAsset          string
	WebReleaseBundle         string
	WebRollbackPackage       string
	WebRollbackReleaseAsset  string
	WebRollbackReleaseBundle string
	ProbeBinary              string
	AgeRecipientFile         string
	ObservationSeconds       int
	RollbackSeconds          int
	ManualConfirm            bool
	PreserveEdgePolicy       bool
	WithBackups              bool
}

type productionReleasePackagePlan struct {
	PackagePath           string `json:"package_path"`
	PackageSHA256         string `json:"package_sha256"`
	Name                  string `json:"name"`
	Version               string `json:"version"`
	Identity              string `json:"identity"`
	GitRevision           string `json:"git_revision"`
	ContractRevision      string `json:"contract_revision"`
	PayloadSHA256         string `json:"payload_sha256"`
	ReleaseAsset          string `json:"release_asset"`
	ReleaseAssetSHA256    string `json:"release_asset_sha256"`
	SignatureBundle       string `json:"signature_bundle"`
	SignatureBundleSHA256 string `json:"signature_bundle_sha256"`
	ReleaseTag            string `json:"release_tag"`
	Workflow              string `json:"workflow"`
}

type productionReleaseFilePlan struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type productionReleasePlan struct {
	Format              int                          `json:"format"`
	DeploymentID        string                       `json:"deployment_id"`
	CreatedUTC          time.Time                    `json:"created_utc"`
	ControllerWorkspace string                       `json:"controller_workspace"`
	Repository          string                       `json:"repository"`
	TargetAlias         string                       `json:"target_alias"`
	ExpectedHost        string                       `json:"expected_host"`
	OperatorUser        string                       `json:"operator_user"`
	ExpectedVersion     string                       `json:"expected_version"`
	GoCandidate         productionReleasePackagePlan `json:"go_candidate"`
	GoRollback          productionReleasePackagePlan `json:"go_rollback"`
	WebCandidate        productionReleasePackagePlan `json:"web_candidate"`
	WebRollback         productionReleasePackagePlan `json:"web_rollback"`
	ProbeBinary         productionReleaseFilePlan    `json:"probe_binary"`
	GoChanged           bool                         `json:"go_changed"`
	WebChanged          bool                         `json:"web_changed"`
	ObservationSeconds  int                          `json:"observation_seconds"`
	RollbackSeconds     int                          `json:"rollback_seconds"`
	ManualConfirm       bool                         `json:"manual_confirm"`
	PreserveEdgePolicy  bool                         `json:"preserve_edge_policy"`
	WithBackups         bool                         `json:"with_backups"`
	AgeRecipient        productionReleaseFilePlan    `json:"age_recipient,omitempty"`
}

type productionReleasePlanResult struct {
	DeploymentID string `json:"deployment_id"`
	Plan         string `json:"plan"`
	PlanSHA256   string `json:"plan_sha256"`
	Version      string `json:"version"`
	Revision     string `json:"revision"`
}

type productionReleaseRuntime struct {
	runner productionCommandRunner
	now    func() time.Time
}

func runProductionReleasePlan(args []string, stdout, stderr io.Writer) int {
	options, err := parseProductionReleasePlanOptions(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return ExitOK
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production plan: %v\n", ProgramName, err)
		return ExitUsage
	}
	runtime := &productionReleaseRuntime{runner: osProductionCommandRunner{}, now: time.Now}
	result, err := runtime.createPlan(context.Background(), options)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production plan: %v\n", ProgramName, err)
		return ExitError
	}
	return writeJSONCommandResult(result, stdout, stderr, "production release plan")
}

func parseProductionReleasePlanOptions(args []string, stderr io.Writer) (productionReleasePlanOptions, error) {
	options := productionReleasePlanOptions{ObservationSeconds: 180, RollbackSeconds: 600}
	flags := flag.NewFlagSet("deploy production plan", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.Repo, "repo", "", "clean api.lmm.best source checkout with fetched release tags")
	flags.StringVar(&options.Workspace, "workspace", "", "marker-owned controller workspace")
	flags.StringVar(&options.DeploymentID, "deployment-id", "", "unique release-scoped deployment ID")
	flags.StringVar(&options.GoPackage, "go-package", "", "candidate lmm-api-go-bin package")
	flags.StringVar(&options.GoReleaseAsset, "go-release-asset", "", "signed candidate Go release archive")
	flags.StringVar(&options.GoReleaseBundle, "go-release-bundle", "", "candidate Go Sigstore bundle")
	flags.StringVar(&options.GoRollbackPackage, "go-rollback-package", "", "exact installed Go rollback package")
	flags.StringVar(&options.GoRollbackReleaseAsset, "go-rollback-release-asset", "", "signed rollback Go release archive")
	flags.StringVar(&options.GoRollbackReleaseBundle, "go-rollback-release-bundle", "", "rollback Go Sigstore bundle")
	flags.StringVar(&options.WebPackage, "web-package", "", "candidate lmm-api-web-bin package")
	flags.StringVar(&options.WebReleaseAsset, "web-release-asset", "", "signed candidate Web release archive")
	flags.StringVar(&options.WebReleaseBundle, "web-release-bundle", "", "candidate Web Sigstore bundle")
	flags.StringVar(&options.WebRollbackPackage, "web-rollback-package", "", "exact installed Web rollback package")
	flags.StringVar(&options.WebRollbackReleaseAsset, "web-rollback-release-asset", "", "signed rollback Web release archive")
	flags.StringVar(&options.WebRollbackReleaseBundle, "web-rollback-release-bundle", "", "rollback Web Sigstore bundle")
	flags.StringVar(&options.ProbeBinary, "probe-binary", "", "candidate lmm-api binary extracted from the signed Go release")
	flags.BoolVar(&options.WithBackups, "with-backups", false, "require target, controller, and off-host backups before promotion")
	flags.StringVar(&options.AgeRecipientFile, "age-recipient-file", "", "age or SSH public recipient file used when backups are enabled")
	flags.IntVar(&options.ObservationSeconds, "observation-seconds", options.ObservationSeconds, "automatic stability observation window (120-360)")
	flags.IntVar(&options.RollbackSeconds, "rollback-seconds", options.RollbackSeconds, "fixed automatic rollback deadline (must be 600)")
	flags.BoolVar(&options.ManualConfirm, "manual-confirm", false, "leave a healthy release awaiting an explicit confirm command")
	flags.BoolVar(&options.PreserveEdgePolicy, "preserve-edge-policy", false, "preserve the active nginx edge policy during activation")
	flags.Usage = func() { writeProductionDeployUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		return productionReleasePlanOptions{}, err
	}
	if flags.NArg() != 0 {
		return productionReleasePlanOptions{}, errors.New("unexpected positional arguments")
	}
	if !productionIDPattern.MatchString(options.DeploymentID) {
		return productionReleasePlanOptions{}, errors.New("valid --deployment-id is required")
	}
	paths := map[string]*string{
		"--repo":                        &options.Repo,
		"--workspace":                   &options.Workspace,
		"--go-package":                  &options.GoPackage,
		"--go-release-asset":            &options.GoReleaseAsset,
		"--go-release-bundle":           &options.GoReleaseBundle,
		"--go-rollback-package":         &options.GoRollbackPackage,
		"--go-rollback-release-asset":   &options.GoRollbackReleaseAsset,
		"--go-rollback-release-bundle":  &options.GoRollbackReleaseBundle,
		"--web-package":                 &options.WebPackage,
		"--web-release-asset":           &options.WebReleaseAsset,
		"--web-release-bundle":          &options.WebReleaseBundle,
		"--web-rollback-package":        &options.WebRollbackPackage,
		"--web-rollback-release-asset":  &options.WebRollbackReleaseAsset,
		"--web-rollback-release-bundle": &options.WebRollbackReleaseBundle,
		"--probe-binary":                &options.ProbeBinary,
	}
	if options.WithBackups {
		paths["--age-recipient-file"] = &options.AgeRecipientFile
	} else if options.AgeRecipientFile != "" {
		return productionReleasePlanOptions{}, errors.New("--age-recipient-file requires --with-backups")
	}
	for label, value := range paths {
		if *value == "" {
			return productionReleasePlanOptions{}, fmt.Errorf("%s is required", label)
		}
		clean, err := cleanAbsoluteNonRoot(*value)
		if err != nil {
			return productionReleasePlanOptions{}, fmt.Errorf("invalid %s: %w", label, err)
		}
		*value = clean
	}
	if options.ObservationSeconds < 120 || options.ObservationSeconds > 360 {
		return productionReleasePlanOptions{}, errors.New("--observation-seconds must be between 120 and 360")
	}
	if options.RollbackSeconds != 600 {
		return productionReleasePlanOptions{}, errors.New("--rollback-seconds must be exactly 600")
	}
	return options, nil
}

func (runtime *productionReleaseRuntime) createPlan(ctx context.Context, options productionReleasePlanOptions) (productionReleasePlanResult, error) {
	if err := validateBuildRepository(options.Repo); err != nil {
		return productionReleasePlanResult{}, err
	}
	if err := validateBuildWorkspace(options.Workspace); err != nil {
		return productionReleasePlanResult{}, err
	}
	for label, path := range map[string]string{
		"candidate Go package":           options.GoPackage,
		"candidate Go release asset":     options.GoReleaseAsset,
		"candidate Go signature bundle":  options.GoReleaseBundle,
		"rollback Go package":            options.GoRollbackPackage,
		"rollback Go release asset":      options.GoRollbackReleaseAsset,
		"rollback Go signature bundle":   options.GoRollbackReleaseBundle,
		"candidate Web package":          options.WebPackage,
		"candidate Web release asset":    options.WebReleaseAsset,
		"candidate Web signature bundle": options.WebReleaseBundle,
		"rollback Web package":           options.WebRollbackPackage,
		"rollback Web release asset":     options.WebRollbackReleaseAsset,
		"rollback Web signature bundle":  options.WebRollbackReleaseBundle,
	} {
		if err := validateControllerArtifact(path, label, false); err != nil {
			return productionReleasePlanResult{}, err
		}
	}
	if err := validateControllerArtifact(options.ProbeBinary, "probe binary", true); err != nil {
		return productionReleasePlanResult{}, err
	}
	if options.WithBackups {
		if err := validateControllerArtifact(options.AgeRecipientFile, "age recipient", false); err != nil {
			return productionReleasePlanResult{}, err
		}
	}
	planPath := filepath.Join(options.Workspace, productionReleasePlanFilename)
	digestPath := filepath.Join(options.Workspace, productionReleasePlanHashFilename)
	statePath := filepath.Join(options.Workspace, productionReleaseStateFilename)
	for _, path := range []string{planPath, digestPath, statePath} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return productionReleasePlanResult{}, fmt.Errorf("release plan destination already exists or is unsafe: %s", path)
		}
	}
	localRuntime := &productionRuntime{runner: runtime.runner}
	goCandidate, err := runtime.verifyPackageEvidence(ctx, options.Repo, options.Workspace, localRuntime, productionAURPackageName, options.GoPackage, options.GoReleaseAsset, options.GoReleaseBundle)
	if err != nil {
		return productionReleasePlanResult{}, fmt.Errorf("candidate Go evidence: %w", err)
	}
	goRollback, err := runtime.verifyPackageEvidence(ctx, options.Repo, options.Workspace, localRuntime, productionAURPackageName, options.GoRollbackPackage, options.GoRollbackReleaseAsset, options.GoRollbackReleaseBundle)
	if err != nil {
		return productionReleasePlanResult{}, fmt.Errorf("rollback Go evidence: %w", err)
	}
	webCandidate, err := runtime.verifyPackageEvidence(ctx, options.Repo, options.Workspace, localRuntime, productionWebPackageName, options.WebPackage, options.WebReleaseAsset, options.WebReleaseBundle)
	if err != nil {
		return productionReleasePlanResult{}, fmt.Errorf("candidate Web evidence: %w", err)
	}
	webRollback, err := runtime.verifyPackageEvidence(ctx, options.Repo, options.Workspace, localRuntime, productionWebPackageName, options.WebRollbackPackage, options.WebRollbackReleaseAsset, options.WebRollbackReleaseBundle)
	if err != nil {
		return productionReleasePlanResult{}, fmt.Errorf("rollback Web evidence: %w", err)
	}
	goChanged := goCandidate.PackageSHA256 != goRollback.PackageSHA256
	webChanged := webCandidate.PackageSHA256 != webRollback.PackageSHA256
	if !goChanged && !webChanged {
		return productionReleasePlanResult{}, errors.New("candidate release is byte-identical to both rollback packages")
	}
	if err := validateChangedIdentity(goChanged, releasePlanMetadata(goCandidate), releasePlanMetadata(goRollback), goCandidate.PackageSHA256, goRollback.PackageSHA256); err != nil {
		return productionReleasePlanResult{}, fmt.Errorf("Go package pair: %w", err)
	}
	if err := validateChangedIdentity(webChanged, releasePlanMetadata(webCandidate), releasePlanMetadata(webRollback), webCandidate.PackageSHA256, webRollback.PackageSHA256); err != nil {
		return productionReleasePlanResult{}, fmt.Errorf("Web package pair: %w", err)
	}
	if goCandidate.ContractRevision != webCandidate.ContractRevision {
		return productionReleasePlanResult{}, errors.New("candidate Go API and Web route contract revisions differ")
	}
	if goRollback.ContractRevision != webRollback.ContractRevision {
		return productionReleasePlanResult{}, errors.New("rollback Go API and Web route contract revisions differ")
	}
	expectedVersion, err := packageReleaseVersion(goCandidate.Version)
	if err != nil {
		return productionReleasePlanResult{}, err
	}
	probeSHA256, err := sha256File(options.ProbeBinary)
	if err != nil {
		return productionReleasePlanResult{}, fmt.Errorf("hash probe binary: %w", err)
	}
	if probeSHA256 != goCandidate.PayloadSHA256 {
		return productionReleasePlanResult{}, errors.New("probe binary is not the binary in the signed candidate Go release and package")
	}
	plan := productionReleasePlan{
		Format:              productionReleasePlanFormat,
		DeploymentID:        options.DeploymentID,
		CreatedUTC:          utcSecond(runtime.now()),
		ControllerWorkspace: options.Workspace,
		Repository:          options.Repo,
		TargetAlias:         productionTargetAlias,
		ExpectedHost:        productionExpectedHost,
		OperatorUser:        productionOperatorUser,
		ExpectedVersion:     expectedVersion,
		GoCandidate:         goCandidate,
		GoRollback:          goRollback,
		WebCandidate:        webCandidate,
		WebRollback:         webRollback,
		ProbeBinary:         productionReleaseFilePlan{Path: options.ProbeBinary, SHA256: probeSHA256},
		GoChanged:           goChanged,
		WebChanged:          webChanged,
		ObservationSeconds:  options.ObservationSeconds,
		RollbackSeconds:     options.RollbackSeconds,
		ManualConfirm:       options.ManualConfirm,
		PreserveEdgePolicy:  options.PreserveEdgePolicy,
		WithBackups:         options.WithBackups,
	}
	if options.WithBackups {
		recipientSHA256, err := sha256File(options.AgeRecipientFile)
		if err != nil {
			return productionReleasePlanResult{}, fmt.Errorf("hash age recipient: %w", err)
		}
		plan.AgeRecipient = productionReleaseFilePlan{Path: options.AgeRecipientFile, SHA256: recipientSHA256}
	}
	if err := validateProductionReleasePlan(plan); err != nil {
		return productionReleasePlanResult{}, err
	}
	encoded, err := canonicalProductionReleasePlan(plan)
	if err != nil {
		return productionReleasePlanResult{}, err
	}
	if err := writeAtomicRegularFile(planPath, encoded, 0o600); err != nil {
		return productionReleasePlanResult{}, fmt.Errorf("write immutable release plan: %w", err)
	}
	planSHA256, err := sha256File(planPath)
	if err != nil {
		return productionReleasePlanResult{}, err
	}
	if err := writeAtomicRegularFile(digestPath, []byte(planSHA256+"  "+productionReleasePlanFilename+"\n"), 0o600); err != nil {
		return productionReleasePlanResult{}, fmt.Errorf("write release plan digest: %w", err)
	}
	return productionReleasePlanResult{DeploymentID: plan.DeploymentID, Plan: planPath, PlanSHA256: planSHA256, Version: expectedVersion, Revision: goCandidate.GitRevision}, nil
}

func (runtime *productionReleaseRuntime) verifyPackageEvidence(ctx context.Context, repo, workspace string, localRuntime *productionRuntime, expectedName, packagePath, releaseAsset, signatureBundle string) (productionReleasePackagePlan, error) {
	metadata, err := localRuntime.packageMetadata(ctx, packagePath, expectedName)
	if err != nil {
		return productionReleasePackagePlan{}, err
	}
	packageSHA256, err := sha256File(packagePath)
	if err != nil {
		return productionReleasePackagePlan{}, err
	}
	assetSHA256, err := sha256File(releaseAsset)
	if err != nil {
		return productionReleasePackagePlan{}, err
	}
	bundleSHA256, err := sha256File(signatureBundle)
	if err != nil {
		return productionReleasePackagePlan{}, err
	}
	if metadata.ReleaseAssetSHA256 != "" && metadata.ReleaseAssetSHA256 != assetSHA256 {
		return productionReleasePackagePlan{}, errors.New("package release-asset digest does not match supplied signed archive")
	}
	releaseVersion, err := packageReleaseVersion(metadata.Version)
	if err != nil {
		return productionReleasePackagePlan{}, err
	}
	workflow := "release-go.yml"
	tagPrefix := "go-v"
	if expectedName == productionWebPackageName {
		workflow = "release-web.yml"
		tagPrefix = "web-v"
	}
	releaseTag := tagPrefix + releaseVersion
	identity := productionReleaseRepository + "/.github/workflows/" + workflow + "@refs/tags/" + releaseTag
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandCosign, Args: []string{
		"verify-blob", "--bundle", signatureBundle,
		"--certificate-identity", identity,
		"--certificate-oidc-issuer", productionReleaseOIDCIssuer,
		releaseAsset,
	}, Timeout: 2 * time.Minute}); err != nil {
		return productionReleasePackagePlan{}, fmt.Errorf("verify Sigstore release identity: %w", err)
	}
	tagRevision, err := runtime.runner.Run(ctx, productionCommand{Name: commandGit, Args: []string{"-C", repo, "rev-list", "-n", "1", "refs/tags/" + releaseTag}})
	if err != nil || strings.TrimSpace(string(tagRevision)) != metadata.GitRevision {
		return productionReleasePackagePlan{}, errors.New("release tag does not resolve to the package Git revision")
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandGit, Args: []string{"-C", repo, "merge-base", "--is-ancestor", metadata.GitRevision, "origin/main"}}); err != nil {
		return productionReleasePackagePlan{}, errors.New("release revision is not an ancestor of origin/main")
	}
	archiveRevision, archiveContract, payload, err := runtime.readSignedReleasePayload(ctx, expectedName, releaseAsset)
	if err != nil {
		return productionReleasePackagePlan{}, err
	}
	if archiveRevision != metadata.GitRevision || archiveContract != metadata.ContractRevision {
		return productionReleasePackagePlan{}, errors.New("signed release metadata does not match package metadata")
	}
	payloadDigest := sha256.Sum256(payload)
	payloadSHA256 := hex.EncodeToString(payloadDigest[:])
	packagePayloadSHA256 := metadata.BinarySHA256
	if expectedName == productionWebPackageName {
		packagePayloadSHA256 = metadata.IndexSHA256
	}
	if payloadSHA256 != packagePayloadSHA256 {
		return productionReleasePackagePlan{}, errors.New("signed release payload does not match package payload")
	}
	if err := runtime.verifySignedPackageLayout(ctx, workspace, expectedName, metadata.Version, packagePath, releaseAsset, assetSHA256); err != nil {
		return productionReleasePackagePlan{}, err
	}
	return productionReleasePackagePlan{
		PackagePath:           packagePath,
		PackageSHA256:         packageSHA256,
		Name:                  metadata.Name,
		Version:               metadata.Version,
		Identity:              metadata.Identity,
		GitRevision:           metadata.GitRevision,
		ContractRevision:      metadata.ContractRevision,
		PayloadSHA256:         payloadSHA256,
		ReleaseAsset:          releaseAsset,
		ReleaseAssetSHA256:    assetSHA256,
		SignatureBundle:       signatureBundle,
		SignatureBundleSHA256: bundleSHA256,
		ReleaseTag:            releaseTag,
		Workflow:              workflow,
	}, nil
}

func (runtime *productionReleaseRuntime) readSignedReleasePayload(ctx context.Context, packageName, archive string) (string, string, []byte, error) {
	prefix := ""
	if packageName == productionAURPackageName {
		listing, err := runtime.runner.Run(ctx, productionCommand{Name: commandBsdtar, Args: []string{"-tf", archive}})
		if err != nil {
			return "", "", nil, fmt.Errorf("list signed Go release: %w", err)
		}
		root := ""
		for _, line := range strings.Split(strings.TrimSpace(string(listing)), "\n") {
			line = strings.TrimSuffix(strings.TrimSpace(line), "/")
			if line == "" || filepath.IsAbs(line) || strings.Contains(line, "\\") {
				continue
			}
			part := strings.SplitN(line, "/", 2)[0]
			if part == "." || part == ".." {
				return "", "", nil, errors.New("signed Go release contains an unsafe path")
			}
			if root == "" {
				root = part
			} else if root != part {
				return "", "", nil, errors.New("signed Go release has multiple top-level roots")
			}
		}
		if root == "" {
			return "", "", nil, errors.New("signed Go release is empty")
		}
		prefix = root + "/"
	}
	read := func(members ...string) ([]byte, error) {
		var last error
		for _, member := range members {
			output, err := runtime.runner.Run(ctx, productionCommand{Name: commandBsdtar, Args: []string{"-xOf", archive, prefix + member}})
			if err == nil && len(output) > 0 {
				return output, nil
			}
			last = err
		}
		return nil, last
	}
	revisionBytes, err := read("REVISION")
	if err != nil {
		return "", "", nil, errors.New("signed release Git revision is missing")
	}
	revision := strings.TrimSpace(string(revisionBytes))
	if !productionRevisionPattern.MatchString(revision) {
		return "", "", nil, errors.New("signed release Git revision is invalid")
	}
	contract := legacyContractRevision
	if contractBytes, contractErr := read("API_ROUTE_CONTRACT_REVISION"); contractErr == nil {
		contract = strings.TrimSpace(string(contractBytes))
	}
	if !productionContractPattern.MatchString(contract) {
		return "", "", nil, errors.New("signed release route-contract revision is invalid")
	}
	members := []string{"lmm-api", "lmm-api-go"}
	if packageName == productionWebPackageName {
		members = []string{"dist/index.html"}
	}
	payload, err := read(members...)
	if err != nil {
		return "", "", nil, errors.New("signed release payload is missing")
	}
	return revision, contract, payload, nil
}

func (runtime *productionReleaseRuntime) verifySignedPackageLayout(ctx context.Context, workspace, packageName, packageVersion, packagePath, releaseAsset, releaseAssetSHA256 string) error {
	temporaryRoot := filepath.Join(workspace, "tmp")
	if err := ensureRealDirectory(temporaryRoot, 0o700); err != nil {
		return err
	}
	extractionRoot, err := os.MkdirTemp(temporaryRoot, "release-layout.*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(extractionRoot)
	assetRoot := filepath.Join(extractionRoot, "asset")
	packageRoot := filepath.Join(extractionRoot, "package")
	for _, root := range []string{assetRoot, packageRoot} {
		if err := os.Mkdir(root, 0o700); err != nil {
			return err
		}
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandBsdtar, Args: []string{"-xf", releaseAsset, "-C", assetRoot}, Timeout: 2 * time.Minute}); err != nil {
		return fmt.Errorf("extract signed release for layout verification: %w", err)
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandBsdtar, Args: []string{"-xf", packagePath, "-C", packageRoot}, Timeout: 2 * time.Minute}); err != nil {
		return fmt.Errorf("extract package for layout verification: %w", err)
	}
	signedRoot := assetRoot
	if packageName == productionAURPackageName {
		entries, err := os.ReadDir(assetRoot)
		if err != nil || len(entries) != 1 || !entries[0].IsDir() || entries[0].Type()&os.ModeSymlink != 0 {
			return errors.New("signed Go release must contain exactly one real top-level directory")
		}
		signedRoot = filepath.Join(assetRoot, entries[0].Name())
	}
	expected := make(map[string]string)
	if err := filepath.WalkDir(signedRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == signedRoot || entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 || info.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("signed release contains an unsafe payload: %s", path)
		}
		relative, err := filepath.Rel(signedRoot, path)
		if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("signed release payload escaped its root")
		}
		packageRelative, ignored, err := signedPackageMember(packageName, relative)
		if err != nil {
			return err
		}
		if ignored {
			return nil
		}
		if previous, exists := expected[packageRelative]; exists && previous != path {
			return fmt.Errorf("signed release maps multiple payloads to %s", packageRelative)
		}
		expected[packageRelative] = path
		return nil
	}); err != nil {
		return err
	}
	if len(expected) == 0 {
		return errors.New("signed release has no package payload")
	}
	packageFiles := make(map[string]string)
	legacyAlias := false
	if err := filepath.WalkDir(packageRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == packageRoot || entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(packageRoot, path)
		if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("package payload escaped its root")
		}
		if !strings.Contains(relative, string(filepath.Separator)) && (relative == ".PKGINFO" || relative == ".BUILDINFO" || relative == ".MTREE" || relative == ".INSTALL") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if packageName != productionAURPackageName || relative != "usr/bin/lmm-api-go" {
				return fmt.Errorf("package contains an unexpected symlink: %s", relative)
			}
			target, err := os.Readlink(path)
			if err != nil || target != "lmm-api" {
				return errors.New("legacy CLI compatibility symlink has an unsafe target")
			}
			legacyAlias = true
			return nil
		}
		if !info.Mode().IsRegular() || info.Size() == 0 || info.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("package contains an unsafe payload: %s", relative)
		}
		packageFiles[relative] = path
		return nil
	}); err != nil {
		return err
	}
	releaseDigestRelative := "usr/share/doc/" + packageName + "/RELEASE_ASSET_SHA256"
	releaseDigestPath, ok := packageFiles[releaseDigestRelative]
	if !ok {
		if !legacyPackageWithoutEmbeddedReleaseDigest(packageName, packageVersion) {
			return errors.New("package does not preserve its signed release-asset digest")
		}
	} else {
		releaseDigestBytes, err := os.ReadFile(releaseDigestPath)
		if err != nil || string(releaseDigestBytes) != releaseAssetSHA256+"\n" {
			return errors.New("package release-asset digest evidence is invalid")
		}
		delete(packageFiles, releaseDigestRelative)
	}
	if len(packageFiles) != len(expected) {
		return errors.New("package payload set differs from the signed release")
	}
	for relative, signedPath := range expected {
		packageFile, ok := packageFiles[relative]
		if !ok {
			return fmt.Errorf("package omits signed release payload: %s", relative)
		}
		signedDigest, err := sha256File(signedPath)
		if err != nil {
			return err
		}
		packageDigest, err := sha256File(packageFile)
		if err != nil || packageDigest != signedDigest {
			return fmt.Errorf("package payload differs from signed release: %s", relative)
		}
	}
	canonicalExecutable := filepath.Join(packageRoot, "usr/bin/lmm-api")
	if packageName == productionAURPackageName {
		t1, err := isT1SingleCLIPackage(packageName, packageVersion)
		if err != nil {
			return err
		}
		if t1 && legacyAlias {
			return errors.New("T1 package still exposes the legacy CLI compatibility link")
		}
		if !t1 && !legacyAlias {
			return errors.New("T0 rollback package lacks the legacy CLI compatibility link")
		}
	}
	if packageName == productionWebPackageName {
		canonicalExecutable = filepath.Join(packageRoot, "usr/lib/lmm-api-web/lmm-api-web-activate")
	}
	executableInfo, err := os.Stat(canonicalExecutable)
	if err != nil || executableInfo.Mode().Perm()&0o111 == 0 {
		return errors.New("package canonical executable is missing or not executable")
	}
	return nil
}

func legacyPackageWithoutEmbeddedReleaseDigest(packageName, packageVersion string) bool {
	switch packageName + " " + packageVersion {
	case productionAURPackageName + " 0.1.34-1", productionAURPackageName + " 0.1.57-1",
		productionWebPackageName + " 0.1.40-1", productionWebPackageName + " 0.1.41-1":
		return true
	default:
		return false
	}
}

func signedPackageMember(packageName, relative string) (packageRelative string, ignored bool, err error) {
	if filepath.IsAbs(relative) || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false, errors.New("signed release member is unsafe")
	}
	if packageName == productionWebPackageName {
		switch {
		case strings.HasPrefix(relative, "dist/"):
			return filepath.Join("usr/share/lmm-api-web/frontend-dist", strings.TrimPrefix(relative, "dist/")), false, nil
		case relative == "lmm-api-web-activate":
			return "usr/lib/lmm-api-web/lmm-api-web-activate", false, nil
		case relative == "frontend-release.sh":
			return "usr/lib/lmm-api-web/frontend-release.sh", false, nil
		case relative == "LICENSE", relative == "NOTICE", relative == "THIRD-PARTY-LICENSES.md":
			return "usr/share/licenses/" + packageName + "/" + relative, false, nil
		case relative == "REVISION", relative == "API_ROUTE_CONTRACT_REVISION":
			return "usr/share/doc/" + packageName + "/" + relative, false, nil
		default:
			return "", false, fmt.Errorf("signed Web release contains an unmapped payload: %s", relative)
		}
	}
	switch {
	case relative == "lmm-api" || relative == "lmm-api-go":
		return "usr/bin/lmm-api", false, nil
	case relative == "lmm-api.service":
		return "usr/lib/systemd/system/lmm-api.service", false, nil
	case relative == "lmm-api-go.env":
		return "etc/lmm-api-go/lmm-api-go.env", false, nil
	case relative == "lmm-api-memory.conf":
		return "usr/lib/systemd/system/lmm-api.service.d/20-memory.conf", false, nil
	case relative == "lmm-api-operator.sysusers":
		return "usr/lib/sysusers.d/lmm-api-operator.conf", false, nil
	case relative == "lmm-api-operator.tmpfiles":
		return "usr/lib/tmpfiles.d/lmm-api-operator.conf", false, nil
	case relative == "lmm-api-operator.sudoers":
		return "etc/sudoers.d/lmm-api-operator", false, nil
	case relative == "geoip2-country-update.service", relative == "geoip2-country-update.timer":
		return "usr/lib/systemd/system/" + relative, false, nil
	case strings.HasPrefix(relative, "edge-policy/"):
		return filepath.Join("usr/share/lmm-api-go", relative), false, nil
	case relative == "LICENSE", relative == "NOTICE", relative == "THIRD-PARTY-LICENSES.md":
		return "usr/share/licenses/" + packageName + "/" + relative, false, nil
	case relative == "REVISION", relative == "API_ROUTE_CONTRACT_REVISION":
		return "usr/share/doc/" + packageName + "/" + relative, false, nil
	default:
		return "", false, fmt.Errorf("signed Go release contains an unmapped payload: %s", relative)
	}
}

func isT1SingleCLIPackage(packageName, packageVersion string) (bool, error) {
	if packageName == productionSourcePackageName {
		return true, nil
	}
	if packageName != productionAURPackageName {
		return false, errors.New("single-CLI transition check received an unsupported package")
	}
	return numericPackageReleaseAtLeast(packageVersion, [3]int{0, 1, 59})
}

func isIntegratedOperatorPackage(packageName, packageVersion string) (bool, error) {
	if packageName == productionSourcePackageName {
		return true, nil
	}
	if packageName != productionAURPackageName {
		return false, nil
	}
	return numericPackageReleaseAtLeast(packageVersion, [3]int{0, 1, 58})
}

func numericPackageReleaseAtLeast(packageVersion string, minimum [3]int) (bool, error) {
	releaseVersion, err := packageReleaseVersion(packageVersion)
	if err != nil {
		return false, err
	}
	parts := strings.Split(releaseVersion, ".")
	if len(parts) != len(minimum) {
		return false, nil
	}
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return false, nil
		}
		if value != minimum[index] {
			return value > minimum[index], nil
		}
	}
	return true, nil
}

func packageReleaseVersion(packageVersion string) (string, error) {
	separator := strings.LastIndexByte(packageVersion, '-')
	if separator <= 0 || !productionVersionPattern.MatchString(packageVersion[:separator]) || !productionPkgrelPattern.MatchString(packageVersion[separator+1:]) {
		return "", errors.New("package version is invalid")
	}
	return packageVersion[:separator], nil
}

func releasePlanMetadata(plan productionReleasePackagePlan) productionPackageMetadata {
	metadata := productionPackageMetadata{
		Name:             plan.Name,
		Version:          plan.Version,
		Identity:         plan.Identity,
		GitRevision:      plan.GitRevision,
		ContractRevision: plan.ContractRevision,
	}
	if plan.Name == productionWebPackageName {
		metadata.IndexSHA256 = plan.PayloadSHA256
	} else {
		metadata.BinarySHA256 = plan.PayloadSHA256
	}
	return metadata
}

func canonicalProductionReleasePlan(plan productionReleasePlan) ([]byte, error) {
	encoded, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func loadProductionReleasePlan(path, expectedSHA256 string) (productionReleasePlan, error) {
	clean, err := cleanAbsoluteNonRoot(path)
	if err != nil {
		return productionReleasePlan{}, fmt.Errorf("invalid --plan: %w", err)
	}
	if !productionSHA256Pattern.MatchString(expectedSHA256) {
		return productionReleasePlan{}, errors.New("--plan-sha256 must be 64 lowercase hexadecimal characters")
	}
	raw, err := readPrivateRegularFile(clean, 2<<20)
	if err != nil {
		return productionReleasePlan{}, fmt.Errorf("read immutable release plan: %w", err)
	}
	actual := sha256.Sum256(raw)
	if hex.EncodeToString(actual[:]) != expectedSHA256 {
		return productionReleasePlan{}, errors.New("immutable release plan SHA-256 mismatch")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var plan productionReleasePlan
	if err := decoder.Decode(&plan); err != nil {
		return productionReleasePlan{}, fmt.Errorf("decode immutable release plan: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return productionReleasePlan{}, errors.New("immutable release plan contains trailing JSON")
	}
	if err := validateProductionReleasePlan(plan); err != nil {
		return productionReleasePlan{}, err
	}
	canonical, err := canonicalProductionReleasePlan(plan)
	if err != nil || !bytes.Equal(canonical, raw) {
		return productionReleasePlan{}, errors.New("immutable release plan is not canonical JSON")
	}
	if filepath.Dir(clean) != plan.ControllerWorkspace || filepath.Base(clean) != productionReleasePlanFilename {
		return productionReleasePlan{}, errors.New("release plan is not in its marker-owned controller workspace")
	}
	return plan, nil
}

func validateProductionReleasePlan(plan productionReleasePlan) error {
	if plan.Format != productionReleasePlanFormat {
		return errors.New("unsupported release plan format")
	}
	if !productionIDPattern.MatchString(plan.DeploymentID) {
		return errors.New("release plan deployment ID is invalid")
	}
	if plan.CreatedUTC.IsZero() || plan.CreatedUTC.Location() != time.UTC || plan.CreatedUTC.Nanosecond() != 0 {
		return errors.New("release plan creation time is invalid")
	}
	if plan.TargetAlias != productionTargetAlias || plan.ExpectedHost != productionExpectedHost || plan.OperatorUser != productionOperatorUser {
		return errors.New("release plan target identity is invalid")
	}
	if !productionVersionPattern.MatchString(plan.ExpectedVersion) || plan.ObservationSeconds < 120 || plan.ObservationSeconds > 360 || plan.RollbackSeconds != 600 {
		return errors.New("release plan timing or version contract is invalid")
	}
	workspace, err := cleanAbsoluteNonRoot(plan.ControllerWorkspace)
	if err != nil || workspace != plan.ControllerWorkspace {
		return errors.New("release plan controller workspace is invalid")
	}
	repo, err := cleanAbsoluteNonRoot(plan.Repository)
	if err != nil || repo != plan.Repository {
		return errors.New("release plan repository is invalid")
	}
	packages := []productionReleasePackagePlan{plan.GoCandidate, plan.GoRollback, plan.WebCandidate, plan.WebRollback}
	for _, item := range packages {
		for _, path := range []string{item.PackagePath, item.ReleaseAsset, item.SignatureBundle} {
			clean, err := cleanAbsoluteNonRoot(path)
			if err != nil || clean != path {
				return errors.New("release plan contains an invalid artifact path")
			}
		}
		for _, digest := range []string{item.PackageSHA256, item.PayloadSHA256, item.ReleaseAssetSHA256, item.SignatureBundleSHA256} {
			if !productionSHA256Pattern.MatchString(digest) {
				return errors.New("release plan contains an invalid artifact digest")
			}
		}
		if item.Identity != item.Name+" "+item.Version || !productionRevisionPattern.MatchString(item.GitRevision) || !productionContractPattern.MatchString(item.ContractRevision) {
			return errors.New("release plan contains invalid package metadata")
		}
		if item.Name != productionAURPackageName && item.Name != productionWebPackageName {
			return errors.New("release plan contains an unsupported package")
		}
		releaseVersion, err := packageReleaseVersion(item.Version)
		if err != nil {
			return err
		}
		prefix, workflow := "go-v", "release-go.yml"
		if item.Name == productionWebPackageName {
			prefix, workflow = "web-v", "release-web.yml"
		}
		if item.ReleaseTag != prefix+releaseVersion || item.Workflow != workflow {
			return errors.New("release plan tag or workflow identity is invalid")
		}
	}
	if plan.GoCandidate.Name != productionAURPackageName || plan.GoRollback.Name != productionAURPackageName || plan.WebCandidate.Name != productionWebPackageName || plan.WebRollback.Name != productionWebPackageName {
		return errors.New("release plan package roles are invalid")
	}
	if !productionPackageMatches(plan.GoCandidate.Version, plan.ExpectedVersion) || plan.GoCandidate.ContractRevision != plan.WebCandidate.ContractRevision || plan.GoRollback.ContractRevision != plan.WebRollback.ContractRevision {
		return errors.New("release plan version or route-contract pairing is invalid")
	}
	if err := validateChangedIdentity(plan.GoChanged, releasePlanMetadata(plan.GoCandidate), releasePlanMetadata(plan.GoRollback), plan.GoCandidate.PackageSHA256, plan.GoRollback.PackageSHA256); err != nil {
		return fmt.Errorf("release plan Go pair: %w", err)
	}
	if err := validateChangedIdentity(plan.WebChanged, releasePlanMetadata(plan.WebCandidate), releasePlanMetadata(plan.WebRollback), plan.WebCandidate.PackageSHA256, plan.WebRollback.PackageSHA256); err != nil {
		return fmt.Errorf("release plan Web pair: %w", err)
	}
	probe, err := cleanAbsoluteNonRoot(plan.ProbeBinary.Path)
	if err != nil || probe != plan.ProbeBinary.Path || !productionSHA256Pattern.MatchString(plan.ProbeBinary.SHA256) || plan.ProbeBinary.SHA256 != plan.GoCandidate.PayloadSHA256 {
		return errors.New("release plan probe identity is invalid")
	}
	if plan.WithBackups {
		recipient, err := cleanAbsoluteNonRoot(plan.AgeRecipient.Path)
		if err != nil || recipient != plan.AgeRecipient.Path || !productionSHA256Pattern.MatchString(plan.AgeRecipient.SHA256) {
			return errors.New("release plan age recipient is invalid")
		}
	} else if plan.AgeRecipient.Path != "" || plan.AgeRecipient.SHA256 != "" {
		return errors.New("release plan has undeclared backup evidence")
	}
	return validateReleaseBasenameCollisions(plan)
}

func validateReleaseBasenameCollisions(plan productionReleasePlan) error {
	seen := make(map[string]string)
	files := []productionReleaseFilePlan{
		{Path: plan.GoCandidate.PackagePath, SHA256: plan.GoCandidate.PackageSHA256},
		{Path: plan.GoRollback.PackagePath, SHA256: plan.GoRollback.PackageSHA256},
		{Path: plan.WebCandidate.PackagePath, SHA256: plan.WebCandidate.PackageSHA256},
		{Path: plan.WebRollback.PackagePath, SHA256: plan.WebRollback.PackageSHA256},
		plan.ProbeBinary,
	}
	if plan.WithBackups {
		files = append(files, plan.AgeRecipient)
	}
	for _, file := range files {
		base := filepath.Base(file.Path)
		if base == "." || base == string(filepath.Separator) || base == productionReleasePlanFilename || base == productionReleasePlanHashFilename {
			return errors.New("release plan contains an unsafe staging basename")
		}
		if digest, exists := seen[base]; exists && digest != file.SHA256 {
			return fmt.Errorf("release plan staging basename collision: %s", base)
		}
		seen[base] = file.SHA256
	}
	return nil
}

func validateProductionReleasePlanArtifacts(ctx context.Context, runtime *productionReleaseRuntime, plan productionReleasePlan) error {
	if err := validateBuildRepository(plan.Repository); err != nil {
		return err
	}
	if err := validateBuildWorkspace(plan.ControllerWorkspace); err != nil {
		return err
	}
	files := []struct {
		path   string
		digest string
		label  string
	}{
		{plan.GoCandidate.PackagePath, plan.GoCandidate.PackageSHA256, "candidate Go package"},
		{plan.GoRollback.PackagePath, plan.GoRollback.PackageSHA256, "rollback Go package"},
		{plan.WebCandidate.PackagePath, plan.WebCandidate.PackageSHA256, "candidate Web package"},
		{plan.WebRollback.PackagePath, plan.WebRollback.PackageSHA256, "rollback Web package"},
		{plan.GoCandidate.ReleaseAsset, plan.GoCandidate.ReleaseAssetSHA256, "candidate Go release asset"},
		{plan.GoRollback.ReleaseAsset, plan.GoRollback.ReleaseAssetSHA256, "rollback Go release asset"},
		{plan.WebCandidate.ReleaseAsset, plan.WebCandidate.ReleaseAssetSHA256, "candidate Web release asset"},
		{plan.WebRollback.ReleaseAsset, plan.WebRollback.ReleaseAssetSHA256, "rollback Web release asset"},
		{plan.GoCandidate.SignatureBundle, plan.GoCandidate.SignatureBundleSHA256, "candidate Go signature bundle"},
		{plan.GoRollback.SignatureBundle, plan.GoRollback.SignatureBundleSHA256, "rollback Go signature bundle"},
		{plan.WebCandidate.SignatureBundle, plan.WebCandidate.SignatureBundleSHA256, "candidate Web signature bundle"},
		{plan.WebRollback.SignatureBundle, plan.WebRollback.SignatureBundleSHA256, "rollback Web signature bundle"},
		{plan.ProbeBinary.Path, plan.ProbeBinary.SHA256, "probe binary"},
	}
	if plan.WithBackups {
		files = append(files, struct {
			path   string
			digest string
			label  string
		}{plan.AgeRecipient.Path, plan.AgeRecipient.SHA256, "age recipient"})
	}
	for _, file := range files {
		executable := file.label == "probe binary"
		if err := validateControllerArtifact(file.path, file.label, executable); err != nil {
			return err
		}
		digest, err := sha256File(file.path)
		if err != nil || digest != file.digest {
			return fmt.Errorf("%s changed after planning", file.label)
		}
	}
	localRuntime := &productionRuntime{runner: runtime.runner}
	checks := []struct {
		want        productionReleasePackagePlan
		packageName string
	}{
		{plan.GoCandidate, productionAURPackageName},
		{plan.GoRollback, productionAURPackageName},
		{plan.WebCandidate, productionWebPackageName},
		{plan.WebRollback, productionWebPackageName},
	}
	for _, check := range checks {
		got, err := runtime.verifyPackageEvidence(ctx, plan.Repository, plan.ControllerWorkspace, localRuntime, check.packageName, check.want.PackagePath, check.want.ReleaseAsset, check.want.SignatureBundle)
		if err != nil {
			return err
		}
		if got != check.want {
			return errors.New("release evidence no longer matches immutable plan")
		}
	}
	return nil
}

func validateControllerArtifact(path, label string, executable bool) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s is missing, empty, writable, or unsafe", label)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 || stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("%s ownership or link count is unsafe", label)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || filepath.Clean(canonical) != filepath.Clean(path) {
		return fmt.Errorf("%s path contains a symlink", label)
	}
	if executable && info.Mode().Perm()&0o100 == 0 {
		return fmt.Errorf("%s is not executable", label)
	}
	return nil
}
