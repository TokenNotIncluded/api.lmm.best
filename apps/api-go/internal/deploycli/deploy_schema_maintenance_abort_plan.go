package deploycli

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
)

const schemaMaintenanceAbortPreparedFormat = "lmm-schema-maintenance-abort-prepared-v1"
const schemaMaintenanceAbortBindingFormat = "lmm-schema-maintenance-abort-binding-v1"
const schemaMaintenanceAbortModeNeverClaimed = "NEVER_CLAIMED"

// These declarations bind immutable evidence references only. They do not prove
// that the native owner was never claimed, that any service can start, or that
// admission can reopen. The original daemons must independently reobserve all
// live phase gates before using an abort binding.
type productionSchemaMaintenanceAbortPrepared struct {
	Format           string                      `json:"format"`
	TransitionID     string                      `json:"transition_id"`
	PrePlanSHA256    string                      `json:"pre_plan_sha256"`
	Host             string                      `json:"host"`
	DaemonGeneration schemaMaintenanceRPCBinding `json:"daemon_generation"`
	OldCapsule       productionReleaseFilePlan   `json:"old_capsule"`
	StoppedCapture   productionReleaseFilePlan   `json:"stopped_capture"`
	GuardianBinding  productionReleaseFilePlan   `json:"guardian_binding"`
}

type productionSchemaMaintenanceAbortBinding struct {
	Format        string                                   `json:"format"`
	TransitionID  string                                   `json:"transition_id"`
	PrePlanSHA256 string                                   `json:"pre_plan_sha256"`
	Mode          string                                   `json:"mode"`
	PreSchema     productionExistingSchemaContract         `json:"pre_schema"`
	Origins       []productionSchemaMaintenanceAbortOrigin `json:"origins"`
}

type productionSchemaMaintenanceAbortOrigin struct {
	Alias           string                    `json:"alias"`
	Host            string                    `json:"host"`
	Prepared        productionReleaseFilePlan `json:"prepared"`
	OldCapsule      productionReleaseFilePlan `json:"old_capsule"`
	StoppedCapture  productionReleaseFilePlan `json:"stopped_capture"`
	GuardianBinding productionReleaseFilePlan `json:"guardian_binding"`
}

func validateSchemaMaintenanceAbortPrePlan(plan productionSchemaMaintenancePlan, preSHA string, paths productionPaths) error {
	raw, err := canonicalProductionSchemaMaintenancePlan(plan, paths)
	if err != nil || !productionSHA256Pattern.MatchString(preSHA) || startupContentSHA256(raw) != preSHA {
		return errors.New("schema maintenance abort does not bind the exact original canonical plan")
	}
	return nil
}

func validateSchemaMaintenanceAbortOriginReferences(old, stopped, guardian productionReleaseFilePlan, origin productionSchemaMaintenanceOriginPlan) error {
	if old != origin.OldCapsule || validateSchemaMaintenanceReference(old) != nil || validateSchemaMaintenanceReference(stopped) != nil || validateSchemaMaintenanceReference(guardian) != nil || stopped.Path != filepath.Join(origin.Workspace, "state", "writer-stopped.json") || guardian.Path != origin.Guardian.BindingPath {
		return errors.New("schema maintenance abort changed an old capsule, sealed writer stop or guardian reference")
	}
	return nil
}

func validateProductionSchemaMaintenanceAbortPrepared(prepared productionSchemaMaintenanceAbortPrepared, plan productionSchemaMaintenancePlan, preSHA string, paths productionPaths) error {
	if validateSchemaMaintenanceAbortPrePlan(plan, preSHA, paths) != nil || prepared.Format != schemaMaintenanceAbortPreparedFormat || prepared.TransitionID != plan.TransitionID || prepared.PrePlanSHA256 != preSHA || validateSchemaMaintenanceRPCBinding(prepared.DaemonGeneration) != nil || prepared.DaemonGeneration.TransitionID != plan.TransitionID || prepared.DaemonGeneration.PlanSHA256 != preSHA || prepared.DaemonGeneration.DaemonExeSHA256 != plan.Worker.Provider.DeployEngineSHA256 {
		return errors.New("schema maintenance abort preparation lacks its exact original plan or native daemon generation")
	}
	for _, origin := range plan.Origins {
		if origin.Host == prepared.Host {
			return validateSchemaMaintenanceAbortOriginReferences(prepared.OldCapsule, prepared.StoppedCapture, prepared.GuardianBinding, origin)
		}
	}
	return errors.New("schema maintenance abort preparation is not one fixed registered origin")
}

func canonicalProductionSchemaMaintenanceAbortPrepared(prepared productionSchemaMaintenanceAbortPrepared, plan productionSchemaMaintenancePlan, preSHA string, paths productionPaths) ([]byte, error) {
	if err := validateProductionSchemaMaintenanceAbortPrepared(prepared, plan, preSHA, paths); err != nil {
		return nil, err
	}
	raw, err := json.MarshalIndent(prepared, "", "  ")
	return append(raw, '\n'), err
}

func validateProductionSchemaMaintenanceAbortBinding(binding productionSchemaMaintenanceAbortBinding, plan productionSchemaMaintenancePlan, preSHA string, paths productionPaths) error {
	if validateSchemaMaintenanceAbortPrePlan(plan, preSHA, paths) != nil || binding.Format != schemaMaintenanceAbortBindingFormat || binding.TransitionID != plan.TransitionID || binding.PrePlanSHA256 != preSHA || binding.Mode != schemaMaintenanceAbortModeNeverClaimed || binding.PreSchema != plan.PreSchema || len(binding.Origins) != len(plan.Origins) {
		return errors.New("schema maintenance abort binding lacks its exact never-claimed original plan and physical catalog")
	}
	for i, origin := range binding.Origins {
		expected := plan.Origins[i]
		if origin.Alias != expected.Alias || origin.Host != expected.Host || validateSchemaMaintenanceReference(origin.Prepared) != nil || origin.Prepared.Path != filepath.Join(expected.Workspace, "state", "abort-prepared.json") || validateSchemaMaintenanceAbortOriginReferences(origin.OldCapsule, origin.StoppedCapture, origin.GuardianBinding, expected) != nil {
			return errors.New("schema maintenance abort binding omitted, reordered or changed a fixed origin and its original evidence")
		}
	}
	return nil
}

func canonicalProductionSchemaMaintenanceAbortBinding(binding productionSchemaMaintenanceAbortBinding, plan productionSchemaMaintenancePlan, preSHA string, paths productionPaths) ([]byte, error) {
	if err := validateProductionSchemaMaintenanceAbortBinding(binding, plan, preSHA, paths); err != nil {
		return nil, err
	}
	raw, err := json.MarshalIndent(binding, "", "  ")
	return append(raw, '\n'), err
}

func (runtime *productionRuntime) createSchemaMaintenanceAbortPrepared(path string, prepared productionSchemaMaintenanceAbortPrepared, plan productionSchemaMaintenancePlan, preSHA string) (string, error) {
	raw, err := canonicalProductionSchemaMaintenanceAbortPrepared(prepared, plan, preSHA, runtime.paths)
	if err != nil || path != filepath.Join(schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID), "state", "abort-prepared.json") {
		return "", errors.New("schema maintenance abort preparation requires its fixed immutable private path")
	}
	return runtime.writeSchemaMaintenanceDataNew(path, raw)
}
func (runtime *productionRuntime) loadSchemaMaintenanceAbortPrepared(path, sha string, plan productionSchemaMaintenancePlan, preSHA string) (productionSchemaMaintenanceAbortPrepared, error) {
	var prepared productionSchemaMaintenanceAbortPrepared
	if validateSchemaMaintenanceAbortPrePlan(plan, preSHA, runtime.paths) != nil || path != filepath.Join(schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID), "state", "abort-prepared.json") {
		return prepared, errors.New("schema maintenance abort preparation path or original plan differs")
	}
	raw, err := runtime.readSchemaMaintenanceData(path, sha, &prepared)
	if err != nil {
		return prepared, err
	}
	canonical, err := canonicalProductionSchemaMaintenanceAbortPrepared(prepared, plan, preSHA, runtime.paths)
	if err != nil || !bytes.Equal(raw, canonical) {
		return prepared, errors.New("schema maintenance abort preparation bytes are not exact and canonical")
	}
	return prepared, nil
}

func (runtime *productionRuntime) createSchemaMaintenanceAbortBinding(path string, binding productionSchemaMaintenanceAbortBinding, plan productionSchemaMaintenancePlan, preSHA string) (string, error) {
	raw, err := canonicalProductionSchemaMaintenanceAbortBinding(binding, plan, preSHA, runtime.paths)
	if err != nil || path != filepath.Join(schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID), "state", "abort-binding.json") {
		return "", errors.New("schema maintenance abort binding requires its fixed immutable private path")
	}
	return runtime.writeSchemaMaintenanceDataNew(path, raw)
}
func (runtime *productionRuntime) loadSchemaMaintenanceAbortBinding(path, sha string, plan productionSchemaMaintenancePlan, preSHA string) (productionSchemaMaintenanceAbortBinding, error) {
	var binding productionSchemaMaintenanceAbortBinding
	if validateSchemaMaintenanceAbortPrePlan(plan, preSHA, runtime.paths) != nil || path != filepath.Join(schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID), "state", "abort-binding.json") {
		return binding, errors.New("schema maintenance abort binding path or original plan differs")
	}
	raw, err := runtime.readSchemaMaintenanceData(path, sha, &binding)
	if err != nil {
		return binding, err
	}
	canonical, err := canonicalProductionSchemaMaintenanceAbortBinding(binding, plan, preSHA, runtime.paths)
	if err != nil || !bytes.Equal(raw, canonical) {
		return binding, errors.New("schema maintenance abort binding bytes are not exact and canonical")
	}
	return binding, nil
}
