package deploycli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type sealedStartupFixture struct {
	runtime     productionRuntime
	contract    *productionExistingSchemaContract
	unitOutput  string
	files       []string
	unitPath    string
	unitContent []byte
	process     []byte
	commands    []productionCommand
}

// Sanitized representations of the two actual production host shapes: array
// properties repeat, unset hooks are omitted, command runtime fields vary.
func realShapeStartupFixture(t *testing.T, host string) *sealedStartupFixture {
	t.Helper()
	directory := t.TempDir()
	main := filepath.Join(directory, "lmm-api-go.env")
	tool := filepath.Join(directory, "tool-market.env")
	cluster := filepath.Join(directory, "cluster.env")
	unit := filepath.Join(directory, "lmm-api.service")
	write := func(path, value string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), mode); err != nil {
			t.Fatal(err)
		}
	}
	dsn := "postgres://user@127.0.0.1/lmm"
	write(main, string(hardenProductionEnvironment([]byte("SQL_DSN="+dsn+"\n"))), 0600)
	write(tool, "MERCHANT_STORE_ENCRYPTION_KEY=fixture-secret\n", 0600)
	write(cluster, "CACHE_MODE=fixture\n", 0600)
	unitContent := []byte("[Service]\nEnvironment=LMM_DB_MIGRATION_MODE=verify\nExecStart=/fixture/lmm-api serve\n")
	write(unit, string(unitContent), 0644)
	files := []string{main, tool}
	if host == "ubuntu" {
		files = []string{main, cluster, tool}
	}
	output := "Environment=LMM_DB_MIGRATION_MODE=verify GOMEMLIMIT=1GiB\nPassEnvironment=\nUnsetEnvironment=\nMainPID=1234\nInvocationID=" + strings.Repeat("1", 32) + "\nActiveState=active\nFragmentPath=" + unit + "\n"
	output += "ExecStart={ path=/fixture/lmm-api ; argv[]=/fixture/lmm-api serve ; ignore_errors=no ; start_time=[Wed 2026-10-07 12:00:00 UTC] ; pid=1234 ; code=(null) ; status=0/0 }\n"
	for i, path := range files {
		ignore := "no"
		if i == 0 {
			ignore = "yes"
		}
		output += "EnvironmentFiles=" + path + " (ignore_errors=" + ignore + ")\n"
	}
	if host == "arch" {
		output += "ExecStartPost={ path=/usr/bin/curl ; argv[]=" + existingSchemaReadinessCurl + " ; ignore_errors=no ; start_time=[Wed 2026-10-07 12:00:00 UTC] ; pid=5678 ; code=exited ; status=0 }\n"
		output += "ExecStartPost={ path=/usr/bin/sleep ; argv[]=" + existingSchemaReadinessSleep + " ; ignore_errors=no ; start_time=[Wed 2026-10-07 12:00:01 UTC] ; pid=5679 ; code=exited ; status=0 }\n"
	}
	f := &sealedStartupFixture{contract: testExistingSchemaContract(t), unitOutput: output, files: files, unitPath: unit, unitContent: unitContent, process: []byte("SQL_DSN=" + dsn + "\x00LMM_DB_MIGRATION_MODE=verify\x00")}
	f.runtime = productionRuntime{paths: productionPaths{ConfigDir: directory, Service: "lmm-api.service", InstalledBinary: "/fixture/lmm-api"}, requiredOwnerUID: uint32(os.Getuid())}
	f.runtime.maintenanceProcessEnvironment = func(_ int) ([]byte, error) { return f.process, nil }
	f.runtime.runner = existingSchemaTestRunner{run: func(command productionCommand) ([]byte, error) {
		f.commands = append(f.commands, command)
		switch command.Name {
		case commandSystemctl:
			if !slices.Contains(command.Args, "--all") || !command.Sensitive {
				return nil, errors.New("unit inspection is not explicit/private")
			}
			return []byte(f.unitOutput), nil
		case commandPSQL:
			if !command.Sensitive || !strings.Contains(strings.Join(command.Args, " "), "BEGIN READ ONLY") {
				return nil, errors.New("identity query is not private/read-only")
			}
			if slices.Contains(command.Env, "PGOPTIONS=-c search_path=other") {
				return bytes.Replace(testExistingSchemaIdentitySnapshot(), []byte(`"schema":"public"`), []byte(`"schema":"other"`), 1), nil
			}
			if slices.Contains(command.Env, "PGHOST=other") {
				return bytes.Replace(testExistingSchemaIdentitySnapshot(), []byte(`7560021234567890123`), []byte(`7560021234567890999`), 1), nil
			}
			return testExistingSchemaIdentitySnapshot(), nil
		case commandBsdtar:
			return f.unitContent, nil
		default:
			return nil, fmt.Errorf("unexpected command %s", command.Name)
		}
	}}
	return f
}
func (f *sealedStartupFixture) verify() error {
	return f.runtime.verifyExistingSchemaStartupMode(context.Background(), productionManifest{SchemaMode: productionSchemaModeVerifyExisting, ExistingSchemaContract: f.contract})
}
func sealFixture(t *testing.T, f *sealedStartupFixture) {
	t.Helper()
	if err := f.runtime.sealExistingSchemaStartup(context.Background(), f.contract); err != nil {
		t.Fatal(err)
	}
}

func TestExistingSchemaRealHostStartupShapesSealed(t *testing.T) {
	for _, host := range []string{"arch", "ubuntu"} {
		t.Run(host, func(t *testing.T) {
			f := realShapeStartupFixture(t, host)
			if err := f.verify(); err == nil {
				t.Fatal("multiple EnvFiles accepted without seal")
			}
			sealFixture(t, f)
			if f.contract.StartupSHA256 == "" || f.contract.SignedUnitSHA256 != startupContentSHA256(f.unitContent) {
				t.Fatal("startup or unit not sealed")
			}
			if err := f.verify(); err != nil {
				t.Fatal(err)
			}
			if err := f.runtime.verifyExistingSchemaSignedUnitBinding(context.Background(), f.contract, "candidate.pkg", "rollback.pkg"); err != nil {
				t.Fatal(err)
			}
			f.unitOutput = strings.ReplaceAll(f.unitOutput, "pid=5678", "pid=6000")
			f.unitOutput = strings.ReplaceAll(f.unitOutput, "GOMEMLIMIT=1GiB", "GOMEMLIMIT=2GiB")
			if err := f.verify(); err != nil {
				t.Fatalf("runtime fields or separately guarded memory changed startup identity: %v", err)
			}
			f.unitOutput = strings.ReplaceAll(f.unitOutput, "MainPID=1234", "MainPID=0")
			f.unitOutput = strings.ReplaceAll(f.unitOutput, "ActiveState=active", "ActiveState=inactive")
			if err := f.verify(); err != nil {
				t.Fatalf("inactive rollback startup failed: %v", err)
			}
		})
	}
}

func TestExistingSchemaSealedStartupRefusesMutations(t *testing.T) {
	write := func(t *testing.T, p, v string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(v), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]func(*testing.T, *sealedStartupFixture){
		"extra-envfile": func(t *testing.T, f *sealedStartupFixture) {
			p := filepath.Join(filepath.Dir(f.files[0]), "extra.env")
			write(t, p, "EXTRA=true\n")
			f.unitOutput += "EnvironmentFiles=" + p + " (ignore_errors=no)\n"
		},
		"duplicate-envfile": func(t *testing.T, f *sealedStartupFixture) {
			f.unitOutput += "EnvironmentFiles=" + f.files[0] + " (ignore_errors=no)\n"
		},
		"envfile-bytes": func(t *testing.T, f *sealedStartupFixture) { write(t, f.files[1], "NEW_VALUE=true\n") },
		"envfile-permissions": func(t *testing.T, f *sealedStartupFixture) {
			if err := os.Chmod(f.files[1], 0644); err != nil {
				t.Fatal(err)
			}
		},
		"envfile-symlink": func(t *testing.T, f *sealedStartupFixture) {
			p := f.files[1]
			content, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			write(t, p+".target", string(content))
			if err := os.Symlink(p+".target", p); err != nil {
				t.Fatal(err)
			}
		},
		"envfile-hardlink": func(t *testing.T, f *sealedStartupFixture) {
			if err := os.Link(f.files[1], f.files[1]+".link"); err != nil {
				t.Fatal(err)
			}
		},
		"unit-bytes": func(t *testing.T, f *sealedStartupFixture) {
			write(t, f.unitPath, "[Service]\nExecStart=/fixture/write-db\n")
			if err := os.Chmod(f.unitPath, 0644); err != nil {
				t.Fatal(err)
			}
		},
		"write-hook": func(t *testing.T, f *sealedStartupFixture) {
			f.unitOutput += "ExecStartPre={ path=/fixture/write-db ; argv[]=/fixture/write-db ; ignore_errors=no ; }\n"
		},
		"curl-post": func(t *testing.T, f *sealedStartupFixture) {
			f.unitOutput = strings.Replace(f.unitOutput, "--fail --silent", "--request POST --fail --silent", 1)
		},
		"curl-file-output": func(t *testing.T, f *sealedStartupFixture) {
			f.unitOutput = strings.Replace(f.unitOutput, "--output /dev/null", "--output /etc/unsafe", 1)
		},
		"curl-redirect": func(t *testing.T, f *sealedStartupFixture) {
			f.unitOutput = strings.Replace(f.unitOutput, "--fail --silent", "--location --fail --silent", 1)
		},
		"curl-url": func(t *testing.T, f *sealedStartupFixture) {
			f.unitOutput = strings.Replace(f.unitOutput, "http://127.0.0.1:3000/api/livez", "https://other.example/api/livez", 1)
		},
		"curl-shell": func(t *testing.T, f *sealedStartupFixture) {
			f.unitOutput = strings.Replace(f.unitOutput, "path=/usr/bin/curl", "path=/usr/bin/sh", 1)
		},
		"stop-hook": func(t *testing.T, f *sealedStartupFixture) {
			f.unitOutput += "ExecStop={ path=/fixture/write-db ; argv[]=/fixture/write-db ; ignore_errors=no ; }\n"
		},
		"future-pgoptions": func(t *testing.T, f *sealedStartupFixture) {
			f.unitOutput = strings.Replace(f.unitOutput, "LMM_DB_MIGRATION_MODE=verify", "LMM_DB_MIGRATION_MODE=verify \"PGOPTIONS=-c search_path=other\"", 1)
		},
		"process-pgoptions": func(t *testing.T, f *sealedStartupFixture) {
			f.process = append(f.process, []byte("PGOPTIONS=-c search_path=other\x00")...)
		},
		"process-pghost": func(t *testing.T, f *sealedStartupFixture) {
			f.process = append(f.process, []byte("PGHOST=other\x00")...)
		},
		"financial-env": func(t *testing.T, f *sealedStartupFixture) {
			f.unitOutput = strings.Replace(f.unitOutput, "LMM_DB_MIGRATION_MODE=verify", "LMM_DB_MIGRATION_MODE=verify LMM_CREDIT_TRANSITION_PLAN=old", 1)
		},
		"future-log-database": func(t *testing.T, f *sealedStartupFixture) {
			write(t, f.files[1], "LOG_SQL_DSN=postgres://user@other/log\n")
		},
		"unit-unset": func(t *testing.T, f *sealedStartupFixture) {
			f.unitOutput = strings.Replace(f.unitOutput, "UnsetEnvironment=", "UnsetEnvironment=SQL_DSN", 1)
		},
		"duplicate-scalar": func(t *testing.T, f *sealedStartupFixture) { f.unitOutput += "MainPID=1234\n" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := realShapeStartupFixture(t, "arch")
			sealFixture(t, f)
			mutate(t, f)
			if err := f.verify(); err == nil {
				t.Fatal("changed or unsafe startup accepted")
			}
		})
	}
}

func TestExistingSchemaStartupSealCanonicalDigestAndUnitBinding(t *testing.T) {
	f := realShapeStartupFixture(t, "arch")
	sealFixture(t, f)
	plan := testExistingSchemaReleasePlan(t)
	plan.ExistingSchemaContract = f.contract
	encoded, err := canonicalProductionReleasePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	tampered := plan
	changed := *f.contract
	changed.StartupSHA256 = strings.Repeat("e", 64)
	tampered.ExistingSchemaContract = &changed
	other, err := canonicalProductionReleasePlan(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(encoded, other) {
		t.Fatal("startup seal is outside canonical plan digest")
	}
	changed.SignedUnitSHA256 = strings.Repeat("f", 64)
	if err := f.runtime.verifyExistingSchemaSignedUnitBinding(context.Background(), &changed, "candidate.pkg", "rollback.pkg"); err == nil {
		t.Fatal("signed unit mismatch accepted")
	}
	if err := f.runtime.verifyExistingSchemaSignedUnitBinding(context.Background(), f.contract, "candidate.pkg", ""); err == nil {
		t.Fatal("missing N-1 binding accepted")
	}
	incomplete := *f.contract
	incomplete.SignedUnitSHA256 = ""
	if err := validateProductionExistingSchemaContract(&incomplete); err == nil {
		t.Fatal("partial startup seal accepted")
	}
}

func TestExistingSchemaStartupSealUsesEnvironmentFilePrecedence(t *testing.T) {
	for _, line := range []string{"LMM_DB_MIGRATION_MODE=apply\n", "LMM_CREDIT_TRANSITION_SHA256=legacy\n", "SQL_DSN=postgres://user@other/lmm\n"} {
		t.Run(strings.Split(line, "=")[0], func(t *testing.T) {
			f := realShapeStartupFixture(t, "ubuntu")
			if err := os.WriteFile(f.files[1], []byte(line), 0600); err != nil {
				t.Fatal(err)
			}
			if err := f.runtime.sealExistingSchemaStartup(context.Background(), f.contract); err == nil {
				t.Fatal("later unsafe EnvFile override accepted")
			}
		})
	}
}

// Exercise the sealed production-shaped configuration through the full native
// drain, verify, install, rollback and confirmation state machine.
func TestExistingSchemaSealedHostLifecycleKeepsVerificationOnly(t *testing.T) {
	for _, action := range []string{"confirm", "rollback"} {
		t.Run(action, func(t *testing.T) {
			fixture, plan, base := existingSchemaProductionFixture(t)
			main := filepath.Join(fixture.runtime.paths.ConfigDir, "lmm-api-go.env")
			content, err := os.ReadFile(main)
			if err != nil {
				t.Fatal(err)
			}
			tool := filepath.Join(fixture.runtime.paths.ConfigDir, "tool-market.env")
			if err := os.WriteFile(tool, []byte("TOOL_MARKET_TOKEN=fixture-private\n"), 0600); err != nil {
				t.Fatal(err)
			}
			unitPath := filepath.Join(fixture.runtime.paths.ConfigDir, "lmm-api.service")
			unitContent := []byte("[Service]\nEnvironment=LMM_DB_MIGRATION_MODE=verify\nExecStart=" + fixture.runtime.paths.InstalledBinary + " serve\n")
			if err := os.WriteFile(unitPath, unitContent, 0644); err != nil {
				t.Fatal(err)
			}
			fixture.runtime.runner = existingSchemaTestRunner{run: func(command productionCommand) ([]byte, error) {
				if command.Name == commandBsdtar && slices.Contains(command.Args, "usr/lib/systemd/system/lmm-api.service") {
					fixture.runner.commands = append(fixture.runner.commands, command)
					return unitContent, nil
				}
				if command.Name == commandSystemctl && slices.Contains(command.Args, "--property="+existingSchemaUnitProperties) {
					fixture.runner.commands = append(fixture.runner.commands, command)
					active, pid := "inactive", "0"
					if fixture.runner.serviceActive {
						active, pid = "active", "2147483600"
					}
					start := "{ path=" + fixture.runtime.paths.InstalledBinary + " ; argv[]=" + fixture.runtime.paths.InstalledBinary + " serve ; ignore_errors=no ; }"
					return []byte(fmt.Sprintf("Environment=LMM_DB_MIGRATION_MODE=verify\nEnvironmentFiles=%s (ignore_errors=yes)\nEnvironmentFiles=%s (ignore_errors=no)\nMainPID=%s\nInvocationID=%s\nActiveState=%s\nFragmentPath=%s\nExecStart=%s\nExecStartPost={ path=/usr/bin/curl ; argv[]=%s ; ignore_errors=no ; }\nExecStartPost={ path=/usr/bin/sleep ; argv[]=%s ; ignore_errors=no ; }\n", main, tool, pid, strings.Repeat("1", 32), active, unitPath, start, existingSchemaReadinessCurl, existingSchemaReadinessSleep)), nil
				}
				return base.Run(context.Background(), command)
			}}
			if err := fixture.runtime.sealExistingSchemaStartup(context.Background(), plan.ExistingSchemaContract); err != nil {
				t.Fatal(err)
			}
			canonical, err := canonicalProductionReleasePlan(plan)
			if err != nil {
				t.Fatal(err)
			}
			fixture.options.StagedPlanSHA256 = startupContentSHA256(canonical)
			if err := os.WriteFile(fixture.options.StagedPlanPath, canonical, 0600); err != nil {
				t.Fatal(err)
			}
			fixture.runner.commands = nil
			fixture.runner.events = nil
			status, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options)
			if err != nil {
				t.Fatal(err)
			}
			if status.Phase != "AWAITING_CONFIRMATION" {
				t.Fatalf("phase=%s", status.Phase)
			}
			if action == "confirm" {
				if status, err := fixture.runtime.confirm(context.Background(), fixture.workspace); err != nil || status.Phase != "CONFIRMED" {
					t.Fatalf("confirm=%v %s", err, status.Phase)
				}
			} else {
				if status, err := fixture.runtime.rollback(context.Background(), fixture.workspace, "sealed-host-test"); err != nil || status.Phase != "ROLLED_BACK" {
					t.Fatalf("rollback=%v %s", err, status.Phase)
				}
			}
			finalEnvironment, err := os.ReadFile(main)
			if err != nil || !bytes.Equal(finalEnvironment, content) {
				t.Fatal("sealed environment bytes changed during install or rollback")
			}
			verifications := 0
			for _, command := range fixture.runner.commands {
				if slices.Contains(command.Args, "--apply") {
					t.Fatal("sealed lifecycle executed candidate apply")
				}
				if slices.Contains(command.Args, "--verify") && slices.Contains(command.Args, "migrate") {
					verifications++
					for _, assignment := range command.Env {
						if strings.HasPrefix(assignment, "LMM_CREDIT_TRANSITION_PLAN=") || strings.HasPrefix(assignment, "LMM_CREDIT_TRANSITION_SHA256=") {
							t.Fatal("financial transition environment reached verification child")
						}
					}
				}
			}
			if verifications < 4 || fixture.runner.onlineWriteCount != 0 || slices.Index(fixture.runner.events, "systemd-stop") < 0 || !fixture.runner.serviceActive {
				t.Fatalf("native drain/verify/restore lost: verifies=%d events=%v", verifications, fixture.runner.events)
			}
		})
	}
}
