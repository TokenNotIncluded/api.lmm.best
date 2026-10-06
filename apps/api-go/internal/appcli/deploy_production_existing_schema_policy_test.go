//go:build !windows

package appcli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func testExistingSchemaReleasePlan(t *testing.T) productionReleasePlan {
	t.Helper()
	plan := testProductionReleasePlan(t, t.TempDir())
	plan.Format = productionExistingSchemaPlanFormat
	plan.SchemaMode = productionSchemaModeVerifyExisting
	plan.ExistingSchemaContract = testExistingSchemaContract(t)
	plan.WithBackups = false
	plan.BackupMode = "disabled"
	plan.AgeRecipient = productionReleaseFilePlan{}
	return plan
}

func TestExistingSchemaPlanPolicyRejectsDowngradeAndFinancialHandoff(t *testing.T) {
	plan := testExistingSchemaReleasePlan(t)
	if err := validateProductionReleasePlan(plan); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*productionReleasePlan)
	}{
		{"historical format", func(p *productionReleasePlan) { p.Format = 6 }},
		{"omitted mode", func(p *productionReleasePlan) { p.SchemaMode = "" }},
		{"unknown mode", func(p *productionReleasePlan) { p.SchemaMode = "skip" }},
		{"web only", func(p *productionReleasePlan) { p.GoChanged = false }},
		{"missing schema", func(p *productionReleasePlan) { p.ExistingSchemaContract = nil }},
		{"route contract changed", func(p *productionReleasePlan) { p.GoRollback.ContractRevision = strings.Repeat("f", 64) }},
		{"financial handoff", func(p *productionReleasePlan) {
			p.MaintenanceHandoff = &productionMaintenanceHandoff{Stage: "prebridge"}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := plan
			test.change(&changed)
			if err := validateProductionReleasePlan(changed); err == nil {
				t.Fatal("invalid existing-schema policy accepted")
			}
		})
	}
	// Component source revisions are independent. Only their verified route
	// contracts must agree, including candidate versus N-1 in this mode.
	plan.GoRollback.GitRevision = strings.Repeat("c", 40)
	plan.WebRollback.GitRevision = strings.Repeat("d", 40)
	plan.WebCandidate.GitRevision = plan.WebRollback.GitRevision
	if err := validateProductionReleasePlan(plan); err != nil {
		t.Fatalf("compatible mixed source revisions rejected: %v", err)
	}
}

func TestExistingSchemaPlanFlagsAreExplicitAndPaired(t *testing.T) {
	base := validProductionReleasePlanArguments(t.TempDir())
	for _, test := range []struct {
		name  string
		flags []string
		valid bool
	}{
		{"historical default", nil, true},
		{"new policy", []string{"--schema-mode", "verify-existing", "--schema-contract", "/tmp/schema.json", "--schema-contract-sha256", strings.Repeat("a", 64)}, true},
		{"missing seal", []string{"--schema-mode", "verify-existing", "--schema-contract", "/tmp/schema.json"}, false},
		{"unselected contract", []string{"--schema-contract", "/tmp/schema.json", "--schema-contract-sha256", strings.Repeat("a", 64)}, false},
		{"unknown mode", []string{"--schema-mode", "false"}, false},
		{"financial handoff", []string{"--schema-mode", "verify-existing", "--schema-contract", "/tmp/schema.json", "--schema-contract-sha256", strings.Repeat("a", 64), "--maintenance-handoff", "/tmp/handoff.json", "--maintenance-handoff-sha256", strings.Repeat("b", 64)}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			options, err := parseProductionReleasePlanOptions(append(slices.Clone(base), test.flags...), &bytes.Buffer{})
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v error=%v", test.valid, err)
			}
			if test.name == "historical default" && (options.SchemaMode != "" || options.SchemaContract != "") {
				t.Fatal("default policy changed")
			}
		})
	}
}

func TestHistoricalPlanCanonicalBytesDoNotContainNewPolicyFields(t *testing.T) {
	for _, format := range []int{5, 6} {
		plan := testProductionReleasePlan(t, t.TempDir())
		plan.Format = format
		if format == 6 {
			plan.WithBackups = false
			plan.AgeRecipient = productionReleaseFilePlan{}
			plan.BackupMode = "disabled"
		}
		canonical, err := canonicalProductionReleasePlan(plan)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(canonical, []byte("schema_mode")) || bytes.Contains(canonical, []byte("existing_schema_contract")) ||
			!bytes.HasPrefix(canonical, []byte(fmt.Sprintf("{\n  \"format\": %d,", format))) {
			t.Fatalf("historical format %d canonical fields changed", format)
		}
		if !productionReleasePlanSupportsControllerBackups(plan) && format == 6 {
			t.Fatal("format 6 backups no longer supported")
		}
	}
}

type existingSchemaTransactionRunner struct {
	base                   *fakeProductionRunner
	configPath             string
	snapshot               []byte
	snapshotReads          int
	driftAfterStop         bool
	candidateVerifyFailure bool
	loadedMode             string
	ambiguousStopOnce      bool
	starts                 int
	badProcessAfterStart   bool
	driftAfterStart        bool
}

func (runner *existingSchemaTransactionRunner) Run(ctx context.Context, command productionCommand) ([]byte, error) {
	if command.Name == commandPSQL && strings.Contains(strings.Join(command.Args, " "), "lmm-existing-startup-identity") {
		runner.base.commands = append(runner.base.commands, command)
		identity := testExistingSchemaIdentitySnapshot()
		if (runner.driftAfterStop && !runner.base.serviceActive) || (runner.driftAfterStart && runner.starts > 0) || bytes.Contains(runner.snapshot, []byte(`"schema_oid":2201`)) {
			identity = bytes.Replace(identity, []byte(`"schema_oid":2200`), []byte(`"schema_oid":2201`), 1)
		}
		return identity, nil
	}
	if command.Name == commandPSQL && strings.Contains(strings.Join(command.Args, " "), "pg_control_system") {
		runner.base.commands = append(runner.base.commands, command)
		runner.snapshotReads++
		if (runner.driftAfterStop && !runner.base.serviceActive) || (runner.driftAfterStart && runner.starts > 0) {
			return bytes.Replace(runner.snapshot, []byte(`"schema_oid":2200`), []byte(`"schema_oid":2201`), 1), nil
		}
		return runner.snapshot, nil
	}
	startupUnit := slices.ContainsFunc(command.Args, func(arg string) bool {
		return strings.HasPrefix(arg, "--property=Environment,EnvironmentFiles,PassEnvironment,UnsetEnvironment,MainPID,InvocationID,ActiveState")
	})
	if command.Name == commandSystemctl && startupUnit {
		runner.base.commands = append(runner.base.commands, command)
		active, pid := "inactive", "0"
		if runner.base.serviceActive {
			active, pid = "active", "2147483600"
		}
		mode := runner.loadedMode
		if mode == "" {
			mode = "verify"
		}
		return []byte(fmt.Sprintf("Environment=LMM_DB_MIGRATION_MODE=%s\nEnvironmentFiles=%s (ignore_errors=yes)\nPassEnvironment=\nUnsetEnvironment=\nMainPID=%s\nInvocationID=11111111111111111111111111111111\nActiveState=%s\nExecStart={ path=%s ; argv[]=%s serve ; ignore_errors=no ; }\nExecStartPre=\nExecStartPost=\nExecCondition=\nExecStop=\nExecStopPost=\n", mode, runner.configPath, pid, active, runner.base.installedBinary, runner.base.installedBinary)), nil
	}
	migrationIndex := slices.Index(command.Args, "migrate")
	if runner.candidateVerifyFailure && migrationIndex >= 0 && migrationIndex+1 < len(command.Args) && command.Args[migrationIndex+1] == "--verify" && strings.HasPrefix(filepath.Base(command.Dir), "candidate-") {
		runner.base.commands = append(runner.base.commands, command)
		return nil, errors.New("injected candidate schema verification failure")
	}
	if command.Name == commandSystemctl && len(command.Args) > 1 && command.Args[0] == "stop" && command.Args[len(command.Args)-1] == productionServiceName && runner.ambiguousStopOnce {
		runner.ambiguousStopOnce = false
		if _, err := runner.base.Run(ctx, command); err != nil {
			return nil, err
		}
		return nil, errors.New("injected stopped writer without durable stop verification")
	}
	if command.Name == commandSystemctl && len(command.Args) > 2 && command.Args[0] == "enable" && command.Args[1] == "--now" && command.Args[2] == productionServiceName {
		runner.starts++
	}
	return runner.base.Run(ctx, command)
}

func existingSchemaProductionFixture(t *testing.T) (productionFixture, productionReleasePlan, *existingSchemaTransactionRunner) {
	t.Helper()
	fixture := newProductionFixture(t)
	fixture.options.BackupDir = ""
	fixture.options.WithBackups = false
	fixture.options.SchemaMode = productionSchemaModeVerifyExisting
	runner := &existingSchemaTransactionRunner{base: fixture.runner, snapshot: testExistingSchemaSnapshot(), configPath: filepath.Join(fixture.runtime.paths.ConfigDir, "lmm-api-go.env")}
	fixture.runtime.runner = runner
	fixture.runtime.maintenanceProcessEnvironment = func(_ int) ([]byte, error) {
		if runner.badProcessAfterStart && runner.starts > 0 {
			return []byte("SQL_DSN=postgres://user:password@127.0.0.1/lmm\x00LMM_DB_MIGRATION_MODE=apply\x00"), nil
		}
		return []byte("SQL_DSN=postgres://user:password@127.0.0.1/lmm\x00LMM_DB_MIGRATION_MODE=verify\x00"), nil
	}
	plan := testExistingSchemaReleasePlan(t)
	plan.DeploymentID = fixture.workspace.id
	plan.ExpectedVersion = fixture.options.ExpectedVersion
	plan.GoChanged, plan.WebChanged = fixture.options.GoChanged, fixture.options.WebChanged
	plan.ObservationSeconds = int(fixture.options.ObservationWindow / time.Second)
	plan.ProbeBinary = productionReleaseFilePlan{Path: fixture.options.ProbeBinary, SHA256: fixture.options.ProbeBinarySHA256}
	plan.OperatorBinary = plan.ProbeBinary
	for _, pair := range []struct {
		path, digest, name string
		output             *productionReleasePackagePlan
	}{
		{fixture.options.GoPackage, fixture.options.GoPackageSHA256, productionAURPackageName, &plan.GoCandidate},
		{fixture.options.GoRollbackPackage, fixture.options.GoRollbackSHA256, productionAURPackageName, &plan.GoRollback},
		{fixture.options.WebPackage, fixture.options.WebPackageSHA256, productionWebPackageName, &plan.WebCandidate},
		{fixture.options.WebRollbackPackage, fixture.options.WebRollbackSHA256, productionWebPackageName, &plan.WebRollback},
	} {
		metadata, err := fixture.runtime.packageMetadata(context.Background(), pair.path, pair.name)
		if err != nil {
			t.Fatal(err)
		}
		payload := metadata.BinarySHA256
		if pair.name == productionWebPackageName {
			payload = metadata.IndexSHA256
		}
		*pair.output = testProductionReleasePackage(t, plan.ControllerWorkspace, pair.name, metadata.Version, pair.digest, payload)
		pair.output.PackagePath = pair.path
		pair.output.GitRevision, pair.output.ContractRevision = metadata.GitRevision, metadata.ContractRevision
	}
	canonical, err := canonicalProductionReleasePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	fixture.options.StagedPlanPath = filepath.Join(fixture.workspace.stagingDir, productionReleasePlanFilename)
	fixture.options.StagedPlanSHA256 = fmt.Sprintf("%x", sha256.Sum256(canonical))
	if err := os.WriteFile(fixture.options.StagedPlanPath, canonical, 0600); err != nil {
		t.Fatal(err)
	}
	fixture.runner.commands = nil
	fixture.runner.events = nil
	return fixture, plan, runner
}

func TestExistingSchemaApplyBindsExactPlanTuple(t *testing.T) {
	fixture, plan, _ := existingSchemaProductionFixture(t)
	if err := validateProductionExistingSchemaApply(fixture.options, plan); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*productionTransactionOptions)
	}{
		{"changed Go flag", func(o *productionTransactionOptions) { o.GoChanged = false }},
		{"changed Web flag", func(o *productionTransactionOptions) { o.WebChanged = false }},
		{"package digest", func(o *productionTransactionOptions) { o.GoRollbackSHA256 = strings.Repeat("d", 64) }},
		{"package path", func(o *productionTransactionOptions) { o.WebPackage = "/tmp/elsewhere.pkg.tar.zst" }},
		{"provider digest", func(o *productionTransactionOptions) { o.ProbeBinarySHA256 = strings.Repeat("e", 64) }},
		{"operator", func(o *productionTransactionOptions) { o.OperatorUser = "root" }},
		{"observation", func(o *productionTransactionOptions) { o.ObservationWindow += time.Second }},
		{"unselected backups", func(o *productionTransactionOptions) { o.WithBackups = true }},
		{"maintenance action", func(o *productionTransactionOptions) { o.Action = "maintenance-retry" }},
		{"downgrade mode", func(o *productionTransactionOptions) { o.SchemaMode = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := fixture.options
			test.change(&options)
			if err := validateProductionExistingSchemaApply(options, plan); err == nil {
				t.Fatal("changed immutable activation accepted")
			}
		})
	}
}

func TestExistingSchemaApplyCannotOmitModeFromStagedPlan(t *testing.T) {
	fixture, _, _ := existingSchemaProductionFixture(t)
	fixture.options.SchemaMode = ""
	fixture.options.StagedPlanPath = ""
	fixture.options.StagedPlanSHA256 = ""
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err == nil || !strings.Contains(err.Error(), "cannot omit") {
		t.Fatalf("omitted immutable policy was accepted: %v", err)
	}
	if len(fixture.runner.commands) != 0 || len(fixture.runner.events) != 0 || !fixture.runner.serviceActive {
		t.Fatalf("downgrade mutated production before refusal: events=%v", fixture.runner.events)
	}
	if _, err := os.Stat(fixture.workspace.manifestPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("downgrade wrote a legacy manifest: %v", err)
	}
}

func TestExistingSchemaApplyVerifiesBothProvidersWithoutApplyingAndConfirms(t *testing.T) {
	fixture, _, runner := existingSchemaProductionFixture(t)
	status, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "AWAITING_CONFIRMATION" {
		t.Fatalf("phase=%s", status.Phase)
	}
	manifest, err := fixture.runtime.readManifest(fixture.workspace)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Format != 9 || manifest.SchemaMode != "verify-existing" || manifest.SchemaPlanSHA256 != fixture.options.StagedPlanSHA256 {
		t.Fatalf("schema policy not preserved: %#v", manifest)
	}
	stop := slices.Index(fixture.runner.events, "systemd-stop")
	verifications := 0
	stopCommand, startCommand := -1, -1
	var runs []struct {
		name, provider string
		index          int
	}
	for commandIndex, command := range fixture.runner.commands {
		if command.Name == commandSystemctl && len(command.Args) > 1 && command.Args[0] == "stop" && command.Args[len(command.Args)-1] == fixture.runtime.paths.Service {
			stopCommand = commandIndex
		}
		if command.Name == commandSystemctl && len(command.Args) > 2 && command.Args[0] == "enable" && command.Args[1] == "--now" && command.Args[2] == fixture.runtime.paths.Service {
			startCommand = commandIndex
		}
		index := slices.Index(command.Args, "migrate")
		if index >= 0 && index+1 < len(command.Args) {
			if command.Args[index+1] != "--verify" {
				t.Fatalf("unexpected schema writer: %v", command.Args)
			}
			verifications++
			if index == 0 {
				t.Fatal("verification command lost its provider")
			}
			runs = append(runs, struct {
				name, provider string
				index          int
			}{filepath.Base(command.Dir), command.Args[index-1], commandIndex})
			if !strings.Contains(strings.Join(command.Env, "\n"), "default_transaction_read_only=on") {
				t.Fatal("verification child is not read-only")
			}
		}
	}
	if verifications != 4 || stop < 0 || slices.Index(fixture.runner.events, "migrate:--verify") > stop || fixture.runner.onlineWriteCount != 0 || runner.snapshotReads < 8 {
		t.Fatalf("missing independent pre/post verification: count=%d stop=%d snapshots=%d events=%v", verifications, stop, runner.snapshotReads, fixture.runner.events)
	}
	for index, expected := range []struct{ name, provider string }{
		{"candidate-preflight", fixture.runner.probeBinary}, {"rollback-preflight", fixture.runner.installedBinary},
		{"candidate-verify", fixture.runner.probeBinary}, {"rollback-verify", fixture.runner.installedBinary},
	} {
		actual := runs[index]
		if actual.name != expected.name || actual.provider != expected.provider || (index < 2 && actual.index >= stopCommand) ||
			(index >= 2 && (actual.index <= stopCommand || actual.index >= startCommand)) {
			t.Fatalf("provider verification outside required pre/post-stop order: runs=%#v stop=%d start=%d", runs, stopCommand, startCommand)
		}
	}
	confirmed, err := fixture.runtime.confirm(context.Background(), fixture.workspace)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Phase != "CONFIRMED" {
		t.Fatalf("confirmation phase=%s", confirmed.Phase)
	}
}

func TestExistingSchemaRollbackVerifiesInstalledNMinusOneBeforeRestart(t *testing.T) {
	fixture, _, _ := existingSchemaProductionFixture(t)
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err != nil {
		t.Fatal(err)
	}
	before := len(fixture.runner.commands)
	rolledBack, err := fixture.runtime.rollback(context.Background(), fixture.workspace, "existing-schema-test")
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Phase != "ROLLED_BACK" || fixture.runner.installedGoVersion != fixture.runner.oldVersion || !fixture.runner.serviceActive {
		t.Fatalf("rollback did not restore N-1: status=%#v events=%v", rolledBack, fixture.runner.events)
	}
	manifest, err := fixture.runtime.readManifest(fixture.workspace)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.BillingGate == nil || !manifest.BillingGate.AdmissionReopened || mustHashFile(t, filepath.Join(fixture.runtime.paths.NginxRoot, "lmm-api-locations.conf")) != manifest.BillingGate.OriginalSHA256 {
		t.Fatal("rollback did not restore billing ingress")
	}
	stopped, verified, started := -1, -1, -1
	for index, command := range fixture.runner.commands[before:] {
		if command.Name == commandSystemctl && len(command.Args) > 1 && command.Args[0] == "stop" && command.Args[len(command.Args)-1] == fixture.runtime.paths.Service {
			stopped = index
		}
		migration := slices.Index(command.Args, "migrate")
		if migration >= 0 && migration+1 < len(command.Args) {
			if command.Args[migration+1] != "--verify" || filepath.Base(command.Dir) != "rollback-recovery-verify" {
				t.Fatalf("rollback used another migration policy: %v", command.Args)
			}
			verified = index
		}
		if command.Name == commandSystemctl && len(command.Args) > 2 && command.Args[0] == "enable" && command.Args[1] == "--now" && command.Args[2] == fixture.runtime.paths.Service {
			started = index
		}
	}
	if stopped < 0 || verified <= stopped || started <= verified || fixture.runner.onlineWriteCount != 0 {
		t.Fatalf("N-1 verification was outside stopped restart boundary: stop=%d verify=%d start=%d", stopped, verified, started)
	}
}

func TestExistingSchemaConfirmationRejectsLaterSchemaDrift(t *testing.T) {
	fixture, _, runner := existingSchemaProductionFixture(t)
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err != nil {
		t.Fatal(err)
	}
	runner.snapshot = bytes.Replace(runner.snapshot, []byte(`"schema_oid":2200`), []byte(`"schema_oid":2201`), 1)
	if _, err := fixture.runtime.confirm(context.Background(), fixture.workspace); err == nil || !strings.Contains(err.Error(), "effective database identity or schema") {
		t.Fatalf("late schema drift accepted: %v", err)
	}
	status, err := fixture.runtime.readStatus(fixture.workspace)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "AWAITING_CONFIRMATION" {
		t.Fatalf("schema drift changed confirmation phase: %s", status.Phase)
	}
	if _, err := os.Stat(fixture.runtime.paths.TransactionLock); err != nil {
		t.Fatalf("failed confirmation discarded recovery lock: %v", err)
	}
}

func TestExistingSchemaStoppedUnchangedRecoveryRechecksNewGeneration(t *testing.T) {
	for _, failure := range []string{"none", "process mode", "schema drift"} {
		t.Run(failure, func(t *testing.T) {
			fixture, _, runner := existingSchemaProductionFixture(t)
			runner.ambiguousStopOnce = true
			if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err == nil {
				t.Fatal("ambiguous stop unexpectedly succeeded")
			}
			manifest, err := fixture.runtime.readManifest(fixture.workspace)
			if err != nil {
				t.Fatal(err)
			}
			if fixture.runner.serviceActive || manifest.BillingGate == nil || manifest.BillingGate.StopVerified || !manifest.BillingGate.AdmissionClosed || manifest.BillingGate.AdmissionReopened {
				t.Fatalf("fixture is not an unchanged stopped writer: %#v", manifest.BillingGate)
			}
			if failure == "process mode" {
				runner.badProcessAfterStart = true
			}
			if failure == "schema drift" {
				runner.driftAfterStart = true
			}
			before := len(fixture.runner.events)
			status, err := fixture.runtime.rollback(context.Background(), fixture.workspace, "unchanged-schema-recovery")
			if failure == "none" {
				if err != nil || status.Phase != "ROLLED_BACK" {
					t.Fatalf("unchanged recovery status=%#v error=%v", status, err)
				}
			} else {
				if err == nil {
					t.Fatal("changed restarted generation was accepted")
				}
				status, statusErr := fixture.runtime.readStatus(fixture.workspace)
				if statusErr != nil || status.Phase != "ROLLBACK_REQUIRED" {
					t.Fatalf("recovery status=%#v error=%v", status, statusErr)
				}
				manifest, manifestErr := fixture.runtime.readManifest(fixture.workspace)
				if manifestErr != nil || manifest.BillingGate.AdmissionReopened {
					t.Fatalf("bad generation reopened ingress: error=%v", manifestErr)
				}
				if _, lockErr := os.Stat(fixture.runtime.paths.TransactionLock); lockErr != nil {
					t.Fatalf("bad recovery released lock: %v", lockErr)
				}
			}
			for _, event := range fixture.runner.events[before:] {
				if event == "systemd-stop" || strings.HasPrefix(event, "paru-") || event == "migrate:--apply" {
					t.Fatalf("unchanged recovery replaced/wrote provider: %s", event)
				}
			}
		})
	}
}

func TestExistingSchemaPreflightFailuresDoNotStopOrInstall(t *testing.T) {
	for _, failure := range []string{"candidate verification", "N-1 verification", "startup mode"} {
		t.Run(failure, func(t *testing.T) {
			fixture, _, runner := existingSchemaProductionFixture(t)
			switch failure {
			case "candidate verification":
				runner.candidateVerifyFailure = true
			case "N-1 verification":
				fixture.runner.rollbackMigrationFailure = true
			case "startup mode":
				runner.loadedMode = "apply"
			}
			if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err == nil {
				t.Fatal("preflight failure accepted")
			}
			for _, event := range fixture.runner.events {
				if event == "systemd-stop" || event == "paru-go" || event == "paru-web-hook" {
					t.Fatalf("live mutation before verification: %s", event)
				}
			}
			if !fixture.runner.serviceActive {
				t.Fatal("preflight stopped running service")
			}
		})
	}
}

func TestExistingSchemaDriftAfterStopPreservesRecoveryBarrier(t *testing.T) {
	fixture, _, runner := existingSchemaProductionFixture(t)
	runner.driftAfterStop = true
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err == nil || !strings.Contains(err.Error(), "metadata changed") {
		t.Fatalf("schema drift error=%v", err)
	}
	status, err := fixture.runtime.readStatus(fixture.workspace)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "ROLLBACK_REQUIRED" || fixture.runner.serviceActive || !fixture.runner.nginxClosed {
		t.Fatalf("barrier not retained: status=%#v events=%v", status, fixture.runner.events)
	}
	if slices.Contains(fixture.runner.events, "paru-go") {
		t.Fatal("package installed after schema drift")
	}
	if _, err := fixture.runtime.rollback(context.Background(), fixture.workspace, "schema-drift-test"); err == nil {
		t.Fatal("rollback reopened a changed schema")
	}
	if fixture.runner.serviceActive || !fixture.runner.nginxClosed {
		t.Fatal("rollback reopened writer after schema drift")
	}
}

func TestExistingSchemaRecoveryManifestCannotLoseOrChangePolicy(t *testing.T) {
	fixture, _, _ := existingSchemaProductionFixture(t)
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(fixture.workspace.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"downgrade", "different schema", "different contract", "different package", "duplicate field"} {
		t.Run(kind, func(t *testing.T) {
			var manifest productionManifest
			if err := json.Unmarshal(original, &manifest); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "downgrade":
				manifest.Format = 8
				manifest.SchemaMode = ""
				manifest.ExistingSchemaContract = nil
				manifest.SchemaPlanSHA256 = ""
			case "different schema":
				manifest.DatabaseSchema = "other"
			case "different contract":
				manifest.ExistingSchemaContract.MetadataSHA256 = strings.Repeat("e", 64)
			case "different package":
				manifest.Go.CandidateGitRevision = strings.Repeat("f", 40)
			}
			raw, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "duplicate field" {
				raw = bytes.Replace(raw, []byte(`"format":9`), []byte(`"format":9,"format":9`), 1)
			}
			if err := os.WriteFile(fixture.workspace.manifestPath, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.runtime.readManifest(fixture.workspace); err == nil {
				t.Fatalf("recovery accepted %s", kind)
			}
			if err := os.WriteFile(fixture.workspace.manifestPath, original, 0600); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestExistingSchemaControllerTransportPreservesPlanAndMode(t *testing.T) {
	plan := testExistingSchemaReleasePlan(t)
	state := productionReleaseControllerState{RemoteWorkspace: "/var/lib/lmm-api/deployments/" + plan.DeploymentID, PlanSHA256: strings.Repeat("e", 64)}
	args := (&productionReleaseRuntime{}).productionApplyArguments(plan, state)
	start := slices.Index(args, "apply")
	if start < 0 {
		t.Fatal("apply command missing")
	}
	options, err := parseProductionTransactionOptions("apply", args[start+1:], &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if options.SchemaMode != plan.SchemaMode || options.StagedPlanSHA256 != state.PlanSHA256 || options.StagedPlanPath != filepath.Join(state.RemoteWorkspace, "staging", productionReleasePlanFilename) {
		t.Fatalf("transport lost policy: %#v", options)
	}
	if err := validateProductionExistingSchemaApply(options, plan); err != nil {
		t.Fatal(err)
	}
}

func TestExistingSchemaRecoveryWithoutBackupsRejectsLegacyInstalledCLI(t *testing.T) {
	plan := testExistingSchemaReleasePlan(t)
	state := productionReleaseControllerState{RemoteWorkspace: filepath.Join(defaultProductionPaths().WorkRoot, plan.DeploymentID)}
	digests := productionDispatchRemoteDigests(plan, state)
	installedProvider := filepath.Join(filepath.Dir(productionOperatorBinary), backendGoName)
	digests[installedProvider], digests[productionOperatorBinary] = strings.Repeat("0", 64), strings.Repeat("0", 64)
	runner := &productionDispatchFaultRunner{remoteDigests: digests}
	runtime := &productionReleaseRuntime{runner: runner}
	operator, err := runtime.controllerRecoveryOperator(context.Background(), plan, state)
	if err != nil || operator != productionRemoteOperatorPath(state) {
		t.Fatalf("format 7 recovery chose old format 8 reader: operator=%q error=%v", operator, err)
	}
	if runner.digestCalls[productionRemoteOperatorPath(state)] == 0 || runner.dispatchCalls != 0 {
		t.Fatal("recovery did not verify its candidate reader")
	}
}
