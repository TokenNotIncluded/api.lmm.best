//go:build !windows

package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type schemaMaintenancePreExecRecoveryFixture struct {
	original *schemaMaintenancePreExecFixture
	plan     productionSchemaMaintenancePreExecRecoveryPlan
	ref      productionReleaseFilePlan
	capsule  productionMerchantStoreCapsule
	calls    int
}

// Actual private files exercise the pure local guard only. The 118 release
// tuple and assets are synthetic; the sealed engine is a copy of this test
// executable. This is not Sigstore, live holder, PostgreSQL or release proof.
func newSchemaMaintenancePreExecRecoveryFixture(t *testing.T, primary bool) *schemaMaintenancePreExecRecoveryFixture {
	t.Helper()
	original := newSchemaMaintenancePreExecFixture(t, primary)
	f := &schemaMaintenancePreExecRecoveryFixture{original: original}
	original.runtime.runner = existingSchemaTestRunner{run: func(productionCommand) ([]byte, error) {
		f.calls++
		return nil, errors.New("pure recovery guard must not run commands")
	}}
	index := 0
	if !primary {
		index = 1
	}
	origin := original.plan.Origins[index]
	old, err := original.runtime.loadMerchantStoreCapsule(origin.ArtifactCapsule.Path, origin.ArtifactCapsule.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	f.capsule = cloneMerchantCapsule(old)
	f.capsule.DeploymentID = schemaMaintenancePreExecRecoveryArtifactID(original.plan)
	f.capsule.Root = merchantStoreCapsuleRoot(original.runtime.paths, f.capsule.DeploymentID)
	for _, path := range []string{filepath.Join(f.capsule.Root, "assets"), filepath.Join(f.capsule.Root, "tmp", "migrations", "merchant-store-candidate")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	f.capsule.Candidate = schemaMaintenancePlanProvider("0.2.118", "candidate")
	f.capsule.Candidate.ContractRevision = original.plan.Worker.Provider.ContractRevision
	executable, err := os.ReadFile("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	f.capsule.Candidate.DeployEngineSHA256 = startupContentSHA256(executable)
	for _, asset := range []struct {
		path string
		sha  *string
	}{{f.capsule.Candidate.PackagePath, &f.capsule.Candidate.PackageSHA256}, {f.capsule.Candidate.ReleaseAsset, &f.capsule.Candidate.ReleaseAssetSHA256}, {f.capsule.Candidate.SignatureBundle, &f.capsule.Candidate.SignatureBundleSHA256}} {
		body := []byte("synthetic recovery asset: " + asset.path + "\n")
		*asset.sha = startupContentSHA256(body)
		schemaMaintenanceRecoveryWriteFile(t, filepath.Join(f.capsule.Root, asset.path), body)
	}
	f.capsule.Writer.Candidate = schemaMaintenanceRecoveryWriterTarget(f.capsule.Candidate)
	engine := filepath.Join(f.capsule.Root, "tmp", "migrations", "merchant-store-candidate", deployEngineName)
	schemaMaintenanceRecoveryWriteFile(t, engine, executable)
	if err := os.Chmod(engine, 0700); err != nil {
		t.Fatal(err)
	}
	f.plan = productionSchemaMaintenancePreExecRecoveryPlan{Format: schemaMaintenancePreExecRecoveryFormat, TransitionID: original.plan.TransitionID, PrePlanSHA256: original.sha}
	for _, origin := range original.plan.Origins {
		evidence := productionSchemaMaintenancePreExecRejectedEvidence{Plan: original.evidence.Plan, OriginalDaemon: original.evidence.OriginalDaemon}
		if origin.Host == original.plan.PrimaryHost {
			refs := []*productionReleaseFilePlan{}
			for _, name := range []string{"schema-owner.json", "schema-worker-intent.json", "schema-worker-result.json"} {
				refs = append(refs, &productionReleaseFilePlan{Path: filepath.Join(origin.Workspace, "state", name), SHA256: strings.Repeat("a", 64)})
			}
			evidence.OriginalOwner, evidence.WorkerIntent, evidence.WorkerResult = refs[0], refs[1], refs[2]
		}
		if origin.Host == old.Host {
			evidence = original.evidence
		}
		f.plan.Origins = append(f.plan.Origins, productionSchemaMaintenancePreExecRecoveryOrigin{Host: origin.Host, RecoveryArtifactCapsule: productionReleaseFilePlan{Path: filepath.Join(f.capsule.Root, "capsule.json")}, Evidence: evidence})
	}
	f.ref.Path = filepath.Join(original.plan.Origins[0].Workspace, "state", "preexec-recovery-plan.json")
	f.sealCapsule(t)
	return f
}

func (f *schemaMaintenancePreExecRecoveryFixture) sealAuthority(t *testing.T) {
	t.Helper()
	raw, err := canonicalSchemaMaintenancePreExecRecovery(f.plan, f.original.plan, f.original.sha, f.original.runtime.paths)
	if err != nil {
		t.Fatal(err)
	}
	schemaMaintenanceRecoveryWriteFile(t, f.ref.Path, raw)
	f.ref.SHA256 = startupContentSHA256(raw)
}

func (f *schemaMaintenancePreExecRecoveryFixture) sealCapsule(t *testing.T) {
	t.Helper()
	if err := validateMerchantStoreCapsule(f.capsule, f.original.runtime.paths); err != nil {
		t.Fatal(err)
	}
	raw, err := canonicalMerchantStoreCapsule(f.capsule)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.capsule.Root, "capsule.json")
	schemaMaintenanceRecoveryWriteFile(t, path, raw)
	for i := range f.plan.Origins {
		f.plan.Origins[i].RecoveryArtifactCapsule.SHA256 = startupContentSHA256(raw)
	}
	f.sealAuthority(t)
}

func (f *schemaMaintenancePreExecRecoveryFixture) check() (productionMerchantStoreCapsule, error) {
	return f.original.runtime.checkSchemaMaintenancePreExecRecovery(context.Background(), f.original.plan, f.original.sha, f.ref)
}

func TestSchemaMaintenancePreExecRecoveryCanonicalAuthority(t *testing.T) {
	f := newSchemaMaintenancePreExecRecoveryFixture(t, true)
	cases := map[string]func(*productionSchemaMaintenancePreExecRecoveryPlan){
		"foreign transition": func(p *productionSchemaMaintenancePreExecRecoveryPlan) { p.TransitionID += "-foreign" },
		"foreign plan hash":  func(p *productionSchemaMaintenancePreExecRecoveryPlan) { p.PrePlanSHA256 = strings.Repeat("f", 64) },
		"missing origin":     func(p *productionSchemaMaintenancePreExecRecoveryPlan) { p.Origins = p.Origins[:1] },
		"reordered origins": func(p *productionSchemaMaintenancePreExecRecoveryPlan) {
			p.Origins[0], p.Origins[1] = p.Origins[1], p.Origins[0]
		},
		"foreign host": func(p *productionSchemaMaintenancePreExecRecoveryPlan) { p.Origins[0].Host = "foreign-host" },
		"artifact path": func(p *productionSchemaMaintenancePreExecRecoveryPlan) {
			p.Origins[0].RecoveryArtifactCapsule.Path += ".other"
		},
		"artifact hash": func(p *productionSchemaMaintenancePreExecRecoveryPlan) {
			p.Origins[0].RecoveryArtifactCapsule.SHA256 = ""
		},
		"daemon path": func(p *productionSchemaMaintenancePreExecRecoveryPlan) {
			p.Origins[0].Evidence.OriginalDaemon.Path += ".other"
		},
		"missing primary owner":  func(p *productionSchemaMaintenancePreExecRecoveryPlan) { p.Origins[0].Evidence.OriginalOwner = nil },
		"missing primary intent": func(p *productionSchemaMaintenancePreExecRecoveryPlan) { p.Origins[0].Evidence.WorkerIntent = nil },
		"missing primary result": func(p *productionSchemaMaintenancePreExecRecoveryPlan) { p.Origins[0].Evidence.WorkerResult = nil },
		"primary result path": func(p *productionSchemaMaintenancePreExecRecoveryPlan) {
			p.Origins[0].Evidence.WorkerResult.Path += ".other"
		},
		"secondary owner": func(p *productionSchemaMaintenancePreExecRecoveryPlan) {
			p.Origins[1].Evidence.OriginalOwner = p.Origins[0].Evidence.OriginalOwner
		},
		"secondary worker": func(p *productionSchemaMaintenancePreExecRecoveryPlan) {
			p.Origins[1].Evidence.WorkerResult = p.Origins[0].Evidence.WorkerResult
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(f.plan)
			if err != nil {
				t.Fatal(err)
			}
			var p productionSchemaMaintenancePreExecRecoveryPlan
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			mutate(&p)
			if _, err := canonicalSchemaMaintenancePreExecRecovery(p, f.original.plan, f.original.sha, f.original.runtime.paths); err == nil {
				t.Fatal("accepted redirected or incomplete recovery authority")
			}
		})
	}
	if f.calls != 0 {
		t.Fatal("canonical validation ran a command")
	}
}

func TestSchemaMaintenancePreExecRecoveryPureGuardOriginalFiles(t *testing.T) {
	for _, primary := range []bool{true, false} {
		name := "secondary"
		if primary {
			name = "primary"
		}
		t.Run(name, func(t *testing.T) {
			f := newSchemaMaintenancePreExecRecoveryFixture(t, primary)
			for i := 0; i < 2; i++ {
				got, err := f.check()
				if err != nil || !reflect.DeepEqual(got, f.capsule) {
					t.Fatalf("pure revalidation: %v", err)
				}
			}
			if f.calls != 0 {
				t.Fatal("pure revalidation ran a command")
			}
		})
	}
}

func TestSchemaMaintenancePreExecRecoveryGuardRejectsChangedFiles(t *testing.T) {
	cases := map[string]func(*testing.T, *schemaMaintenancePreExecRecoveryFixture){
		"historical worker result": func(t *testing.T, f *schemaMaintenancePreExecRecoveryFixture) {
			schemaMaintenanceRecoveryWriteFile(t, f.original.evidence.WorkerResult.Path, []byte("changed original result\n"))
		},
		"authority bytes": func(t *testing.T, f *schemaMaintenancePreExecRecoveryFixture) {
			raw, err := os.ReadFile(f.ref.Path)
			if err != nil {
				t.Fatal(err)
			}
			schemaMaintenanceRecoveryWriteFile(t, f.ref.Path, append(raw, '\n'))
		},
		"authority hash": func(t *testing.T, f *schemaMaintenancePreExecRecoveryFixture) { f.ref.SHA256 = strings.Repeat("f", 64) },
		"authority path": func(t *testing.T, f *schemaMaintenancePreExecRecoveryFixture) { f.ref.Path += ".other" },
		"capsule bytes": func(t *testing.T, f *schemaMaintenancePreExecRecoveryFixture) {
			schemaMaintenanceRecoveryWriteFile(t, f.plan.Origins[0].RecoveryArtifactCapsule.Path, []byte("changed capsule\n"))
		},
		"package bytes": func(t *testing.T, f *schemaMaintenancePreExecRecoveryFixture) {
			schemaMaintenanceRecoveryWriteFile(t, filepath.Join(f.capsule.Root, f.capsule.Candidate.PackagePath), []byte("changed package\n"))
		},
		"release bytes": func(t *testing.T, f *schemaMaintenancePreExecRecoveryFixture) {
			schemaMaintenanceRecoveryWriteFile(t, filepath.Join(f.capsule.Root, f.capsule.Candidate.ReleaseAsset), []byte("changed release\n"))
		},
		"signature bytes": func(t *testing.T, f *schemaMaintenancePreExecRecoveryFixture) {
			schemaMaintenanceRecoveryWriteFile(t, filepath.Join(f.capsule.Root, f.capsule.Candidate.SignatureBundle), []byte("changed signature\n"))
		},
		"different version": func(t *testing.T, f *schemaMaintenancePreExecRecoveryFixture) {
			f.capsule.Candidate.Version, f.capsule.Candidate.ReleaseTag = "0.2.119-1", "go-v0.2.119"
			f.capsule.Candidate.Identity = f.capsule.Candidate.Name + " " + f.capsule.Candidate.Version
			f.sealCapsule(t)
		},
		"different self engine": func(t *testing.T, f *schemaMaintenancePreExecRecoveryFixture) {
			body := []byte("sealed but not the executing recovery engine\n")
			f.capsule.Candidate.DeployEngineSHA256 = startupContentSHA256(body)
			path := filepath.Join(f.capsule.Root, "tmp", "migrations", "merchant-store-candidate", deployEngineName)
			if err := os.WriteFile(path, body, 0700); err != nil {
				t.Fatal(err)
			}
			f.sealCapsule(t)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newSchemaMaintenancePreExecRecoveryFixture(t, true)
			if _, err := f.check(); err != nil {
				t.Fatalf("baseline failed: %v", err)
			}
			before, err := os.ReadFile(f.original.evidence.Plan.Path)
			if err != nil {
				t.Fatal(err)
			}
			mutate(t, f)
			if _, err := f.check(); err == nil {
				t.Fatal("accepted changed recovery or historical evidence")
			}
			after, err := os.ReadFile(f.original.evidence.Plan.Path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("guard rewrote original plan")
			}
			if f.calls != 0 {
				t.Fatal("pure rejection ran a command")
			}
		})
	}
}
