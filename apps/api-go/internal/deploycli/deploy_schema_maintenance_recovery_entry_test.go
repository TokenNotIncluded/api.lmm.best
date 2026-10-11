//go:build !windows

package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func schemaRecoveryStartupFixture(t *testing.T, host string) (*sealedStartupFixture, productionMerchantStoreCapsule, string, map[string]string, string) {
	t.Helper()
	f := realShapeStartupFixture(t, host)
	f.runtime.paths.WorkRoot = filepath.Join(t.TempDir(), "work")
	f.runtime.paths.DropInDir = filepath.Join(filepath.Dir(f.files[0]), "drop-ins")
	if err := os.Mkdir(f.runtime.paths.DropInDir, 0755); err != nil {
		t.Fatal(err)
	}
	old := testMerchantCapsule(t, f.runtime.paths)
	f.runtime.hostname = func() (string, error) { return old.Host, nil }
	oldSHA := strings.Repeat("a", 64)
	oldPath := filepath.Join(old.Root, "capsule.json")
	pre := "{ path=" + old.Binary + " ; argv[]=" + old.Binary + " operator production writer-start --capsule " + oldPath + " --capsule-sha256 " + oldSHA + " ; ignore_errors=no ; }"
	drop := filepath.Join(f.runtime.paths.DropInDir, "90-merchant-startup-baseline.conf")
	if err := os.WriteFile(drop, merchantStorePortableHookBytes(old.Binary, oldPath, oldSHA), 0644); err != nil {
		t.Fatal(err)
	}
	f.unitOutput += "ExecStartPre=" + pre + "\nExecStartPreEx=" + testPortableStartEx(pre) + "\nDropInPaths=" + drop + "\n"
	seal, err := f.runtime.captureMerchantStorePortableStartup(context.Background(), oldPath, oldSHA)
	if err != nil {
		t.Fatal(err)
	}
	old.Writer.StartupSHA256, old.Writer.SignedUnitSHA256 = seal["startup_sha256"], seal["signed_unit_sha256"]
	old.ExistingSchemaContract.StartupSHA256, old.ExistingSchemaContract.SignedUnitSHA256 = seal["startup_sha256"], seal["signed_unit_sha256"]
	loaded, err := f.runtime.loadedExistingSchemaUnit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return f, old, oldSHA, loaded, drop
}

func TestSchemaMaintenanceProspectiveRecoveryMatchesActualOrdinaryHook(t *testing.T) {
	for _, host := range []string{"arch", "ubuntu"} {
		t.Run(host, func(t *testing.T) {
			f, old, oldSHA, loaded, drop := schemaRecoveryStartupFixture(t, host)
			oldBefore, _ := json.Marshal(old)
			oldHookBefore, err := os.ReadFile(drop)
			if err != nil {
				t.Fatal(err)
			}
			newPath := filepath.Join(merchantStoreCapsuleRoot(f.runtime.paths, "recovery-new"), "capsule.json")
			startup, unit, oldHook, hook, err := f.runtime.prospectiveSchemaRecoveryStartup(context.Background(), old, oldSHA, newPath, loaded)
			if err != nil || startup == old.Writer.StartupSHA256 || unit != old.Writer.SignedUnitSHA256 || oldHook != startupContentSHA256(oldHookBefore) || hook != startupContentSHA256(merchantStorePortableHookBytes(old.Binary, newPath, merchantStoreCapsuleHashPlaceholder)) {
				t.Fatalf("prospective seal: startup=%s unit=%s err=%v", startup, unit, err)
			}
			unchangedHook, _ := os.ReadFile(drop)
			oldAfter, _ := json.Marshal(old)
			if !bytes.Equal(oldBefore, oldAfter) || !bytes.Equal(oldHookBefore, unchangedHook) {
				t.Fatal("prospective capture changed old capsule or actual hook")
			}
			// This is a local sanitized fixture, not a systemd qualification. The
			// normal loaded-hook capture must produce precisely the prospective
			// digest when the actual persistent hook/property bytes are replaced.
			newSHA := strings.Repeat("b", 64)
			f.unitOutput = strings.ReplaceAll(strings.ReplaceAll(f.unitOutput, filepath.Join(old.Root, "capsule.json"), newPath), oldSHA, newSHA)
			if err := os.WriteFile(drop, merchantStorePortableHookBytes(old.Binary, newPath, newSHA), 0644); err != nil {
				t.Fatal(err)
			}
			actual, err := f.runtime.captureMerchantStorePortableStartup(context.Background(), newPath, newSHA)
			if err != nil || actual["startup_sha256"] != startup || actual["signed_unit_sha256"] != unit {
				t.Fatalf("actual ordinary capture differs from prospective seal: %+v %v", actual, err)
			}
		})
	}
}

func TestSchemaMaintenanceProspectiveRecoveryRejectsOldOrUnsealedHook(t *testing.T) {
	for _, mutation := range []string{"same old path", "uncanonical path", "arbitrary root", "wrong old capsule hash", "unprivileged", "file changed", "environment changed", "file order changed", "baseline"} {
		t.Run(mutation, func(t *testing.T) {
			f, old, oldSHA, loaded, drop := schemaRecoveryStartupFixture(t, "ubuntu")
			newPath := filepath.Join(merchantStoreCapsuleRoot(f.runtime.paths, "recovery-new"), "capsule.json")
			switch mutation {
			case "same old path":
				newPath = filepath.Join(old.Root, "capsule.json")
			case "uncanonical path":
				newPath += "/../capsule.json"
			case "arbitrary root":
				newPath = filepath.Join(t.TempDir(), "recovery-new", "capsule.json")
			case "wrong old capsule hash":
				oldSHA = strings.Repeat("c", 64)
			case "unprivileged":
				loaded["ExecStartPreEx"] = strings.ReplaceAll(loaded["ExecStartPreEx"], "privileged", "")
			case "file changed":
				if err := os.WriteFile(drop, append(merchantStorePortableHookBytes(old.Binary, filepath.Join(old.Root, "capsule.json"), oldSHA), []byte("Environment=NEW=value\n")...), 0644); err != nil {
					t.Fatal(err)
				}
			case "environment changed":
				loaded["Environment"] += " NEW=value"
			case "file order changed":
				rows := strings.Split(loaded["EnvironmentFiles"], "\n")
				rows[1], rows[2] = rows[2], rows[1]
				loaded["EnvironmentFiles"] = strings.Join(rows, "\n")
			case "baseline":
				old.Format = 2
			}
			if _, _, _, _, err := f.runtime.prospectiveSchemaRecoveryStartup(context.Background(), old, oldSHA, newPath, loaded); err == nil {
				t.Fatal("accepted unsealed old startup or redirected recovery path")
			}
		})
	}
}

func TestSchemaMaintenanceRecoveryProfileIsExactBinding(t *testing.T) {
	h, plan, _, binding, c := newSchemaMaintenanceRecoveryFixture(t)
	origin := binding.Origins[0]
	profile := productionSchemaMaintenanceRecoveryProfile{Format: schemaMaintenanceRecoveryProfileFormat, TransitionID: binding.TransitionID, PrePlanSHA256: binding.PrePlanSHA256, Host: origin.Host,
		OwnerSHA256: binding.OwnerSHA256, OutcomeSHA256: binding.Outcome.SHA256, PostSchema: binding.PostSchema, OldCapsuleSHA256: h.binding.CapsuleSHA256,
		StoppedCaptureSHA256: strings.Repeat("1", 64), OldHookSHA256: strings.Repeat("2", 64), ProspectiveHookSHA256: strings.Repeat("3", 64), CapsulePath: origin.Capsule.Path,
		ProviderSHA256: c.Candidate.PayloadSHA256, DeployEngineSHA256: c.Candidate.DeployEngineSHA256, StartupSHA256: origin.StartupSHA256, SignedUnitSHA256: origin.SignedUnitSHA256}
	if err := validateSchemaRecoveryProfileBinding(profile, binding, origin, h.binding.CapsuleSHA256); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*productionSchemaMaintenanceRecoveryProfile){
		"caller startup":          func(p *productionSchemaMaintenanceRecoveryProfile) { p.StartupSHA256 = strings.Repeat("9", 64) },
		"old catalog":             func(p *productionSchemaMaintenanceRecoveryProfile) { p.PostSchema = plan.PreSchema },
		"wrong outcome":           func(p *productionSchemaMaintenanceRecoveryProfile) { p.OutcomeSHA256 = strings.Repeat("9", 64) },
		"wrong owner":             func(p *productionSchemaMaintenanceRecoveryProfile) { p.OwnerSHA256 = strings.Repeat("9", 64) },
		"old capsule":             func(p *productionSchemaMaintenanceRecoveryProfile) { p.OldCapsuleSHA256 = strings.Repeat("9", 64) },
		"foreign host":            func(p *productionSchemaMaintenanceRecoveryProfile) { p.Host = plan.Origins[1].Host },
		"arbitrary path":          func(p *productionSchemaMaintenanceRecoveryProfile) { p.CapsulePath += "-other" },
		"missing journal capture": func(p *productionSchemaMaintenanceRecoveryProfile) { p.StoppedCaptureSHA256 = "" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := profile
			mutate(&changed)
			if validateSchemaRecoveryProfileBinding(changed, binding, origin, h.binding.CapsuleSHA256) == nil {
				t.Fatal("accepted changed native startup authority")
			}
		})
	}
}

func TestSchemaMaintenanceRecoveryOwnerReleaseReceiptIsStrict(t *testing.T) {
	for _, mutation := range []string{"valid", "wrong owner", "wrong transition", "wrong plan", "unknown fields", "duplicate fields", "not canonical", "future", "zero time", "public", "hardlink", "symlink"} {
		t.Run(mutation, func(t *testing.T) {
			h, plan, preSHA, binding, _ := newSchemaMaintenanceRecoveryFixture(t)
			now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
			h.runtime.now = func() time.Time { return now }
			released := productionSchemaMaintenanceOwnerReleased{"lmm-schema-maintenance-owner-released-v1", plan.TransitionID, preSHA, binding.OwnerSHA256, now.Add(-time.Second)}
			switch mutation {
			case "wrong owner":
				released.OwnerSHA256 = strings.Repeat("0", 64)
			case "wrong transition":
				released.TransitionID += "-other"
			case "wrong plan":
				released.PlanSHA256 = strings.Repeat("0", 64)
			case "future":
				released.FinishedUTC = now.Add(time.Second)
			case "zero time":
				released.FinishedUTC = time.Time{}
			}
			path := filepath.Join(h.binding.Workspace, "state", "owner-released.json")
			raw := schemaMaintenanceRecoveryWriteJSON(t, path, released)
			switch mutation {
			case "unknown fields":
				raw = bytes.Replace(raw, []byte("{\n"), []byte("{\n  \"approved\": true,\n"), 1)
			case "duplicate fields":
				raw = bytes.Replace(raw, []byte("{\n"), []byte("{\n  \"format\": \"lmm-schema-maintenance-owner-released-v1\",\n"), 1)
			case "not canonical":
				raw = bytes.TrimSpace(raw)
			case "public":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, path+".other"); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(path, path+".other"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".other", path); err != nil {
					t.Fatal(err)
				}
			}
			if mutation == "unknown fields" || mutation == "duplicate fields" || mutation == "not canonical" {
				schemaMaintenanceRecoveryWriteFile(t, path, raw)
			}
			_, sha, err := h.readRecoveryOwnerReleased(plan, preSHA, binding)
			if mutation == "valid" {
				if err != nil || sha != startupContentSHA256(raw) {
					t.Fatalf("valid release: %s %v", sha, err)
				}
			} else if err == nil {
				t.Fatal("accepted altered owner release receipt")
			}
		})
	}
}

func TestSchemaMaintenanceRecoveryEntryRequiresLivePhysicalAuthority(t *testing.T) {
	h, plan, preSHA, binding, _ := newSchemaMaintenanceRecoveryFixture(t)
	if _, err := h.captureRecoveryProfile(context.Background(), plan, preSHA, nil); err == nil {
		t.Fatal("profile accepted no actual physical owner callback")
	}
	if _, err := h.captureRecoveryProfile(context.Background(), plan, preSHA, func(context.Context) error { return errors.New("original physical owner is gone") }); err == nil {
		t.Fatal("profile accepted loss of original physical owner")
	}
	if _, err := h.startRecovery(context.Background(), plan, preSHA, binding, nil); err == nil {
		t.Fatal("start accepted no fixed two-origin native observations")
	}
}
