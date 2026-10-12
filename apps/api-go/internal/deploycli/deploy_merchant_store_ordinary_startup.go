package deploycli

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

// A portable command is never admitted by the legacy workspace parser. Its
// literal reference must load a canonical capsule and pass its actual hook,
// installed engine, ordered environment and signed-unit checks.
func merchantStoreLoadedCapsuleReference(loaded map[string]string) (string, string, error) {
	semantic, err := existingSchemaCommandSemantics(loaded["ExecStartPre"])
	if err != nil {
		return "", "", err
	}
	parts := strings.Split(semantic, "\x00")
	if len(parts) != 3 || parts[2] != "no" {
		return "", "", errors.New("ordinary portable startup command is unavailable")
	}
	words := strings.Fields(parts[1])
	if len(words) != 8 || words[0] != parts[0] || words[1] != "operator" || words[2] != "production" || words[3] != "writer-start" || words[4] != "--capsule" || words[6] != "--capsule-sha256" || !productionSHA256Pattern.MatchString(words[7]) {
		return "", "", errors.New("ordinary portable startup command has unsupported arguments")
	}
	return words[5], words[7], nil
}

func (runtime *productionRuntime) existingSchemaManifestStartup(ctx context.Context, manifest productionManifest, loaded map[string]string) (map[string]string, error) {
	writer := manifest.MerchantStoreWriter
	if writer != nil && writer.StartupPredecessorPath != "" {
		if err := validateMerchantStoreWriterContract(writer); err != nil {
			return nil, err
		}
		if manifest.DeploymentID != "" && filepath.Base(filepath.Dir(writer.StartupTargetPath)) != manifest.DeploymentID {
			return nil, errors.New("ordinary startup transition selects another deployment's capsule")
		}
		path, digest, err := merchantStoreLoadedCapsuleReference(loaded)
		if err != nil {
			return nil, err
		}
		if path == writer.StartupPredecessorPath && digest == writer.StartupPredecessorSHA256 {
			// This branch proves the old actual seal and separately recomputes
			// the new projection. It does not report a projected seal as loaded.
			return runtime.merchantStoreTransitionStartup(ctx, writer, manifest.ExistingSchemaContract, loaded)
		}
		if path != writer.StartupTargetPath {
			return nil, errors.New("ordinary startup selected another capsule path")
		}
		c, err := runtime.loadMerchantStoreCapsule(path, digest)
		if err != nil || c.Format != 1 || c.StartupPolicy != "per-start" || c.Writer == nil || *c.Writer != *writer || c.ExistingSchemaContract == nil || manifest.ExistingSchemaContract == nil || *c.ExistingSchemaContract != *manifest.ExistingSchemaContract ||
			manifest.DeploymentID != "" && c.DeploymentID != manifest.DeploymentID || manifest.SchemaPlanSHA256 != "" && c.ControllerPlanSHA256 != manifest.SchemaPlanSHA256 || path != filepath.Join(c.Root, "capsule.json") {
			return nil, errors.New("ordinary loaded capsule differs from its immutable plan")
		}
	}
	values, digest, unit, err := runtime.existingSchemaSealedStartup(ctx, loaded)
	if err != nil || manifest.ExistingSchemaContract == nil || digest != manifest.ExistingSchemaContract.StartupSHA256 || unit != manifest.ExistingSchemaContract.SignedUnitSHA256 {
		return nil, errors.New("effective production startup differs from its immutable seal")
	}
	return values, nil
}

func (runtime *productionRuntime) bindMerchantStoreManifestStartup(ctx context.Context, workspace productionWorkspace, manifest productionManifest) error {
	if manifest.MerchantStoreWriter == nil || manifest.MerchantStoreWriter.StartupPredecessorPath == "" {
		return nil
	}
	if manifest.MaintenanceHandoff != nil || manifest.SchemaMode != productionSchemaModeVerifyExisting || !manifest.Go.Changed {
		return errors.New("ordinary startup transition cannot enter another deployment policy")
	}
	plan, err := loadStagedProductionExistingSchemaPlan(workspace, filepath.Join(workspace.stagingDir, productionReleasePlanFilename), manifest.SchemaPlanSHA256)
	if err != nil {
		return err
	}
	path := manifest.MerchantStoreWriter.StartupTargetPath
	raw, err := runtime.readExistingSchemaSealedFile(path, true)
	if err != nil {
		return err
	}
	digest := startupContentSHA256(raw)
	c, err := runtime.loadMerchantStoreCapsule(path, digest)
	if err != nil || c.Format != 1 || c.StartupPolicy != "per-start" || c.DeploymentID != workspace.id || c.ControllerPlanSHA256 != manifest.SchemaPlanSHA256 ||
		c.Writer == nil || *c.Writer != *manifest.MerchantStoreWriter || c.ExistingSchemaContract == nil || manifest.ExistingSchemaContract == nil || *c.ExistingSchemaContract != *manifest.ExistingSchemaContract ||
		c.Candidate != merchantStoreCapsuleProjection(plan.GoCandidate, "candidate") || c.Rollback != merchantStoreCapsuleProjection(plan.GoRollback, "rollback") {
		return errors.New("ordinary startup capsule is not the exact staged official plan projection")
	}
	return runtime.bindMerchantStoreOrdinaryStartup(ctx, c, digest)
}
