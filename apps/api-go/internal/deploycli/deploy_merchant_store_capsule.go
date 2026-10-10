package deploycli

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
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A portable capsule is an immutable projection of an actually validated
// official plan, not a controller approval claim or an unsigned provider list.
// Target verification repeats the official Sigstore identity and full layout.
type productionMerchantStoreCapsule struct {
	Format                 int                                    `json:"format"`
	DeploymentID           string                                 `json:"deployment_id"`
	ControllerPlanSHA256   string                                 `json:"controller_plan_sha256"`
	Root                   string                                 `json:"root"`
	Host                   string                                 `json:"host"`
	Service                string                                 `json:"service"`
	Binary                 string                                 `json:"binary"`
	ConfigDir              string                                 `json:"config_dir"`
	SchemaMode             string                                 `json:"schema_mode"`
	ExistingSchemaContract *productionExistingSchemaContract      `json:"existing_schema_contract"`
	Writer                 *productionMerchantStoreWriterContract `json:"merchant_store_writer"`
	Candidate              productionReleasePackagePlan           `json:"candidate"`
	Rollback               productionReleasePackagePlan           `json:"rollback"`
	StartupPolicy          string                                 `json:"startup_policy"`
	SourceFiles            []productionReleaseFilePlan            `json:"source_files"`
	Baseline               *productionMerchantStartupBaseline     `json:"startup_baseline,omitempty"`
}

var merchantStoreCapsuleSourcePaths = []string{"scripts/native-shared-pg-deploy.py", "scripts/deploy-systemd.py", "scripts/maintenance-deploy-guardian.py"}

func merchantStoreCapsuleRoot(paths productionPaths, id string) string {
	return filepath.Join(filepath.Dir(paths.WorkRoot), "merchant-capsules", id)
}
func merchantStorePortableUnit(id string) string { return "lmm-merchant-portable-" + id + ".service" }
func merchantStoreStartUnit(id, invocation string) string {
	return "lmm-merchant-start-" + id + "-" + invocation + ".service"
}
func merchantStoreCapsuleAsset(role, kind string) string { return "assets/" + role + "." + kind }

func validateMerchantStoreCapsule(c productionMerchantStoreCapsule, paths productionPaths) error {
	if c.Format == 2 {
		return validateMerchantStartupBaselineCapsule(c, paths)
	}
	if c.Baseline != nil {
		return errors.New("ordinary capsule cannot contain startup baseline authority")
	}
	if c.Format != 1 || !productionIDPattern.MatchString(c.DeploymentID) || !productionSHA256Pattern.MatchString(c.ControllerPlanSHA256) ||
		c.Root != merchantStoreCapsuleRoot(paths, c.DeploymentID) || !merchantStoreFenceHostPattern.MatchString(c.Host) || c.Service != paths.Service || c.Service != "lmm-api.service" ||
		c.Binary != paths.InstalledBinary || c.ConfigDir != paths.ConfigDir || c.SchemaMode != productionSchemaModeVerifyExisting || (c.StartupPolicy != "held" && c.StartupPolicy != "per-start") {
		return errors.New("portable merchant capsule host/startup policy is incomplete")
	}
	if err := validateMerchantStoreWriterContract(c.Writer); err != nil {
		return err
	}
	if err := validateProductionExistingSchemaContract(c.ExistingSchemaContract); err != nil {
		return err
	}
	s, w := c.ExistingSchemaContract, c.Writer
	if s.SystemIdentifier != w.SystemIdentifier || s.Database != w.Database || s.DatabaseOID != w.DatabaseOID || s.Schema != w.Schema || s.SchemaOID != w.SchemaOID || s.StartupSHA256 != w.StartupSHA256 || s.SignedUnitSHA256 != w.SignedUnitSHA256 {
		return errors.New("portable capsule schema, ordered startup and writer identities differ")
	}
	if c.Candidate.ContractRevision != c.Rollback.ContractRevision {
		return errors.New("portable capsule candidate/rollback route contracts differ")
	}
	if len(c.SourceFiles) != len(merchantStoreCapsuleSourcePaths) {
		return errors.New("portable capsule lacks the exact verified wrapper source closure")
	}
	for i, path := range merchantStoreCapsuleSourcePaths {
		if c.SourceFiles[i].Path != path || !productionSHA256Pattern.MatchString(c.SourceFiles[i].SHA256) {
			return errors.New("portable capsule source closure is not canonical")
		}
	}
	for i, p := range []productionReleasePackagePlan{c.Candidate, c.Rollback} {
		role, target := "candidate", w.Candidate
		if i == 1 {
			role, target = "rollback", w.Rollback
		}
		version, err := packageReleaseVersion(p.Version)
		if err != nil || p.Name != productionAURPackageName || p.Identity != p.Name+" "+p.Version || p.ReleaseTag != "go-v"+version || p.Workflow != "release-go.yml" ||
			!productionRevisionPattern.MatchString(p.GitRevision) || !productionContractPattern.MatchString(p.ContractRevision) ||
			p.PackagePath != merchantStoreCapsuleAsset(role, "pkg.tar.zst") || p.ReleaseAsset != merchantStoreCapsuleAsset(role, "release.tar.gz") || p.SignatureBundle != merchantStoreCapsuleAsset(role, "sigstore.json") ||
			!productionSHA256Pattern.MatchString(p.SignatureBundleSHA256) || p.MerchantStoreWriterCapability != target.Capability || p.PackageSHA256 != target.PackageSHA256 ||
			p.PayloadSHA256 != target.PayloadSHA256 || p.GitRevision != target.SourceRevision || p.ReleaseAssetSHA256 != target.ReleaseAssetSHA256 {
			return errors.New("portable capsule artifact projection is not an exact official writer tuple")
		}
	}
	return nil
}
func canonicalMerchantStoreCapsule(c productionMerchantStoreCapsule) ([]byte, error) {
	if c.Format == 2 {
		return canonicalMerchantStartupBaselineCapsule(c)
	}
	body, err := json.MarshalIndent(c, "", "  ")
	return append(body, '\n'), err
}
func (runtime *productionRuntime) loadMerchantStoreCapsule(path, digest string) (productionMerchantStoreCapsule, error) {
	var c productionMerchantStoreCapsule
	if !productionSHA256Pattern.MatchString(digest) {
		return c, errors.New("portable capsule requires its immutable SHA-256")
	}
	raw, err := runtime.readExistingSchemaSealedFile(path, true)
	if err != nil || len(raw) > 32768 || startupContentSHA256(raw) != digest {
		return c, errors.New("portable capsule private file/digest is unsafe")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&c) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return c, errors.New("portable capsule JSON is ambiguous")
	}
	if err := validateMerchantStoreCapsule(c, runtime.paths); err != nil {
		return c, err
	}
	canonical, _ := canonicalMerchantStoreCapsule(c)
	if !bytes.Equal(canonical, raw) || path != filepath.Join(c.Root, "capsule.json") {
		return c, errors.New("portable capsule is not canonical or is outside its sealed root")
	}
	host, err := runtime.hostname()
	if err != nil || host != c.Host {
		return c, errors.New("portable capsule belongs to another actual host")
	}
	if c.Format == 2 {
		// The runtime projection has one provider and an empty rollback target.
		// It is never serialized as, or accepted by, an ordinary writer plan.
		c.Writer = c.Baseline.writerIdentity(c.Host)
		c.Candidate = c.Baseline.Provider
	}
	return c, nil
}
func merchantStoreCapsuleWorkspace(c productionMerchantStoreCapsule) productionWorkspace {
	return productionWorkspace{root: c.Root, id: c.DeploymentID, stateDir: filepath.Join(c.Root, "state"), stagingDir: filepath.Join(c.Root, "assets")}
}

// Extraction and holder files may never inherit a pre-existing symlink or a
// replaceable ancestor. Missing private directories are created one at a time
// only after all existing ancestors have been checked.
func (runtime *productionRuntime) merchantStorePrivateDirectory(path string, create bool) error {
	clean, err := cleanAbsoluteNonRoot(path)
	if err != nil || clean != path {
		return errors.New("portable directory is not canonical")
	}
	var missing []string
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			missing = append(missing, current)
		} else {
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("portable directory ancestor is not a real directory")
			}
			owner, _, ok := deploymentFileOwnership(info)
			trustedSticky := owner == 0 && info.Mode()&os.ModeSticky != 0
			if !ok || owner != 0 && owner != runtime.requiredOwnerUID || info.Mode().Perm()&0022 != 0 && !trustedSticky {
				return errors.New("portable directory ancestor is replaceable")
			}
			if current == path && (owner != runtime.requiredOwnerUID || info.Mode().Perm() != 0700) {
				return errors.New("portable private directory ownership or mode differs")
			}
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	if len(missing) != 0 && !create {
		return errors.New("portable private directory is missing")
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], 0700); err != nil {
			return errors.New("portable private directory could not be created")
		}
		if err := runtime.requireOwnedSafePath(missing[i], true); err != nil {
			return err
		}
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return errors.New("portable directory changed during inspection")
	}
	return nil
}

func (runtime *productionRuntime) merchantStoreCapsuleFile(path string, mode os.FileMode) error {
	if err := runtime.merchantStorePrivateDirectory(filepath.Dir(path), false); err != nil {
		return err
	}
	if err := runtime.requireOwnedSafePath(path, false); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != mode {
		return errors.New("portable file permissions differ")
	}
	return nil
}

func (runtime *productionRuntime) merchantStoreCapsuleMutableDirectories(c productionMerchantStoreCapsule) error {
	if err := runtime.merchantStorePrivateDirectory(c.Root, false); err != nil {
		return err
	}
	// Reject every existing extraction/state destination before creating any of
	// them, so a later hostile destination cannot leave earlier side effects.
	paths := []string{filepath.Join(c.Root, "tmp"), filepath.Join(c.Root, "tmp", "migrations"), filepath.Join(c.Root, "tmp", "migrations", "merchant-store-candidate"), filepath.Join(c.Root, "tmp", "migrations", "merchant-store-rollback"), filepath.Join(c.Root, "state")}
	for _, path := range paths {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			if err := runtime.merchantStorePrivateDirectory(path, false); err != nil {
				return err
			}
		}
	}
	for _, path := range paths {
		if err := runtime.merchantStorePrivateDirectory(path, true); err != nil {
			return err
		}
	}
	return nil
}
func merchantStoreCapsuleProjection(p productionReleasePackagePlan, role string) productionReleasePackagePlan {
	p.PackagePath = merchantStoreCapsuleAsset(role, "pkg.tar.zst")
	p.ReleaseAsset = merchantStoreCapsuleAsset(role, "release.tar.gz")
	p.SignatureBundle = merchantStoreCapsuleAsset(role, "sigstore.json")
	return p
}

// No caller can inject an approval boolean. The existing controller verifier
// authenticates all package/release evidence before this function derives it.
func (release *productionReleaseRuntime) deriveMerchantStoreCapsule(ctx context.Context, plan productionReleasePlan, planSHA, host, policy string, schema *productionExistingSchemaContract, writer *productionMerchantStoreWriterContract) (productionMerchantStoreCapsule, error) {
	var c productionMerchantStoreCapsule
	if err := validateProductionReleasePlan(plan); err != nil {
		return c, err
	}
	if plan.SchemaMode != productionSchemaModeVerifyExisting || !plan.GoChanged || plan.ExistingSchemaContract == nil || plan.MerchantStoreWriter == nil || schema == nil || writer == nil {
		return c, errors.New("portable capsule requires an actual immutable no-DDL writer plan")
	}
	shared, local := *plan.ExistingSchemaContract, *schema
	shared.StartupSHA256, local.StartupSHA256 = "", ""
	if shared != local || plan.MerchantStoreWriter.RequiredCapability != writer.RequiredCapability || plan.MerchantStoreWriter.Candidate != writer.Candidate || plan.MerchantStoreWriter.Rollback != writer.Rollback {
		return c, errors.New("portable host contract differs from the controller's qualified shared database/artifact contract")
	}
	canonical, err := canonicalProductionReleasePlan(plan)
	if err != nil || startupContentSHA256(canonical) != planSHA {
		return c, errors.New("portable capsule source plan digest differs")
	}
	if err := validateProductionReleasePlanArtifacts(ctx, release, plan); err != nil {
		return c, err
	}
	paths := defaultProductionPaths()
	c = productionMerchantStoreCapsule{Format: 1, DeploymentID: plan.DeploymentID, ControllerPlanSHA256: planSHA, Root: merchantStoreCapsuleRoot(paths, plan.DeploymentID), Host: host,
		Service: paths.Service, Binary: paths.InstalledBinary, ConfigDir: paths.ConfigDir, SchemaMode: productionSchemaModeVerifyExisting, ExistingSchemaContract: schema, Writer: writer,
		Candidate: merchantStoreCapsuleProjection(plan.GoCandidate, "candidate"), Rollback: merchantStoreCapsuleProjection(plan.GoRollback, "rollback"), StartupPolicy: policy}
	// The official archive pins the commit ID. Verify its actual object graph
	// before deriving scripts, and never honor replacement refs/textconv or
	// ambient Git configuration as a different source for that same ID.
	if _, err := release.runner.Run(ctx, productionCommand{Name: commandGit, Args: []string{"--no-replace-objects", "-C", plan.Repository, "-c", "core.commitGraph=false", "fsck", "--full", "--strict", "--no-reflogs", "--no-dangling", plan.GoCandidate.GitRevision}, Env: merchantStoreSourceGitEnvironment(), Timeout: 2 * time.Minute, OutputLimit: 1 << 20}); err != nil {
		return productionMerchantStoreCapsule{}, errors.New("official candidate source object graph failed integrity verification")
	}
	for _, path := range merchantStoreCapsuleSourcePaths {
		body, err := release.runner.Run(ctx, productionCommand{Name: commandGit, Args: []string{"--no-replace-objects", "-C", plan.Repository, "show", "--no-textconv", plan.GoCandidate.GitRevision + ":" + path}, Env: merchantStoreSourceGitEnvironment(), OutputLimit: 1 << 20})
		if err != nil || len(body) == 0 {
			return productionMerchantStoreCapsule{}, errors.New("official candidate source revision lacks the portable wrapper closure")
		}
		c.SourceFiles = append(c.SourceFiles, productionReleaseFilePlan{Path: path, SHA256: startupContentSHA256(body)})
	}
	return c, validateMerchantStoreCapsule(c, paths)
}

// Portable target does not use pacman or infer installed identity. It repeats
// the exact official certificate identity and all archive/package contents.
func (runtime *productionRuntime) verifyMerchantStoreCapsuleArtifact(ctx context.Context, c productionMerchantStoreCapsule, rollback bool) (string, error) {
	if c.Format == 2 && rollback {
		return "", errors.New("startup baseline has no rollback provider")
	}
	p, target, role := c.Candidate, c.Writer.Candidate, "candidate"
	if rollback {
		p, target, role = c.Rollback, c.Writer.Rollback, "rollback"
	}
	for _, f := range []struct{ path, sha string }{{p.PackagePath, p.PackageSHA256}, {p.ReleaseAsset, p.ReleaseAssetSHA256}, {p.SignatureBundle, p.SignatureBundleSHA256}} {
		path := filepath.Join(c.Root, f.path)
		if runtime.merchantStoreCapsuleFile(path, 0600) != nil || sha256MustEqual(path, f.sha) != nil {
			return "", errors.New("portable capsule artifact file changed or is unsafe")
		}
	}
	asset, bundle, pkg := filepath.Join(c.Root, p.ReleaseAsset), filepath.Join(c.Root, p.SignatureBundle), filepath.Join(c.Root, p.PackagePath)
	identity := productionReleaseIdentity(p.ReleaseAssetSHA256, p.Name, p.Workflow, p.ReleaseTag)
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandCosign, Args: []string{"verify-blob", "--bundle", bundle, "--certificate-identity", identity, "--certificate-oidc-issuer", productionReleaseOIDCIssuer, asset}, Timeout: 2 * time.Minute}); err != nil {
		return "", errors.New("portable capsule official Sigstore identity failed")
	}
	release := productionReleaseRuntime{runner: runtime.runner}
	rev, route, payload, err := release.readSignedReleasePayload(ctx, p.Name, p.Version, asset)
	if err != nil || rev != p.GitRevision || route != p.ContractRevision || startupContentSHA256(payload) != p.PayloadSHA256 {
		return "", errors.New("portable capsule signed archive metadata/payload differs")
	}
	engine, err := readOptionalDeployEngine(ctx, runtime.runner, asset, true)
	if err != nil {
		return "", err
	}
	engineSHA := ""
	if len(engine) != 0 {
		engineSHA = startupContentSHA256(engine)
	}
	if engineSHA != p.DeployEngineSHA256 {
		return "", errors.New("portable capsule deployment tool differs from signed archive")
	}
	if err := runtime.merchantStoreCapsuleMutableDirectories(c); err != nil {
		return "", err
	}
	if err := release.verifySignedPackageLayout(ctx, c.Root, p.Name, p.Version, pkg, asset, p.ReleaseAssetSHA256, false); err != nil {
		return "", err
	}
	cap, err := runtime.merchantStorePackageCapability(ctx, pkg, p.Name)
	if err != nil || cap != target.Capability || merchantStoreWriterTargetAllowed(c.Writer.RequiredCapability, cap) != nil {
		return "", errors.New("portable capsule capability is unsupported or under floor")
	}
	unit, err := runtime.runner.Run(ctx, productionCommand{Name: commandBsdtar, Args: []string{"-xOf", pkg, "usr/lib/systemd/system/lmm-api.service"}, OutputLimit: 1 << 20})
	if err != nil || startupContentSHA256(unit) != c.Writer.SignedUnitSHA256 {
		return "", errors.New("portable capsule startup fragment is not in both signed providers")
	}
	directory, err := prepareMigrationDir(merchantStoreCapsuleWorkspace(c), "merchant-store-"+role)
	if err != nil {
		return "", err
	}
	if p.DeployEngineSHA256 != "" {
		target := filepath.Join(directory, deployEngineName)
		if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) && runtime.merchantStoreCapsuleFile(target, 0700) != nil {
			return "", errors.New("portable existing deployment tool file is unsafe")
		}
		if sha256MustEqual(target, p.DeployEngineSHA256) != nil {
			if err := writeAtomicRegularFile(target, engine, 0700); err != nil {
				return "", err
			}
		}
		if runtime.merchantStoreCapsuleFile(target, 0700) != nil || sha256MustEqual(target, p.DeployEngineSHA256) != nil {
			return "", errors.New("portable deployment tool failed extraction verification")
		}
	}
	provider := filepath.Join(directory, backendGoName)
	if _, err := os.Lstat(provider); !errors.Is(err, os.ErrNotExist) && runtime.merchantStoreCapsuleFile(provider, 0700) != nil {
		return "", errors.New("portable existing provider file is unsafe")
	}
	if sha256MustEqual(provider, p.PayloadSHA256) != nil {
		if err := writeAtomicRegularFile(provider, payload, 0700); err != nil {
			return "", err
		}
	}
	entry := filepath.Join(directory, productionCandidateLinkName)
	if _, err := os.Lstat(entry); errors.Is(err, os.ErrNotExist) {
		if err := os.Symlink(backendGoName, entry); err != nil {
			return "", err
		}
	}
	entryInfo, entryErr := os.Lstat(entry)
	if entryErr != nil {
		return "", errors.New("portable capsule provider entry is missing")
	}
	entryOwner, _, entryOK := deploymentFileOwnership(entryInfo)
	if target, err := os.Readlink(entry); !entryOK || entryOwner != runtime.requiredOwnerUID || err != nil || target != backendGoName || runtime.merchantStoreCapsuleFile(provider, 0700) != nil {
		return "", errors.New("portable capsule provider entry changed")
	}
	// Rehash every source after external verifiers/extraction, before executing.
	for _, f := range []struct{ path, sha string }{{pkg, p.PackageSHA256}, {asset, p.ReleaseAssetSHA256}, {bundle, p.SignatureBundleSHA256}, {provider, p.PayloadSHA256}} {
		if sha256MustEqual(f.path, f.sha) != nil {
			return "", errors.New("portable capsule evidence changed during verification")
		}
	}
	return entry, nil
}

const merchantStoreCapsuleHashPlaceholder = "@CAPSULE_SHA256@"

var merchantStorePortableStartExPattern = regexp.MustCompile(`^\{ path=([^ ;\r\n]+) ; argv\[\]=([^;\r\n]+) ; flags=privileged ;(?:[^\r\n]* )?\}$`)

func merchantStorePortableStartCommand(value, extended, binary, path, digest string) (string, error) {
	semantic, err := existingSchemaCommandSemantics(value)
	expected := binary + "\x00" + binary + " operator production writer-start --capsule " + path + " --capsule-sha256 " + digest + "\x00no"
	if err != nil || !productionSHA256Pattern.MatchString(digest) || semantic != expected {
		return "", errors.New("portable startup hook differs from the exact capsule/path/digest")
	}
	// ExecStartPre omits the '+' privilege flag. Its Ex property must prove
	// exactly that flag for the same sole native command, not a second hook,
	// ignore-failure flag or another systemd privilege mode.
	match := merchantStorePortableStartExPattern.FindStringSubmatch(extended)
	if match == nil || strings.Count(extended, "{ path=") != 1 || strings.Count(extended, "argv[]=") != 1 || strings.Count(extended, "flags=") != 1 || match[1] != binary || match[2] != strings.Split(semantic, "\x00")[1] {
		return "", errors.New("portable startup hook lacks its exact privileged systemd command")
	}
	// Only this validated self-reference is normalized, avoiding a hash cycle.
	// The literal digest must separately equal the actual canonical file hash.
	return binary + "\x00" + binary + " operator production writer-start --capsule " + path + " --capsule-sha256 " + merchantStoreCapsuleHashPlaceholder + "\x00no\x00privileged", nil
}

func merchantStorePortableHookBytes(binary, path, digest string) []byte {
	return []byte(fmt.Sprintf("[Service]\nTimeoutStartSec=%ds\nExecStartPre=\nExecStartPre=+%s operator production writer-start --capsule %s --capsule-sha256 %s\n", int(merchantStoreStartupLimit/time.Second), binary, path, digest))
}

// The actual loaded '+' command and its root-owned persistent drop-in are
// inseparable startup evidence. Normalize only the separately validated literal
// digest so the immutable capsule can bind its own hook without a hash cycle.
func (runtime *productionRuntime) merchantStorePortableStartupCommands(loaded map[string]string, binary, path, digest string) (map[string]string, error) {
	operator := merchantStoreLoadedOperator(loaded, binary)
	pre, err := merchantStorePortableStartCommand(loaded["ExecStartPre"], loaded["ExecStartPreEx"], operator, path, digest)
	if err != nil {
		return nil, err
	}
	dropIn := filepath.Join(runtime.paths.DropInDir, "90-merchant-startup-baseline.conf")
	hook, err := runtime.readExistingSchemaSealedFile(dropIn, false)
	if err != nil || !bytes.Equal(hook, merchantStorePortableHookBytes(operator, path, digest)) {
		return nil, errors.New("portable persistent startup hook bytes differ from the exact privileged capsule hook")
	}
	count := 0
	for _, item := range strings.Fields(loaded["DropInPaths"]) {
		if item == dropIn {
			count++
		}
	}
	if count != 1 {
		return nil, errors.New("portable startup hook is not actually loaded from its persistent drop-in")
	}
	copy := map[string]string{}
	for key, value := range loaded {
		copy[key] = value
	}
	copy["ExecStartPre"], copy["ExecStartPreEx"] = "", ""
	commands, err := verifyExistingSchemaSealedCommands(copy, binary)
	if err != nil {
		return nil, err
	}
	commands["ExecStartPre"] = pre
	commands["ExecStartPreEx"] = "privileged"
	commands["PersistentPortableHookSHA256"] = startupContentSHA256(merchantStorePortableHookBytes(operator, path, merchantStoreCapsuleHashPlaceholder))
	return commands, nil
}

func merchantStoreSourceGitEnvironment() []string {
	return []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_NO_REPLACE_OBJECTS=1"}
}
func (runtime *productionRuntime) merchantStoreCapsuleStartup(ctx context.Context, c productionMerchantStoreCapsule, digest string, loaded map[string]string) (map[string]string, error) {
	var commands map[string]string
	var err error
	if c.StartupPolicy == "per-start" {
		if merchantStoreLoadedOperator(loaded, c.Binary) != deploymentInstalledCommand(c.Binary, c.Candidate) {
			return nil, errors.New("portable startup hook does not use the capsule-bound deployment tool")
		}
		if c.Candidate.DeployEngineSHA256 != "" && (runtime.requireOwnedSafePath(deployEngineInstalledPath, false) != nil || sha256MustEqual(deployEngineInstalledPath, c.Candidate.DeployEngineSHA256) != nil) {
			return nil, errors.New("installed startup deployment tool differs from capsule")
		}
		commands, err = runtime.merchantStorePortableStartupCommands(loaded, c.Binary, filepath.Join(c.Root, "capsule.json"), digest)
	} else {
		commands, err = verifyExistingSchemaSealedCommands(loaded, c.Binary)
	}
	if err != nil {
		return nil, err
	}
	values, err := parseExistingSchemaLoadedEnvironment(loaded["Environment"])
	if err != nil || verifyExistingSchemaStartupEnvironment(values, true) != nil {
		return nil, errors.New("portable startup mode is not verify-only")
	}
	values, startup, unit, err := runtime.existingSchemaSealedStartupCommands(ctx, loaded, values, commands)
	if err != nil || startup != c.Writer.StartupSHA256 || unit != c.Writer.SignedUnitSHA256 {
		return nil, errors.New("portable ordered startup bytes differ from immutable capsule")
	}
	return values, nil
}

func (runtime *productionRuntime) captureMerchantStorePortableStartup(ctx context.Context, path, digest string) (map[string]string, error) {
	id := filepath.Base(filepath.Dir(path))
	if !productionIDPattern.MatchString(id) || path != filepath.Join(merchantStoreCapsuleRoot(runtime.paths, id), "capsule.json") {
		return nil, errors.New("startup capture capsule path is not canonical")
	}
	loaded, err := runtime.loadedExistingSchemaUnit(ctx)
	if err != nil {
		return nil, err
	}
	commands, err := runtime.merchantStorePortableStartupCommands(loaded, runtime.paths.InstalledBinary, path, digest)
	if err != nil {
		return nil, err
	}
	values, err := parseExistingSchemaLoadedEnvironment(loaded["Environment"])
	if err != nil || verifyExistingSchemaStartupEnvironment(values, true) != nil {
		return nil, errors.New("portable startup capture is not verify-only")
	}
	_, startup, unit, err := runtime.existingSchemaSealedStartupCommands(ctx, loaded, values, commands)
	if err != nil {
		return nil, err
	}
	host, err := runtime.hostname()
	if err != nil {
		return nil, err
	}
	return map[string]string{"host": host, "service": runtime.paths.Service, "capsule_path": path, "startup_sha256": startup, "signed_unit_sha256": unit}, nil
}

func (runtime *productionRuntime) merchantStoreCapsuleEnvironment(ctx context.Context, c productionMerchantStoreCapsule, digest, invocation string) ([]string, error) {
	loaded, err := runtime.loadedExistingSchemaUnit(ctx)
	if err != nil {
		return nil, err
	}
	values, err := runtime.merchantStoreCapsuleStartup(ctx, c, digest, loaded)
	if err != nil {
		return nil, err
	}
	raw, err := runtime.readExistingSchemaSealedFile(filepath.Join(c.ConfigDir, "lmm-api-go.env"), true)
	configured, parseErr := parseProductionEnvironment(raw)
	mainDSN, dsnErr := productionDatabaseURL(configured)
	effectiveDSN, effectiveErr := productionDatabaseURL(values)
	if err != nil || parseErr != nil || dsnErr != nil || effectiveErr != nil || mainDSN != effectiveDSN || verifyExistingSchemaStartupEnvironment(configured, false) != nil {
		return nil, errors.New("portable future database differs from canonical startup config")
	}
	if invocation != "" && (!existingSchemaInvocationPattern.MatchString(invocation) || loaded["InvocationID"] != invocation || loaded["ActiveState"] != "activating" && loaded["ActiveState"] != "active") {
		return nil, errors.New("portable startup invocation changed")
	}
	pid, parseErr := strconv.Atoi(loaded["MainPID"])
	if parseErr != nil || pid < 0 || pid == 1 {
		return nil, errors.New("portable writer PID evidence is invalid")
	}
	if pid == 0 {
		if invocation == "" && loaded["ActiveState"] != "inactive" {
			return nil, errors.New("portable inactive writer generation is unavailable")
		}
	} else {
		if !existingSchemaInvocationPattern.MatchString(loaded["InvocationID"]) || loaded["ActiveState"] != "active" && loaded["ActiveState"] != "activating" {
			return nil, errors.New("portable live generation is unavailable")
		}
		process, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
		if err != nil || len(process) > 1<<20 {
			return nil, errors.New("portable actual process environment is unavailable")
		}
		processValues := map[string]string{}
		for _, row := range bytes.Split(process, []byte{0}) {
			if len(row) == 0 {
				continue
			}
			key, value, ok := strings.Cut(string(row), "=")
			if !ok || !productionEnvironmentKeyPattern.MatchString(key) {
				return nil, errors.New("portable process environment is malformed")
			}
			if _, dup := processValues[key]; dup {
				return nil, errors.New("portable process environment is duplicated")
			}
			processValues[key] = value
		}
		processDSN, err := productionDatabaseURL(processValues)
		if err != nil || processDSN != mainDSN || verifyExistingSchemaStartupEnvironment(processValues, true) != nil || verifyExistingSchemaLogDatabase(processValues, processDSN) != nil {
			return nil, errors.New("portable actual process database/mode differs")
		}
		if err := runtime.verifyExistingSchemaEffectiveSearchPath(ctx, processValues, c.ExistingSchemaContract); err != nil {
			return nil, err
		}
	}
	child, err := runtime.merchantStoreWriterEnvironmentFromValues(ctx, c.Writer, values)
	if err != nil {
		return nil, err
	}
	current, err := runtime.loadedExistingSchemaUnit(ctx)
	if err != nil || current["MainPID"] != loaded["MainPID"] || current["InvocationID"] != loaded["InvocationID"] || current["ActiveState"] != loaded["ActiveState"] {
		return nil, errors.New("portable process generation changed during qualification")
	}
	if _, err := runtime.merchantStoreCapsuleStartup(ctx, c, digest, current); err != nil {
		return nil, err
	}
	return child, nil
}
func (runtime *productionRuntime) qualifyMerchantStoreCapsule(ctx context.Context, c productionMerchantStoreCapsule, digest, invocation string, verify bool) error {
	for _, source := range c.SourceFiles {
		path := filepath.Join(c.Root, "source", source.Path)
		body, err := runtime.readExistingSchemaSealedFile(path, true)
		if err != nil || startupContentSHA256(body) != source.SHA256 {
			return errors.New("portable wrapper source differs from the actual official candidate revision")
		}
	}
	child, err := runtime.merchantStoreCapsuleEnvironment(ctx, c, digest, invocation)
	if err != nil {
		return fmt.Errorf("portable capsule environment qualification: %w", err)
	}
	targets := []productionMerchantStoreWriterTarget{c.Writer.Candidate, c.Writer.Rollback}
	if c.Format == 2 {
		targets = targets[:1]
	}
	for i, target := range targets {
		role := "candidate"
		if i == 1 {
			role = "rollback"
		}
		provider, err := runtime.verifyMerchantStoreCapsuleArtifact(ctx, c, i == 1)
		if err != nil {
			return fmt.Errorf("portable capsule %s artifact qualification: %w", role, err)
		}
		output, callErr := runVerifiedBinary(ctx, runtime.runner, provider, []string{"merchant-store-writer-gate", "status"}, child, filepath.Dir(provider), 35*time.Second, true)
		if err := qualifyMerchantStoreWriterStatus(output, callErr, c.Writer, target); err != nil {
			return fmt.Errorf("portable capsule %s writer status qualification: %w", role, err)
		}
		if verify {
			if err := runtime.verifyMerchantStoreWriterProvider(ctx, merchantStoreCapsuleWorkspace(c), provider, child, role); err != nil {
				return err
			}
		}
	}
	if err := runtime.verifyMerchantStoreCapsuleSchema(ctx, c, child); err != nil {
		return fmt.Errorf("portable capsule schema qualification: %w", err)
	}
	return nil
}

func (runtime *productionRuntime) verifyMerchantStoreCapsuleSchema(ctx context.Context, c productionMerchantStoreCapsule, child []string) error {
	values := map[string]string{}
	for _, row := range child {
		key, value, ok := strings.Cut(row, "=")
		if !ok {
			return errors.New("portable schema child is malformed")
		}
		if _, dup := values[key]; dup {
			return errors.New("portable schema child is duplicated")
		}
		values[key] = value
	}
	dsn, environment, err := productionSealedDatabaseCommand(values)
	if err != nil {
		return err
	}
	output, err := runtime.runner.Run(ctx, productionCommand{Name: commandPSQL, Args: []string{"-X", "-q", "-v", "ON_ERROR_STOP=1", "--no-align", "--tuples-only", "--command", existingSchemaMetadataQuery(c.Writer.Schema), "--dbname", dsn}, Env: environment, Sensitive: true, Timeout: 30 * time.Second, OutputLimit: 8 << 20})
	if err != nil {
		return errors.New("portable actual business-role read-only schema verification failed")
	}
	actual, err := decodeExistingSchemaSnapshot(output)
	if err != nil {
		return err
	}
	expected := *c.ExistingSchemaContract
	expected.StartupSHA256, expected.SignedUnitSHA256 = "", ""
	if actual != expected {
		return errors.New("portable physical identity/schema metadata drifted")
	}
	return nil
}

func runProductionMerchantStoreCapsule(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && strings.HasPrefix(args[0], "baseline-") {
		return runProductionMerchantStartupBaseline(args, stdout, stderr)
	}
	if len(args) == 0 {
		return ExitUsage
	}
	flags := flag.NewFlagSet("production writer-capsule "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("capsule", "", "root-private portable capsule")
	digest := flags.String("capsule-sha256", "", "canonical capsule SHA-256")
	planPath := flags.String("release-plan", "", "actually verified official controller plan")
	planSHA := flags.String("release-plan-sha256", "", "exact controller plan digest")
	schemaPath := flags.String("schema-contract", "", "host-specific same-schema contract")
	schemaSHA := flags.String("schema-contract-sha256", "", "host-specific schema contract digest")
	writerPath := flags.String("writer-contract", "", "host-specific writer/ordered-startup qualification")
	writerSHA := flags.String("writer-contract-sha256", "", "host-specific writer contract digest")
	host := flags.String("host", "", "actual target hostname")
	policy := flags.String("startup-policy", "per-start", "per-start or held")
	output := flags.String("output", "", "new root-private output file; never overwrite")
	startupPath := flags.String("startup-capsule-path", "", "read-only capture: exact future typed hook capsule path")
	startupDigest := flags.String("startup-capsule-sha256", "", "read-only capture: literal hook digest, including a preparation placeholder")
	abortState := flags.String("state-sha256", "", "pre-apply abort: original STAGED bytes")
	abortOwner := flags.String("owner-sha256", "", "pre-apply abort: original live owner bytes")
	abortPID := flags.Int("old-pid", 0, "pre-apply abort: originally captured rollback PID")
	abortInvocation := flags.String("old-invocation", "", "pre-apply abort: originally captured rollback InvocationID")
	abortBoot := flags.String("old-boot-id", "", "pre-apply abort: originally captured boot ID")
	abortConfirm := flags.String("confirm", "", "pre-apply abort: explicit target api.lmm.best")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 {
		return ExitUsage
	}
	if args[0] != "pre-apply-abort" && (*abortState != "" || *abortOwner != "" || *abortPID != 0 || *abortInvocation != "" || *abortBoot != "" || *abortConfirm != "") {
		return ExitUsage
	}
	runtime := defaultProductionRuntime()
	if runtime.effectiveUID() != 0 && args[0] != "seal" {
		return ExitError
	}
	if args[0] == "seal" {
		runtime.requiredOwnerUID = uint32(runtime.effectiveUID())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	var err error
	if args[0] == "startup-seal" {
		if *startupPath == "" || *startupDigest == "" || *path != "" || *digest != "" || *planPath != "" || *schemaPath != "" || *writerPath != "" || *host != "" || *output != "" {
			return ExitUsage
		}
		var seal map[string]string
		seal, err = runtime.captureMerchantStorePortableStartup(ctx, *startupPath, *startupDigest)
		if err == nil {
			body, _ := json.Marshal(seal)
			_, _ = stdout.Write(append(body, '\n'))
		}
	} else if *startupPath != "" || *startupDigest != "" {
		return ExitUsage
	} else if args[0] == "seal" {
		if *path != "" || *digest != "" || *output == "" || *host == "" {
			return ExitUsage
		}
		var plan productionReleasePlan
		plan, err = loadProductionReleasePlan(*planPath, *planSHA)
		var schema *productionExistingSchemaContract
		var writer *productionMerchantStoreWriterContract
		if err == nil {
			schema, err = loadProductionExistingSchemaContract(*schemaPath, *schemaSHA)
		}
		if err == nil {
			writer, err = loadMerchantStoreWriterContract(*writerPath, *writerSHA)
		}
		var c productionMerchantStoreCapsule
		if err == nil {
			release := &productionReleaseRuntime{runner: osProductionCommandRunner{}, now: time.Now}
			c, err = release.deriveMerchantStoreCapsule(ctx, plan, *planSHA, *host, *policy, schema, writer)
		}
		if err == nil {
			if filepath.Clean(*output) != *output || filepath.Dir(*output) != plan.ControllerWorkspace || filepath.Base(*output) != "merchant-writer-capsule-"+c.Host+".json" || runtime.requireOwnedSafePath(filepath.Dir(*output), true) != nil {
				_, _ = fmt.Fprintln(stderr, "capsule output must be a new private file in the actual validated controller workspace")
				return ExitError
			}
			body, _ := canonicalMerchantStoreCapsule(c)
			var file *os.File
			file, err = os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err == nil {
				_, err = file.Write(body)
				if err == nil {
					err = file.Sync()
				}
				closeErr := file.Close()
				if err == nil {
					err = closeErr
				}
			}
			if err == nil {
				_, _ = fmt.Fprintf(stdout, "capsule_sha256=%s\n", startupContentSHA256(body))
			}
		}
	} else if args[0] == "check" {
		if *path == "" || *digest == "" || *planPath != "" || *planSHA != "" || *schemaPath != "" || *schemaSHA != "" || *writerPath != "" || *writerSHA != "" || *host != "" || *output != "" {
			return ExitUsage
		}
		var c productionMerchantStoreCapsule
		c, err = runtime.loadMerchantStoreCapsule(*path, *digest)
		if err == nil {
			err = runtime.qualifyMerchantStoreCapsule(ctx, c, *digest, "", true)
		}
	} else if args[0] == "pre-apply-abort" {
		if *path == "" || *digest == "" || *planPath != "" || *planSHA != "" || *schemaPath != "" || *schemaSHA != "" || *writerPath != "" || *writerSHA != "" || *host != "" || *output != "" || *abortConfirm != "api.lmm.best" {
			return ExitUsage
		}
		var c productionMerchantStoreCapsule
		c, err = runtime.loadMerchantStoreCapsule(*path, *digest)
		if err == nil {
			var receipt string
			receipt, err = runtime.abortMerchantStorePortablePreApply(ctx, c, *digest, merchantStorePortableAbortOptions{StateSHA256: *abortState, OwnerSHA256: *abortOwner, OldPID: *abortPID, OldInvocation: *abortInvocation, OldBootID: *abortBoot})
			if err == nil {
				_, _ = fmt.Fprintf(stdout, "portable_pre_apply_abort=ROLLED_BACK receipt_sha256=%s owner_release=pending\n", receipt)
			}
		}
	} else if args[0] == "hold" || args[0] == "ensure" || args[0] == "check-held" || args[0] == "release" {
		if *path == "" || *digest == "" || *planPath != "" || *planSHA != "" || *schemaPath != "" || *schemaSHA != "" || *writerPath != "" || *writerSHA != "" || *host != "" || *output != "" {
			return ExitUsage
		}
		var c productionMerchantStoreCapsule
		c, err = runtime.loadMerchantStoreCapsule(*path, *digest)
		if err == nil {
			if args[0] == "hold" {
				err = runtime.holdMerchantStorePortableFence(context.Background(), c, *digest, "")
			} else if args[0] == "ensure" {
				err = runtime.qualifyMerchantStoreCapsule(ctx, c, *digest, "", false)
				if err == nil {
					err = runtime.ensureMerchantStorePortableFence(ctx, c, *digest, "")
				}
			} else {
				err = runtime.qualifyMerchantStoreCapsule(ctx, c, *digest, "", false)
				if err == nil {
					err = runtime.requestMerchantStorePortableFence(ctx, c, *digest, "", args[0] == "release")
				}
			}
		}
	} else {
		return ExitUsage
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s production writer-capsule: %v\n", DeployProgramName, err)
		return ExitError
	}
	_, _ = fmt.Fprintln(stdout, "merchant_store_capsule=qualified")
	return ExitOK
}
