package appcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const merchantStoreCapabilityMember = "MERCHANT_STORE_WRITER_CAPABILITY"

// The status protocol is deliberately closed. Adding a capability requires a
// signed marker and the actual provider to agree; unknown future values fail.
type productionMerchantStoreWriterStatus struct {
	RequiredCapability int  `json:"required_capability"`
	WriterCapability   int  `json:"writer_capability"`
	NewWritesAllowed   bool `json:"new_writes_allowed"`
	SupportsWriterGate bool `json:"supports_writer_gate"`
	SupportsVariants   bool `json:"supports_variants"`
}

type productionMerchantStoreWriterTarget struct {
	Capability         int    `json:"capability"`
	PackageSHA256      string `json:"package_sha256"`
	PayloadSHA256      string `json:"payload_sha256"`
	SourceRevision     string `json:"source_revision"`
	ReleaseAssetSHA256 string `json:"release_asset_sha256"`
	StatusSHA256       string `json:"status_sha256"`
}

// This belongs to the canonical plan and manifest, never to mutable runtime
// memory alone. The startup seal includes every ordered root-private EnvFile;
// the database tuple also records the actual business role used for status.
type productionMerchantStoreWriterContract struct {
	Format             int                                 `json:"format"`
	RequiredCapability int                                 `json:"required_capability"`
	SystemIdentifier   string                              `json:"system_identifier"`
	Database           string                              `json:"database"`
	DatabaseOID        int64                               `json:"database_oid"`
	Schema             string                              `json:"schema"`
	SchemaOID          int64                               `json:"schema_oid"`
	Role               string                              `json:"role"`
	StartupSHA256      string                              `json:"startup_sha256"`
	SignedUnitSHA256   string                              `json:"signed_unit_sha256"`
	RecoveryPolicy     string                              `json:"recovery_policy"`
	Candidate          productionMerchantStoreWriterTarget `json:"candidate"`
	Rollback           productionMerchantStoreWriterTarget `json:"rollback"`
}

func validMerchantStoreCapability(value int) bool { return value >= 1 && value <= 7 }

func parseMerchantStoreCapability(data []byte) (int, error) {
	if len(data) == 2 && data[1] == '\n' && data[0] >= '1' && data[0] <= '7' {
		return int(data[0] - '0'), nil
	}
	return 0, errors.New("invalid signed merchant-store writer capability")
}

// Only an authentic inventory can establish absence. A present malformed,
// duplicated, or unreadable marker is an error, never a legacy exception.
func (runtime *productionRuntime) merchantStorePackageCapability(ctx context.Context, packagePath, packageName string) (int, error) {
	member := "usr/share/doc/" + packageName + "/" + merchantStoreCapabilityMember
	listing, err := runtime.runner.Run(ctx, productionCommand{Name: commandBsdtar, Args: []string{"-tf", packagePath}, OutputLimit: 1 << 20})
	if err != nil {
		return 0, errors.New("cannot inspect signed merchant-store capability inventory")
	}
	count := 0
	for _, entry := range strings.Split(string(listing), "\n") {
		if strings.TrimPrefix(entry, "./") == member {
			count++
		}
	}
	if count == 0 {
		return 0, nil // Metadata only: ordinary writer qualification rejects zero.
	}
	if count != 1 {
		return 0, errors.New("ambiguous signed merchant-store capability member")
	}
	data, err := runtime.runner.Run(ctx, productionCommand{Name: commandBsdtar, Args: []string{"-xOf", packagePath, member}, OutputLimit: 16})
	if err != nil {
		return 0, errors.New("cannot read signed merchant-store capability member")
	}
	return parseMerchantStoreCapability(data)
}

func parseMerchantStoreWriterStatus(data []byte) (productionMerchantStoreWriterStatus, error) {
	var result productionMerchantStoreWriterStatus
	if len(data) == 0 || len(data) > 8192 {
		return result, errors.New("invalid merchant-store writer status")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return result, errors.New("invalid merchant-store writer status")
	}
	seen := map[string]bool{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok || seen[key] {
			return result, errors.New("ambiguous merchant-store writer status")
		}
		seen[key] = true
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return result, errors.New("invalid merchant-store writer status value")
		}
		switch key {
		case "required_capability":
			err = json.Unmarshal(raw, &result.RequiredCapability)
		case "writer_capability":
			err = json.Unmarshal(raw, &result.WriterCapability)
		case "new_writes_allowed":
			err = json.Unmarshal(raw, &result.NewWritesAllowed)
		case "supports_writer_gate":
			err = json.Unmarshal(raw, &result.SupportsWriterGate)
		case "supports_variants":
			err = json.Unmarshal(raw, &result.SupportsVariants)
		default:
			return result, errors.New("unsupported merchant-store writer status field")
		}
		if err != nil {
			return result, errors.New("invalid merchant-store writer status value")
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') || len(seen) != 5 || decoder.Decode(&struct{}{}) != io.EOF ||
		!validMerchantStoreCapability(result.RequiredCapability) || !validMerchantStoreCapability(result.WriterCapability) ||
		!result.SupportsWriterGate || result.SupportsVariants != (result.WriterCapability >= 2) ||
		result.NewWritesAllowed != (result.WriterCapability >= result.RequiredCapability) {
		return result, errors.New("inconsistent merchant-store writer status")
	}
	return result, nil
}

func merchantStoreWriterTargetAllowed(required, capability int) error {
	if !validMerchantStoreCapability(required) || !validMerchantStoreCapability(capability) {
		return errors.New("target lacks a supported merchant-store writer protocol")
	}
	if capability < required {
		// Freezing new writes does not prove payment/refund callback safety.
		// There is no cap0 or automatic below-floor rollback exception.
		return errors.New("merchant-store recovery target is below the active capability floor")
	}
	return nil
}

func qualifyMerchantStoreWriterStatus(output []byte, commandError error, contract *productionMerchantStoreWriterContract, expected productionMerchantStoreWriterTarget) error {
	if commandError != nil {
		return errors.New("merchant-store target actual status command failed or is unsupported")
	}
	status, err := parseMerchantStoreWriterStatus(output)
	if contract == nil || err != nil || status.RequiredCapability != contract.RequiredCapability || status.WriterCapability != expected.Capability ||
		!status.NewWritesAllowed || startupContentSHA256(output) != expected.StatusSHA256 {
		return errors.New("merchant-store target actual status differs from its immutable qualification")
	}
	return merchantStoreWriterTargetAllowed(contract.RequiredCapability, status.WriterCapability)
}

func validateMerchantStoreWriterTarget(target productionMerchantStoreWriterTarget, floor int) error {
	if err := merchantStoreWriterTargetAllowed(floor, target.Capability); err != nil {
		return err
	}
	for _, digest := range []string{target.PackageSHA256, target.PayloadSHA256, target.ReleaseAssetSHA256, target.StatusSHA256} {
		if !productionSHA256Pattern.MatchString(digest) {
			return errors.New("merchant-store writer target has an incomplete artifact or actual status binding")
		}
	}
	if !productionRevisionPattern.MatchString(target.SourceRevision) {
		return errors.New("merchant-store writer source binding is invalid")
	}
	return nil
}

func validateMerchantStoreWriterContract(contract *productionMerchantStoreWriterContract) error {
	if contract == nil || contract.RecoveryPolicy != "same-floor-writable" {
		return errors.New("ordinary merchant writer requires same-floor writable candidate and retained providers")
	}
	if err := validateMerchantStoreWriterHostIdentity(contract); err != nil {
		return err
	}
	if err := validateMerchantStoreWriterTarget(contract.Candidate, contract.RequiredCapability); err != nil {
		return fmt.Errorf("merchant-store candidate: %w", err)
	}
	if err := validateMerchantStoreWriterTarget(contract.Rollback, contract.RequiredCapability); err != nil {
		return fmt.Errorf("merchant-store rollback: %w", err)
	}
	return nil
}

// Physical/startup identity is common to two-provider upgrades and the separate
// single-provider startup authority. It grants no artifact or recovery role.
func validateMerchantStoreWriterHostIdentity(contract *productionMerchantStoreWriterContract) error {
	if contract == nil || contract.Format != 1 || !validMerchantStoreCapability(contract.RequiredCapability) ||
		!isDatabaseSchema(contract.Schema) ||
		contract.Database == "" || len(contract.Database) > 63 || contract.DatabaseOID <= 0 || contract.SchemaOID <= 0 ||
		contract.Role == "" || len(contract.Role) > 63 || strings.ContainsAny(contract.Database+contract.Role, "\x00\r\n") ||
		!productionSHA256Pattern.MatchString(contract.StartupSHA256) || !productionSHA256Pattern.MatchString(contract.SignedUnitSHA256) {
		return errors.New("merchant-store writer contract is incomplete")
	}
	identifier, err := strconv.ParseUint(contract.SystemIdentifier, 10, 64)
	if err != nil || identifier == 0 || strconv.FormatUint(identifier, 10) != contract.SystemIdentifier {
		return errors.New("merchant-store physical PostgreSQL identity is invalid")
	}
	return nil
}

func validateMerchantStoreWriterPlan(plan productionReleasePlan) error {
	if plan.MerchantStoreWriter == nil {
		return nil // Historical plans remain readable; lifecycle checks fail closed.
	}
	contract := plan.MerchantStoreWriter
	if err := validateMerchantStoreWriterContract(contract); err != nil {
		return err
	}
	if plan.SchemaMode != productionSchemaModeVerifyExisting || plan.ExistingSchemaContract == nil || plan.MaintenanceHandoff != nil {
		return errors.New("ordinary merchant writer qualification requires the sealed verify-existing path")
	}
	schema := plan.ExistingSchemaContract
	if schema.SystemIdentifier != contract.SystemIdentifier || schema.Database != contract.Database || schema.DatabaseOID != contract.DatabaseOID ||
		schema.Schema != contract.Schema || schema.SchemaOID != contract.SchemaOID || schema.StartupSHA256 != contract.StartupSHA256 || schema.SignedUnitSHA256 != contract.SignedUnitSHA256 {
		return errors.New("merchant writer and existing-schema startup/database contracts differ")
	}
	for _, pair := range []struct {
		planned productionReleasePackagePlan
		target  productionMerchantStoreWriterTarget
	}{{plan.GoCandidate, contract.Candidate}, {plan.GoRollback, contract.Rollback}} {
		if pair.planned.MerchantStoreWriterCapability != pair.target.Capability || pair.planned.PackageSHA256 != pair.target.PackageSHA256 ||
			pair.planned.PayloadSHA256 != pair.target.PayloadSHA256 || pair.planned.GitRevision != pair.target.SourceRevision || pair.planned.ReleaseAssetSHA256 != pair.target.ReleaseAssetSHA256 {
			return errors.New("merchant writer qualification differs from the signed plan package tuple")
		}
	}
	return nil
}

func loadMerchantStoreWriterContract(path, digest string) (*productionMerchantStoreWriterContract, error) {
	if path == "" && digest == "" {
		return nil, nil
	}
	clean, err := cleanAbsoluteNonRoot(path)
	if err != nil || clean != path || !productionSHA256Pattern.MatchString(digest) {
		return nil, errors.New("merchant writer contract requires a canonical private file and SHA-256")
	}
	runtime := productionRuntime{requiredOwnerUID: uint32(os.Geteuid())}
	raw, err := runtime.readExistingSchemaSealedFile(path, true)
	if err != nil || len(raw) > 16384 || startupContentSHA256(raw) != digest {
		return nil, errors.New("merchant writer contract file or immutable digest is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var contract productionMerchantStoreWriterContract
	if decoder.Decode(&contract) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("merchant writer contract JSON is invalid")
	}
	if err := validateMerchantStoreWriterContract(&contract); err != nil {
		return nil, err
	}
	canonical, err := json.MarshalIndent(contract, "", "  ")
	if err != nil || !bytes.Equal(append(canonical, '\n'), raw) {
		return nil, errors.New("merchant writer contract is not canonical JSON")
	}
	return &contract, nil
}

// Use the same all-property parser and ordered private-file seal as no-DDL
// startup, including the actual running generation's database identity. A
// secondary EnvFile cannot silently redirect the status child elsewhere.
func (runtime *productionRuntime) merchantStoreWriterEnvironment(ctx context.Context, manifest productionManifest) ([]string, error) {
	contract := manifest.MerchantStoreWriter
	if err := validateMerchantStoreWriterContract(contract); err != nil {
		return nil, err
	}
	if manifest.SchemaMode != productionSchemaModeVerifyExisting || manifest.ExistingSchemaContract == nil || manifest.DatabaseSchema != contract.Schema {
		return nil, errors.New("merchant writer lifecycle lacks its sealed verify-existing database policy")
	}
	if err := runtime.verifyExistingSchemaStartupMode(ctx, manifest); err != nil {
		return nil, err
	}
	loaded, err := runtime.loadedExistingSchemaUnit(ctx)
	if err != nil {
		return nil, err
	}
	values, digest, unit, err := runtime.existingSchemaSealedStartup(ctx, loaded)
	if err != nil || digest != contract.StartupSHA256 || unit != contract.SignedUnitSHA256 {
		return nil, errors.New("merchant writer effective environment or signed unit changed")
	}
	return runtime.merchantStoreWriterEnvironmentFromValues(ctx, contract, values)
}

func (runtime *productionRuntime) merchantStoreWriterEnvironmentFromValues(ctx context.Context, contract *productionMerchantStoreWriterContract, values map[string]string) ([]string, error) {
	child, err := runtime.existingSchemaMigrationValues(values, contract.Schema)
	if err != nil {
		return nil, err
	}
	sealedChild := make([]string, 0, len(values)+5)
	for _, entry := range child {
		key, _, _ := strings.Cut(entry, "=")
		_, sealed := values[key]
		if sealed || key == "GIN_MODE" || key == "PGOPTIONS" || key == "LMM_DB_MIGRATION_MODE" {
			sealedChild = append(sealedChild, entry)
		}
	}
	child = sealedChild
	for _, key := range []string{"PATH", "LANG", "LC_ALL"} {
		if _, present := values[key]; !present {
			value := "C"
			if key == "PATH" {
				value = "/usr/bin:/bin"
			}
			child = append(child, key+"="+value)
		}
	}
	// Verify the gate and role using the exact read-only status connection,
	// rather than the operator's shell or a privileged maintenance role.
	childValues := map[string]string{}
	for _, entry := range child {
		key, value, found := strings.Cut(entry, "=")
		if found {
			childValues[key] = value
		}
	}
	databaseURL, environment, err := productionSealedDatabaseCommand(childValues)
	if err != nil {
		return nil, err
	}
	query := merchantStoreWriterIdentityQuery(contract.Schema)
	output, err := runtime.runner.Run(ctx, productionCommand{Name: commandPSQL,
		Args: []string{"-X", "-q", "-v", "ON_ERROR_STOP=1", "--no-align", "--tuples-only", "--dbname", databaseURL, "--command", query},
		Env:  environment, Sensitive: true, Timeout: 15 * time.Second, OutputLimit: 4096})
	if err != nil {
		return nil, errors.New("merchant writer actual business database identity/floor is unavailable")
	}
	if err := verifyMerchantStoreWriterIdentity(output, contract); err != nil {
		return nil, err
	}
	return child, nil
}

func merchantStoreWriterIdentityQuery(schema string) string {
	// Callers validate the conservative ASCII schema grammar before reaching
	// this builder; qualification remains one bounded read-only transaction.
	return `BEGIN READ ONLY;
/* lmm-merchant-writer-identity */
SELECT pg_catalog.jsonb_build_object(
 'system_identifier',(SELECT system_identifier::pg_catalog.text FROM pg_catalog.pg_control_system()),
 'database',pg_catalog.current_database(),
 'database_oid',(SELECT oid::pg_catalog.int8 FROM pg_catalog.pg_database WHERE datname OPERATOR(pg_catalog.=) pg_catalog.current_database()),
 'schema',pg_catalog.current_schema(),
 'schema_oid',(SELECT oid::pg_catalog.int8 FROM pg_catalog.pg_namespace WHERE nspname OPERATOR(pg_catalog.=) pg_catalog.current_schema()),
 'role',CURRENT_USER,'session_role',SESSION_USER,
 'transaction_read_only',pg_catalog.current_setting('transaction_read_only'),
 'floor',(SELECT value FROM "` + schema + `".options WHERE key OPERATOR(pg_catalog.=) 'MerchantStoreMinimumWriterCapability'));
ROLLBACK;`
}

func verifyMerchantStoreWriterIdentity(output []byte, expected *productionMerchantStoreWriterContract) error {
	var actual struct {
		SystemIdentifier    string `json:"system_identifier"`
		Database            string `json:"database"`
		DatabaseOID         int64  `json:"database_oid"`
		Schema              string `json:"schema"`
		SchemaOID           int64  `json:"schema_oid"`
		Role                string `json:"role"`
		SessionRole         string `json:"session_role"`
		TransactionReadOnly string `json:"transaction_read_only"`
		Floor               string `json:"floor"`
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if len(output) == 0 || len(output) > 4096 || decoder.Decode(&actual) != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		actual.SystemIdentifier != expected.SystemIdentifier || actual.Database != expected.Database || actual.DatabaseOID != expected.DatabaseOID ||
		actual.Schema != expected.Schema || actual.SchemaOID != expected.SchemaOID || actual.Role != expected.Role || actual.SessionRole != expected.Role ||
		actual.TransactionReadOnly != "on" || actual.Floor != strconv.Itoa(expected.RequiredCapability) {
		return errors.New("merchant writer physical database, business role, schema or active floor differs from its sealed qualification")
	}
	return nil
}

// Qualification always executes the retained payload. Missing marker, unknown
// command (including real Go86/87 exit64), empty/invalid JSON and a mismatched
// signed marker all block before any writer/package/admission mutation.
func (runtime *productionRuntime) checkMerchantStoreWriterTarget(ctx context.Context, workspace productionWorkspace, manifest productionManifest, rollback bool) error {
	if err := validateMerchantStoreWriterContract(manifest.MerchantStoreWriter); err != nil {
		return err
	}
	transition := manifest.Go
	packagePath, packageSHA, revision, identity := transition.CandidatePath, transition.CandidateSHA256, transition.CandidateGitRevision, transition.CandidateIdentity
	packageName, role := transition.CandidatePackageName, "candidate"
	expected := manifest.MerchantStoreWriter.Candidate
	if rollback {
		packagePath, packageSHA, revision, identity = transition.RollbackPath, transition.RollbackSHA256, transition.RollbackGitRevision, transition.RollbackIdentity
		packageName, role, expected = transition.RollbackPackageName, "rollback", manifest.MerchantStoreWriter.Rollback
	}
	if err := runtime.validateStagedFile(workspace, packagePath, packageSHA, "merchant-store target package"); err != nil {
		return err
	}
	metadata, err := runtime.packageMetadata(ctx, packagePath, packageName)
	if err != nil || metadata.Identity != identity || metadata.GitRevision != revision || metadata.MerchantStoreWriterCapability != expected.Capability ||
		metadata.BinarySHA256 != expected.PayloadSHA256 || packageSHA != expected.PackageSHA256 || revision != expected.SourceRevision || metadata.ReleaseAssetSHA256 != expected.ReleaseAssetSHA256 {
		return errors.New("merchant-store target differs from its retained signed package/payload/source qualification")
	}
	if err := merchantStoreWriterTargetAllowed(manifest.MerchantStoreWriter.RequiredCapability, metadata.MerchantStoreWriterCapability); err != nil {
		return err
	}
	child, err := runtime.merchantStoreWriterEnvironment(ctx, manifest)
	if err != nil {
		return err
	}
	directory, err := prepareMigrationDir(workspace, "merchant-store-"+role)
	if err != nil {
		return err
	}
	provider := filepath.Join(directory, backendGoName)
	if digest, err := sha256File(provider); err != nil || digest != expected.PayloadSHA256 {
		body, err := runtime.runner.Run(ctx, productionCommand{Name: commandBsdtar, Args: []string{"-xOf", packagePath, "usr/bin/" + backendGoName}})
		if err != nil || startupContentSHA256(body) != expected.PayloadSHA256 {
			return errors.New("merchant-store target provider differs from the retained payload hash")
		}
		if err := writeAtomicRegularFile(provider, body, 0o700); err != nil {
			return err
		}
	}
	if err := runtime.requireOwnedSafePath(provider, false); err != nil {
		return errors.New("merchant-store target provider is unsafe")
	}
	entry := filepath.Join(directory, productionCandidateLinkName)
	if info, err := os.Lstat(entry); errors.Is(err, os.ErrNotExist) {
		if err := os.Symlink(backendGoName, entry); err != nil {
			return err
		}
	} else if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return errors.New("merchant-store target entrypoint is unsafe")
	}
	if target, err := os.Readlink(entry); err != nil || target != backendGoName {
		return errors.New("merchant-store target entrypoint changed")
	}
	output, err := runVerifiedBinary(ctx, runtime.runner, entry, []string{"merchant-store-writer-gate", "status"}, child, directory, 35*time.Second, true)
	if err := qualifyMerchantStoreWriterStatus(output, err, manifest.MerchantStoreWriter, expected); err != nil {
		return err
	}
	runtime.merchantStoreLastWriterCheck = expected
	return nil
}

func (runtime *productionRuntime) checkMerchantStoreWriterLifecycle(ctx context.Context, workspace productionWorkspace, manifest productionManifest, installed, rollback bool) error {
	if runtime.merchantStoreAuthority != nil {
		return runtime.merchantStoreAuthority.Qualify(ctx, workspace, manifest, installed, rollback)
	}
	if !manifest.Go.Changed && manifest.MerchantStoreWriter == nil {
		return nil // A Web-only transaction does not replace or restart a writer.
	}
	if err := runtime.checkMerchantStoreWriterTarget(ctx, workspace, manifest, rollback); err != nil {
		return err
	}
	if !installed {
		return nil
	}
	if err := runtime.verifyTransitionInstalled(ctx, manifest.Go, rollback, true); err != nil {
		return err
	}
	if err := runtime.verifyTransitionCLI(ctx, manifest.Go, rollback); err != nil {
		return err
	}
	expected := manifest.MerchantStoreWriter.Candidate
	if rollback {
		expected = manifest.MerchantStoreWriter.Rollback
	}
	if digest, err := sha256File(runtime.paths.InstalledBinary); err != nil || digest != expected.PayloadSHA256 {
		return errors.New("installed merchant writer differs from the sealed actual payload")
	}
	child, err := runtime.merchantStoreWriterEnvironment(ctx, manifest)
	if err != nil {
		return err
	}
	output, err := runVerifiedBinary(ctx, runtime.runner, runtime.paths.InstalledBinary,
		[]string{"merchant-store-writer-gate", "status"}, child, workspace.root, 35*time.Second, true)
	return qualifyMerchantStoreWriterStatus(output, err, manifest.MerchantStoreWriter, expected)
}

func (runtime *productionRuntime) merchantStorePreviewManifest(workspace productionWorkspace, path, digest string) (productionManifest, error) {
	plan, err := loadStagedProductionExistingSchemaPlan(workspace, path, digest)
	if err != nil {
		return productionManifest{}, err
	}
	if err := validateMerchantStoreWriterContract(plan.MerchantStoreWriter); err != nil {
		return productionManifest{}, err
	}
	host, err := runtime.hostname()
	if err != nil || host != plan.ExpectedHost {
		return productionManifest{}, errors.New("merchant writer preview is bound to another target host")
	}
	transition := productionPackageTransition{CandidatePackageName: plan.GoCandidate.Name, RollbackPackageName: plan.GoRollback.Name,
		CandidatePath: filepath.Join(workspace.stagingDir, filepath.Base(plan.GoCandidate.PackagePath)), RollbackPath: filepath.Join(workspace.stagingDir, filepath.Base(plan.GoRollback.PackagePath)),
		CandidateSHA256: plan.GoCandidate.PackageSHA256, RollbackSHA256: plan.GoRollback.PackageSHA256, CandidateIdentity: plan.GoCandidate.Identity,
		RollbackIdentity: plan.GoRollback.Identity, CandidateGitRevision: plan.GoCandidate.GitRevision, RollbackGitRevision: plan.GoRollback.GitRevision}
	transition.Changed = plan.GoChanged
	return productionManifest{DeploymentID: plan.DeploymentID, SchemaPlanSHA256: digest, Go: transition, DatabaseSchema: plan.MerchantStoreWriter.Schema, SchemaMode: plan.SchemaMode,
		ExistingSchemaContract: plan.ExistingSchemaContract, MerchantStoreWriter: plan.MerchantStoreWriter}, nil
}

func runProductionMerchantStoreWriterCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("production writer-check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	workspacePath := flags.String("workspace", "", "existing manifest-owned deployment workspace")
	rollback := flags.Bool("rollback-target", false, "check the exact retained recovery provider")
	planPath := flags.String("release-plan", "", "exact staged immutable release plan for preactivation preview")
	planSHA := flags.String("release-plan-sha256", "", "exact staged release plan SHA-256")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *workspacePath == "" {
		return ExitUsage
	}
	runtime := defaultProductionRuntime()
	if runtime.effectiveUID() != 0 {
		return ExitError
	}
	workspace, err := runtime.openWorkspace(*workspacePath)
	if err != nil {
		return ExitError
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var manifest productionManifest
	if *planPath != "" || *planSHA != "" {
		manifest, err = runtime.merchantStorePreviewManifest(workspace, *planPath, *planSHA)
	} else {
		manifest, err = runtime.readManifestForRollback(workspace)
	}
	if err == nil {
		err = runtime.checkMerchantStoreWriterTarget(ctx, workspace, manifest, *rollback)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "merchant-store writer target qualification blocked; serving state was not changed")
		return ExitError
	}
	if json.NewEncoder(stdout).Encode(runtime.merchantStoreLastWriterCheck) != nil {
		return ExitError
	}
	return ExitOK
}
