package deploycli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// These fixtures exercise declarations and private immutable file behavior.
// They do not stand in for daemon generations, stopped services or PG owner
// observations; none of the referenced native proof files needs to exist here.
func schemaMaintenanceAbortPlanFixture(t *testing.T) (*productionRuntime, productionSchemaMaintenancePlan, string, []productionSchemaMaintenanceAbortPrepared, productionSchemaMaintenanceAbortBinding) {
	t.Helper()
	runtime, plan, _ := newSchemaMaintenancePlanFixture(t)
	planRaw, err := canonicalProductionSchemaMaintenancePlan(plan, runtime.paths)
	if err != nil {
		t.Fatal(err)
	}
	sha := startupContentSHA256(planRaw)
	prepared := make([]productionSchemaMaintenanceAbortPrepared, 0, len(plan.Origins))
	binding := productionSchemaMaintenanceAbortBinding{Format: schemaMaintenanceAbortBindingFormat, TransitionID: plan.TransitionID, PrePlanSHA256: sha, Mode: schemaMaintenanceAbortModeNeverClaimed, PreSchema: plan.PreSchema, Origins: make([]productionSchemaMaintenanceAbortOrigin, 0, len(plan.Origins))}
	for index, origin := range plan.Origins {
		proof := productionSchemaMaintenanceAbortPrepared{Format: schemaMaintenanceAbortPreparedFormat, TransitionID: plan.TransitionID, PrePlanSHA256: sha, Host: origin.Host, DaemonGeneration: schemaMaintenanceRPCBinding{PlanSHA256: sha, TransitionID: plan.TransitionID, DaemonPID: 200 + index, DaemonInvocationID: strings.Repeat("1", 32), DaemonExeSHA256: plan.Worker.Provider.DeployEngineSHA256, DaemonStartTicks: uint64(100 + index)}, OldCapsule: origin.OldCapsule, StoppedCapture: productionReleaseFilePlan{Path: filepath.Join(origin.Workspace, "state", "writer-stopped.json"), SHA256: startupContentSHA256([]byte(origin.Host + ":stopped"))}, GuardianBinding: productionReleaseFilePlan{Path: origin.Guardian.BindingPath, SHA256: startupContentSHA256([]byte(origin.Host + ":guardian"))}}
		raw, err := canonicalProductionSchemaMaintenanceAbortPrepared(proof, plan, sha, runtime.paths)
		if err != nil {
			t.Fatal(err)
		}
		prepared = append(prepared, proof)
		binding.Origins = append(binding.Origins, productionSchemaMaintenanceAbortOrigin{Alias: origin.Alias, Host: origin.Host, Prepared: productionReleaseFilePlan{Path: filepath.Join(origin.Workspace, "state", "abort-prepared.json"), SHA256: startupContentSHA256(raw)}, OldCapsule: proof.OldCapsule, StoppedCapture: proof.StoppedCapture, GuardianBinding: proof.GuardianBinding})
	}
	return runtime, plan, sha, prepared, binding
}
func cloneSchemaMaintenanceAbortBinding(t *testing.T, binding productionSchemaMaintenanceAbortBinding) productionSchemaMaintenanceAbortBinding {
	t.Helper()
	raw, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	var copied productionSchemaMaintenanceAbortBinding
	if json.Unmarshal(raw, &copied) != nil {
		t.Fatal("abort binding fixture copy failed")
	}
	return copied
}

func TestSchemaMaintenanceAbortPreparedRejectsForeignPlanDaemonAndEvidence(t *testing.T) {
	runtime, plan, sha, prepared, _ := schemaMaintenanceAbortPlanFixture(t)
	cases := map[string]func(*productionSchemaMaintenanceAbortPrepared){
		"format":           func(p *productionSchemaMaintenanceAbortPrepared) { p.Format = "another-format" },
		"transition":       func(p *productionSchemaMaintenanceAbortPrepared) { p.TransitionID += "-other" },
		"pre-plan":         func(p *productionSchemaMaintenanceAbortPrepared) { p.PrePlanSHA256 = strings.Repeat("f", 64) },
		"foreign-host":     func(p *productionSchemaMaintenanceAbortPrepared) { p.Host = "other-host" },
		"copied-peer-host": func(p *productionSchemaMaintenanceAbortPrepared) { p.Host = plan.Origins[1].Host },
		"daemon-plan": func(p *productionSchemaMaintenanceAbortPrepared) {
			p.DaemonGeneration.PlanSHA256 = strings.Repeat("f", 64)
		},
		"daemon-transition": func(p *productionSchemaMaintenanceAbortPrepared) { p.DaemonGeneration.TransitionID += "-other" },
		"old-engine": func(p *productionSchemaMaintenanceAbortPrepared) {
			p.DaemonGeneration.DaemonExeSHA256 = plan.Origins[0].Recovery.Candidate.DeployEngineSHA256
		},
		"missing-pid":         func(p *productionSchemaMaintenanceAbortPrepared) { p.DaemonGeneration.DaemonPID = 0 },
		"missing-invocation":  func(p *productionSchemaMaintenanceAbortPrepared) { p.DaemonGeneration.DaemonInvocationID = "" },
		"missing-start-ticks": func(p *productionSchemaMaintenanceAbortPrepared) { p.DaemonGeneration.DaemonStartTicks = 0 },
		"old-capsule-path": func(p *productionSchemaMaintenanceAbortPrepared) {
			p.OldCapsule.Path = plan.Origins[0].ArtifactCapsule.Path
		},
		"old-capsule-hash": func(p *productionSchemaMaintenanceAbortPrepared) { p.OldCapsule.SHA256 = strings.Repeat("f", 64) },
		"stopped-path": func(p *productionSchemaMaintenanceAbortPrepared) {
			p.StoppedCapture.Path = filepath.Join(plan.Origins[0].Workspace, "state", "stop-intent.json")
		},
		"stopped-hash": func(p *productionSchemaMaintenanceAbortPrepared) { p.StoppedCapture.SHA256 = "" },
		"guardian-path": func(p *productionSchemaMaintenanceAbortPrepared) {
			p.GuardianBinding.Path = plan.Origins[0].Guardian.Socket
		},
		"guardian-hash": func(p *productionSchemaMaintenanceAbortPrepared) { p.GuardianBinding.SHA256 = strings.Repeat("F", 64) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			bad := prepared[0]
			mutate(&bad)
			if _, err := canonicalProductionSchemaMaintenanceAbortPrepared(bad, plan, sha, runtime.paths); err == nil {
				t.Fatal("foreign preparation accepted")
			}
		})
	}
	if _, err := canonicalProductionSchemaMaintenanceAbortPrepared(prepared[0], plan, strings.Repeat("0", 64), runtime.paths); err == nil {
		t.Fatal("caller plan hash not checked")
	}
	changed := cloneSchemaMaintenancePlan(t, plan)
	changed.PreSchema.MetadataSHA256 = strings.Repeat("f", 64)
	if _, err := canonicalProductionSchemaMaintenanceAbortPrepared(prepared[0], changed, sha, runtime.paths); err == nil {
		t.Fatal("changed original catalog accepted with old plan hash")
	}
}
func TestSchemaMaintenanceAbortBindingRequiresExactNeverClaimedTwoOriginClosure(t *testing.T) {
	runtime, plan, sha, _, binding := schemaMaintenanceAbortPlanFixture(t)
	if _, err := canonicalProductionSchemaMaintenanceAbortBinding(binding, plan, sha, runtime.paths); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*productionSchemaMaintenanceAbortBinding){
		"format":          func(b *productionSchemaMaintenanceAbortBinding) { b.Format = "another-format" },
		"transition":      func(b *productionSchemaMaintenanceAbortBinding) { b.TransitionID += "-other" },
		"pre-plan":        func(b *productionSchemaMaintenanceAbortBinding) { b.PrePlanSHA256 = strings.Repeat("f", 64) },
		"claimed-mode":    func(b *productionSchemaMaintenanceAbortBinding) { b.Mode = "OWNER_RELEASED" },
		"missing-mode":    func(b *productionSchemaMaintenanceAbortBinding) { b.Mode = "" },
		"changed-catalog": func(b *productionSchemaMaintenanceAbortBinding) { b.PreSchema.MetadataSHA256 = strings.Repeat("f", 64) },
		"changed-cluster": func(b *productionSchemaMaintenanceAbortBinding) { b.PreSchema.SystemIdentifier = "1" },
		"host-startup-seal": func(b *productionSchemaMaintenanceAbortBinding) {
			b.PreSchema.StartupSHA256 = strings.Repeat("e", 64)
			b.PreSchema.SignedUnitSHA256 = strings.Repeat("f", 64)
		},
		"missing-origin":   func(b *productionSchemaMaintenanceAbortBinding) { b.Origins = b.Origins[:1] },
		"duplicate-origin": func(b *productionSchemaMaintenanceAbortBinding) { b.Origins[1] = b.Origins[0] },
		"reordered-origins": func(b *productionSchemaMaintenanceAbortBinding) {
			b.Origins[0], b.Origins[1] = b.Origins[1], b.Origins[0]
		},
		"extra-origin":  func(b *productionSchemaMaintenanceAbortBinding) { b.Origins = append(b.Origins, b.Origins[1]) },
		"foreign-alias": func(b *productionSchemaMaintenanceAbortBinding) { b.Origins[1].Alias = "Other" },
		"foreign-host":  func(b *productionSchemaMaintenanceAbortBinding) { b.Origins[1].Host = "other-host" },
		"prepared-path": func(b *productionSchemaMaintenanceAbortBinding) {
			b.Origins[1].Prepared.Path = filepath.Join(plan.Origins[1].Workspace, "state", "host-finished.json")
		},
		"prepared-hash": func(b *productionSchemaMaintenanceAbortBinding) { b.Origins[1].Prepared.SHA256 = "" },
		"old-capsule":   func(b *productionSchemaMaintenanceAbortBinding) { b.Origins[1].OldCapsule = plan.Origins[0].OldCapsule },
		"stop-path":     func(b *productionSchemaMaintenanceAbortBinding) { b.Origins[1].StoppedCapture.Path += ".alias" },
		"stop-hash":     func(b *productionSchemaMaintenanceAbortBinding) { b.Origins[1].StoppedCapture.SHA256 = "" },
		"guardian-path": func(b *productionSchemaMaintenanceAbortBinding) { b.Origins[1].GuardianBinding.Path += ".alias" },
		"guardian-hash": func(b *productionSchemaMaintenanceAbortBinding) { b.Origins[1].GuardianBinding.SHA256 = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			bad := cloneSchemaMaintenanceAbortBinding(t, binding)
			mutate(&bad)
			if _, err := canonicalProductionSchemaMaintenanceAbortBinding(bad, plan, sha, runtime.paths); err == nil {
				t.Fatal("incomplete or changed abort closure accepted")
			}
		})
	}
}
func TestSchemaMaintenanceAbortImmutablePrivateRoundTrip(t *testing.T) {
	runtime, plan, sha, prepared, binding := schemaMaintenanceAbortPlanFixture(t)
	workspace := schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID)
	preparedPath := filepath.Join(workspace, "state", "abort-prepared.json")
	bindingPath := filepath.Join(workspace, "state", "abort-binding.json")
	preparedSHA, err := runtime.createSchemaMaintenanceAbortPrepared(preparedPath, prepared[0], plan, sha)
	if err != nil {
		t.Fatal(err)
	}
	gotPrepared, err := runtime.loadSchemaMaintenanceAbortPrepared(preparedPath, preparedSHA, plan, sha)
	if err != nil || !reflect.DeepEqual(gotPrepared, prepared[0]) {
		t.Fatalf("prepared roundtrip: %v", err)
	}
	bindingSHA, err := runtime.createSchemaMaintenanceAbortBinding(bindingPath, binding, plan, sha)
	if err != nil {
		t.Fatal(err)
	}
	gotBinding, err := runtime.loadSchemaMaintenanceAbortBinding(bindingPath, bindingSHA, plan, sha)
	if err != nil || !reflect.DeepEqual(gotBinding, binding) {
		t.Fatalf("binding roundtrip: %v", err)
	}
	if _, err := runtime.createSchemaMaintenanceAbortPrepared(preparedPath, prepared[0], plan, sha); err == nil {
		t.Fatal("replaced prepared evidence")
	}
	if _, err := runtime.createSchemaMaintenanceAbortBinding(bindingPath, binding, plan, sha); err == nil {
		t.Fatal("replaced abort binding")
	}
	for _, path := range []string{preparedPath, bindingPath} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("abort file not private")
		}
	}
	if _, err := runtime.createSchemaMaintenanceAbortPrepared(preparedPath+".other", prepared[0], plan, sha); err == nil {
		t.Fatal("prepared escaped fixed path")
	}
	if _, err := runtime.createSchemaMaintenanceAbortBinding(bindingPath+".other", binding, plan, sha); err == nil {
		t.Fatal("binding escaped fixed path")
	}
	if _, err := runtime.loadSchemaMaintenanceAbortPrepared(preparedPath, strings.Repeat("0", 64), plan, sha); err == nil {
		t.Fatal("wrong prepared hash accepted")
	}
	if _, err := runtime.loadSchemaMaintenanceAbortBinding(bindingPath, strings.Repeat("0", 64), plan, sha); err == nil {
		t.Fatal("wrong binding hash accepted")
	}
}
func TestSchemaMaintenanceAbortLoadRejectsAmbiguousAndUnsafeWire(t *testing.T) {
	for _, kind := range []string{"prepared", "binding"} {
		t.Run(kind, func(t *testing.T) {
			for _, mutation := range []string{"unknown-field", "duplicate-field", "wrong-case", "compact-wire", "trailing-record", "missing-field", "public-mode", "hardlink", "symlink", "wrong-path", "path-alias"} {
				t.Run(mutation, func(t *testing.T) {
					runtime, plan, sha, prepared, binding := schemaMaintenanceAbortPlanFixture(t)
					workspace := schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID)
					var path string
					var raw []byte
					var err error
					if kind == "prepared" {
						path = filepath.Join(workspace, "state", "abort-prepared.json")
						raw, err = canonicalProductionSchemaMaintenanceAbortPrepared(prepared[0], plan, sha, runtime.paths)
					} else {
						path = filepath.Join(workspace, "state", "abort-binding.json")
						raw, err = canonicalProductionSchemaMaintenanceAbortBinding(binding, plan, sha, runtime.paths)
					}
					if err != nil {
						t.Fatal(err)
					}
					switch mutation {
					case "unknown-field":
						raw = bytes.Replace(raw, []byte("{\n"), []byte("{\n  \"approved\": true,\n"), 1)
					case "duplicate-field":
						raw = bytes.Replace(raw, []byte("{\n"), []byte("{\n  \"transition_id\": \""+plan.TransitionID+"\",\n"), 1)
					case "wrong-case":
						raw = bytes.Replace(raw, []byte("\"pre_plan_sha256\""), []byte("\"Pre_Plan_SHA256\""), 1)
					case "compact-wire":
						buffer := &bytes.Buffer{}
						if json.Compact(buffer, raw) != nil {
							t.Fatal("fixture compact failed")
						}
						raw = buffer.Bytes()
					case "trailing-record":
						raw = append(raw, []byte("{}\n")...)
					case "missing-field":
						raw = bytes.Replace(raw, []byte("  \"transition_id\": \""+plan.TransitionID+"\",\n"), nil, 1)
					}
					if os.WriteFile(path, raw, 0600) != nil {
						t.Fatal("fixture write failed")
					}
					switch mutation {
					case "public-mode":
						if os.Chmod(path, 0644) != nil {
							t.Fatal("fixture chmod failed")
						}
					case "hardlink":
						if os.Link(path, path+".hardlink") != nil {
							t.Fatal("fixture link failed")
						}
					case "symlink":
						original := path + ".original"
						if os.Rename(path, original) != nil || os.Symlink(original, path) != nil {
							t.Fatal("fixture symlink failed")
						}
					case "wrong-path":
						path += ".wrong"
						if os.WriteFile(path, raw, 0600) != nil {
							t.Fatal("fixture other-path write failed")
						}
					case "path-alias":
						path = filepath.Dir(path) + "/../state/" + filepath.Base(path)
					}
					var loadErr error
					if kind == "prepared" {
						_, loadErr = runtime.loadSchemaMaintenanceAbortPrepared(path, startupContentSHA256(raw), plan, sha)
					} else {
						_, loadErr = runtime.loadSchemaMaintenanceAbortBinding(path, startupContentSHA256(raw), plan, sha)
					}
					if loadErr == nil {
						t.Fatal("noncanonical or unsafe abort wire accepted")
					}
				})
			}
		})
	}
}
