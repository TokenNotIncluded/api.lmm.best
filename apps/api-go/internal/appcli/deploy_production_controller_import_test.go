//go:build linux

package appcli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// These are bounded format fixtures, not real encrypted/database backups.
// Cryptographic EOF and PostgreSQL parser failures are modeled by the runner;
// archive traversal, metadata, digests and cleanup use real local files.
type controllerImportRunner struct {
	t             *testing.T
	plain         map[string][]byte
	calls         []productionCommand
	ageFailure    bool
	pgFailure     bool
	ageWrongBytes bool
	afterPG       func()
}

func (runner *controllerImportRunner) Run(ctx context.Context, command productionCommand) ([]byte, error) {
	runner.t.Helper()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runner.calls = append(runner.calls, command)
	if !command.Sensitive || command.Timeout <= 0 || command.Dir == "" {
		runner.t.Fatal("import command is not private, bounded and workspace-local")
	}
	for _, env := range command.Env {
		if strings.HasPrefix(env, "PG") || strings.Contains(env, "secret") {
			runner.t.Fatal("import command inherited PostgreSQL authority or secrets")
		}
	}
	switch command.Name {
	case commandAge:
		if len(command.Args) != 6 || !reflect.DeepEqual(command.Args[:2], []string{"--decrypt", "--identity"}) || command.Args[3] != "--output" {
			runner.t.Fatalf("unexpected age arguments: %#v", command.Args)
		}
		return runner.decryptFixture(command)
	case commandPGRestore:
		if len(command.Args) != 3 || command.Args[0] != "--format=custom" || command.Args[1] != "--file=/dev/null" {
			runner.t.Fatal("PostgreSQL import must read the full archive without database access")
		}
		if runner.afterPG != nil {
			runner.afterPG()
		}
		if runner.pgFailure {
			return nil, errors.New("private PostgreSQL diagnostic secret-password")
		}
		return nil, nil
	default:
		runner.t.Fatalf("forbidden import subprocess %q", command.Name)
		return nil, errors.New("forbidden command")
	}
}

// Separate the age fixture implementation to make output/EOF failure paths
// explicit without invoking age, PostgreSQL, SSH, tar or any other process.
func (runner *controllerImportRunner) decryptFixture(command productionCommand) ([]byte, error) {
	cipher, err := os.ReadFile(command.Args[5])
	if err != nil {
		return nil, err
	}
	plain, ok := runner.plain[controllerBackupDigest(cipher)]
	if !ok {
		runner.t.Fatal("age received unchecked or unexpected ciphertext")
	}
	info, err := os.Stat(command.Args[4])
	if err != nil || info.Mode().Perm() != 0o600 {
		runner.t.Fatal("plaintext output was not preclaimed privately")
	}
	if runner.ageWrongBytes {
		plain = append(append([]byte(nil), plain...), '!')
	}
	if err := os.WriteFile(command.Args[4], plain, 0o600); err != nil {
		return nil, err
	}
	if runner.ageFailure {
		return nil, errors.New("private age diagnostic secret-identity")
	}
	return nil, nil
}

type controllerImportFixture struct {
	t        *testing.T
	root     string
	identity string
	plan     productionReleasePlan
	set      controllerBackupSet
	runner   *controllerImportRunner
	runtime  *productionRuntime
}

func newControllerImportFixture(t *testing.T) *controllerImportFixture {
	t.Helper()
	base := t.TempDir()
	root, workspace := filepath.Join(base, "collection"), filepath.Join(base, "workspace")
	for _, dir := range []string{root, workspace, filepath.Join(workspace, "tmp")} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	fixture := &controllerImportFixture{t: t, root: root, identity: filepath.Join(base, "identity")}
	fixture.write(fixture.identity, []byte("fixture-private-age-identity"))
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	application, frontend := []byte("fixture-go-provider"), []byte("<!doctype html><title>N-1</title>")
	environment := []byte("DATABASE_URL=postgresql://user:secret@127.0.0.1:5432/production\nGIN_MODE=release\n")
	fixture.plan = productionReleasePlan{DeploymentID: "controller-import-test", ExpectedHost: productionExpectedHost, ControllerWorkspace: workspace,
		GoRollback:  productionReleasePackagePlan{PackageSHA256: controllerBackupDigest([]byte("go package")), PayloadSHA256: controllerBackupDigest(application)},
		WebRollback: productionReleasePackagePlan{PackageSHA256: controllerBackupDigest([]byte("web package")), PayloadSHA256: controllerBackupDigest(frontend)}}
	fixture.set = controllerBackupSet{Format: 1, DeploymentID: fixture.plan.DeploymentID, ExpectedHost: fixture.plan.ExpectedHost, CapturedUTC: now,
		DatabaseSchema: "public", EnvironmentSHA256: controllerBackupDigest(environment),
		GoRollbackSHA256: fixture.plan.GoRollback.PackageSHA256, WebRollbackSHA256: fixture.plan.WebRollback.PackageSHA256,
		GoRollbackPayloadSHA256: fixture.plan.GoRollback.PayloadSHA256, FrontendRollbackSHA256: fixture.plan.WebRollback.PayloadSHA256,
		Archives: make(map[string]controllerBackupArchive)}
	fixture.write(filepath.Join(workspace, productionWorkspaceMarker), []byte("format=1\ndeployment_id="+fixture.plan.DeploymentID+"\nrole=controller\n"))
	fixture.runner = &controllerImportRunner{t: t, plain: make(map[string][]byte)}
	fixture.runtime = &productionRuntime{runner: fixture.runner, now: func() time.Time { return now }, effectiveUID: os.Geteuid}
	fixture.archive("application", controllerImportTar(t, []controllerImportTarMember{{name: "usr/bin/lmm-api-go", data: application}}))
	fixture.archive("frontend", controllerImportGzip(t, controllerImportTar(t, []controllerImportTarMember{{name: "index.html", data: frontend}})))
	fixture.archive("configuration", controllerImportTar(t, []controllerImportTarMember{{name: "etc/lmm-api-go/lmm-api-go.env", data: environment}}))
	fixture.archive("database", []byte("PGDMP-fixture-full-custom-archive"))
	fixture.manifest(nil)
	return fixture
}

func (fixture *controllerImportFixture) write(name string, data []byte) {
	fixture.t.Helper()
	if err := os.WriteFile(name, data, 0o600); err != nil {
		fixture.t.Fatal(err)
	}
}

func (fixture *controllerImportFixture) archive(kind string, plain []byte) {
	fixture.t.Helper()
	// Synthetic cipher is deliberately larger than plaintext, as real age is.
	cipher := append([]byte("fixture-ciphertext-"+kind+strings.Repeat("!", 128)), plain...)
	fixture.set.Archives[kind] = controllerBackupArchive{CiphertextSHA256: controllerBackupDigest(cipher), PlaintextSHA256: controllerBackupDigest(plain),
		CiphertextBytes: int64(len(cipher)), PlaintextBytes: int64(len(plain))}
	fixture.runner.plain[controllerBackupDigest(cipher)] = plain
	fixture.write(filepath.Join(fixture.root, kind+".age"), cipher)
}

func (fixture *controllerImportFixture) manifest(change func([]byte) []byte) {
	fixture.t.Helper()
	data, err := json.Marshal(fixture.set)
	if err != nil {
		fixture.t.Fatal(err)
	}
	if change != nil {
		data = change(data)
	}
	fixture.write(filepath.Join(fixture.root, "backup-set.json"), data)
	fixture.write(filepath.Join(fixture.root, ".complete"), []byte(controllerBackupDigest(data)+"\n"))
}

func (fixture *controllerImportFixture) verify() (controllerBackupSet, string, error) {
	fixture.t.Helper()
	set, digest, err := fixture.runtime.verifyControllerBackupSet(context.Background(), fixture.plan, fixture.root, fixture.identity)
	entries, readErr := os.ReadDir(filepath.Join(fixture.plan.ControllerWorkspace, "tmp"))
	if readErr != nil || len(entries) != 0 {
		fixture.t.Fatalf("plaintext scratch survived verification: entries=%d err=%v", len(entries), readErr)
	}
	if err != nil && (digest != "" || set.Format != 0 || strings.Contains(err.Error(), "secret")) {
		fixture.t.Fatal("failed verification leaked diagnostics or returned usable evidence")
	}
	return set, digest, err
}

type controllerImportTarMember struct {
	name string
	data []byte
	kind byte
	link string
}

func controllerImportTar(t *testing.T, members []controllerImportTarMember) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, member := range members {
		kind := member.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		if err := writer.WriteHeader(&tar.Header{Name: member.name, Mode: 0o600, Typeflag: kind, Linkname: member.link, Size: int64(len(member.data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(member.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func controllerImportGzip(t *testing.T, plain []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestControllerBackupImportValidatesSequentiallyAndCleans(t *testing.T) {
	fixture := newControllerImportFixture(t)
	set, digest, err := fixture.verify()
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(fixture.root, "backup-set.json"))
	if err != nil || digest != controllerBackupDigest(manifest) || !reflect.DeepEqual(set, fixture.set) {
		t.Fatal("import did not return exact verified collection and manifest digest")
	}
	if len(fixture.runner.calls) != 5 {
		t.Fatalf("expected four age commands and one full pg_restore, got %d", len(fixture.runner.calls))
	}
	for index, command := range fixture.runner.calls {
		expected := commandAge
		if index == 4 {
			expected = commandPGRestore
		}
		if command.Name != expected {
			t.Fatal("verification command order is not sequential")
		}
	}
	if err := controllerBackupInventory(fixture.root, os.Geteuid()); err != nil {
		t.Fatal("successful import altered original inventory")
	}
}

func TestControllerBackupImportRejectsStrictJSONAndMetadata(t *testing.T) {
	cases := map[string]func(*controllerImportFixture){
		"cross-deployment": func(f *controllerImportFixture) { f.set.DeploymentID = "another-deployment"; f.manifest(nil) },
		"cross-host":       func(f *controllerImportFixture) { f.set.ExpectedHost = "archczy"; f.manifest(nil) },
		"missing-schema":   func(f *controllerImportFixture) { f.set.DatabaseSchema = ""; f.manifest(nil) },
		"reserved-schema":  func(f *controllerImportFixture) { f.set.DatabaseSchema = "pg_catalog"; f.manifest(nil) },
		"unsafe-schema":    func(f *controllerImportFixture) { f.set.DatabaseSchema = "public;DROP"; f.manifest(nil) },
		"future": func(f *controllerImportFixture) {
			f.set.CapturedUTC = f.set.CapturedUTC.Add(31 * time.Second)
			f.manifest(nil)
		},
		"stale": func(f *controllerImportFixture) {
			f.set.CapturedUTC = f.set.CapturedUTC.Add(-24*time.Hour - time.Second)
			f.manifest(nil)
		},
		"non-utc": func(f *controllerImportFixture) {
			f.set.CapturedUTC = f.set.CapturedUTC.In(time.FixedZone("east", 3600))
			f.manifest(nil)
		},
		"package-binding": func(f *controllerImportFixture) { f.set.GoRollbackSHA256 = f.set.WebRollbackSHA256; f.manifest(nil) },
		"payload-binding": func(f *controllerImportFixture) {
			f.set.GoRollbackPayloadSHA256 = f.set.FrontendRollbackSHA256
			f.manifest(nil)
		},
		"index-binding": func(f *controllerImportFixture) {
			f.set.FrontendRollbackSHA256 = f.set.GoRollbackPayloadSHA256
			f.manifest(nil)
		},
		"uppercase-digest": func(f *controllerImportFixture) { f.set.EnvironmentSHA256 = strings.Repeat("A", 64); f.manifest(nil) },
		"extra-archive": func(f *controllerImportFixture) {
			f.set.Archives["extra"] = f.set.Archives["database"]
			f.manifest(nil)
		},
		"missing-archive": func(f *controllerImportFixture) { delete(f.set.Archives, "database"); f.manifest(nil) },
		"null-archive": func(f *controllerImportFixture) {
			f.manifest(func(data []byte) []byte {
				var values map[string]any
				if err := json.Unmarshal(data, &values); err != nil {
					t.Fatal(err)
				}
				values["archives"] = nil
				result, err := json.Marshal(values)
				if err != nil {
					t.Fatal(err)
				}
				return result
			})
		},
		"negative-length": func(f *controllerImportFixture) {
			a := f.set.Archives["database"]
			a.PlaintextBytes = -1
			f.set.Archives["database"] = a
			f.manifest(nil)
		},
		"excessive-length": func(f *controllerImportFixture) {
			a := f.set.Archives["database"]
			a.CiphertextBytes = controllerBackupMaxArchive + 1
			f.set.Archives["database"] = a
			f.manifest(nil)
		},
		"age-expansion": func(f *controllerImportFixture) {
			a := f.set.Archives["database"]
			a.PlaintextBytes = a.CiphertextBytes + 1
			f.set.Archives["database"] = a
			f.manifest(nil)
		},
		"duplicate-field": func(f *controllerImportFixture) {
			f.manifest(func(data []byte) []byte { return append([]byte(`{"format":1,`), data[1:]...) })
		},
		"nested-duplicate": func(f *controllerImportFixture) {
			f.manifest(func(data []byte) []byte {
				return bytes.Replace(data, []byte(`"application":{`), []byte(`"application":{"ciphertext_bytes":1,`), 1)
			})
		},
		"unknown-field": func(f *controllerImportFixture) {
			f.manifest(func(data []byte) []byte { return append([]byte(`{"unknown":true,`), data[1:]...) })
		},
		"nested-unknown": func(f *controllerImportFixture) {
			f.manifest(func(data []byte) []byte {
				return bytes.Replace(data, []byte(`"application":{`), []byte(`"application":{"unknown":1,`), 1)
			})
		},
		"wrong-case": func(f *controllerImportFixture) {
			f.manifest(func(data []byte) []byte { return bytes.Replace(data, []byte(`"format"`), []byte(`"Format"`), 1) })
		},
		"second-value": func(f *controllerImportFixture) {
			f.manifest(func(data []byte) []byte { return append(data, []byte(` {}`)...) })
		},
		"truncated-json": func(f *controllerImportFixture) { f.manifest(func(data []byte) []byte { return data[:len(data)-1] }) },
		"bad-complete": func(f *controllerImportFixture) {
			f.write(filepath.Join(f.root, ".complete"), []byte(strings.Repeat("0", 64)+"\n"))
		},
		"complete-no-newline": func(f *controllerImportFixture) {
			data, err := os.ReadFile(filepath.Join(f.root, "backup-set.json"))
			if err != nil {
				t.Fatal(err)
			}
			f.write(filepath.Join(f.root, ".complete"), []byte(controllerBackupDigest(data)))
		},
		"workspace-cross-id": func(f *controllerImportFixture) {
			f.write(filepath.Join(f.plan.ControllerWorkspace, productionWorkspaceMarker), []byte("deployment_id=someone-else\n"))
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := newControllerImportFixture(t)
			mutate(fixture)
			if _, _, err := fixture.verify(); err == nil {
				t.Fatal("unsafe metadata accepted")
			}
			if len(fixture.runner.calls) != 0 {
				t.Fatal("invalid metadata reached subprocess execution")
			}
		})
	}
}

func TestControllerBackupImportRejectsInventoryAndLinks(t *testing.T) {
	for _, name := range []string{"extra", "missing", "empty", "file-mode", "root-mode", "symlink-member", "hardlink-member", "symlink-parent", "identity-symlink", "workspace-symlink", "wrong-owner"} {
		t.Run(name, func(t *testing.T) {
			fixture := newControllerImportFixture(t)
			member := filepath.Join(fixture.root, "application.age")
			var err error
			switch name {
			case "extra":
				fixture.write(filepath.Join(fixture.root, "unexpected.part"), []byte("partial"))
			case "missing":
				err = os.Remove(member)
			case "empty":
				fixture.write(member, nil)
			case "file-mode":
				err = os.Chmod(member, 0o644)
			case "root-mode":
				err = os.Chmod(fixture.root, 0o755)
			case "symlink-member", "hardlink-member":
				outside := filepath.Join(filepath.Dir(fixture.root), "saved.age")
				if err = os.Rename(member, outside); err == nil {
					if name == "symlink-member" {
						err = os.Symlink(outside, member)
					} else {
						err = os.Link(outside, member)
					}
				}
			case "symlink-parent":
				alias := filepath.Join(filepath.Dir(fixture.root), "alias")
				err = os.Symlink(filepath.Dir(fixture.root), alias)
				fixture.root = filepath.Join(alias, "collection")
			case "identity-symlink":
				alias := fixture.identity + "-alias"
				err = os.Symlink(fixture.identity, alias)
				fixture.identity = alias
			case "workspace-symlink":
				alias := fixture.plan.ControllerWorkspace + "-alias"
				err = os.Symlink(fixture.plan.ControllerWorkspace, alias)
				fixture.plan.ControllerWorkspace = alias
			case "wrong-owner":
				fixture.runtime.effectiveUID = func() int { return os.Geteuid() + 1 }
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := fixture.verify(); err == nil {
				t.Fatal("unsafe inventory accepted")
			}
			if len(fixture.runner.calls) != 0 {
				t.Fatal("unsafe inventory reached a subprocess")
			}
		})
	}
}

func TestControllerBackupImportSubprocessAndTamperFailuresCleanPlaintext(t *testing.T) {
	for _, name := range []string{"age-eof", "age-wrong-bytes", "pg-full-read", "cipher-tamper", "cipher-length", "plain-hash", "post-validation-tamper", "database-magic", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			fixture := newControllerImportFixture(t)
			switch name {
			case "age-eof":
				fixture.runner.ageFailure = true
			case "age-wrong-bytes":
				fixture.runner.ageWrongBytes = true
			case "pg-full-read":
				fixture.runner.pgFailure = true
			case "cipher-tamper":
				fixture.write(filepath.Join(fixture.root, "application.age"), []byte("tamper"))
			case "cipher-length":
				a := fixture.set.Archives["application"]
				a.CiphertextBytes++
				fixture.set.Archives["application"] = a
				fixture.manifest(nil)
			case "plain-hash":
				a := fixture.set.Archives["application"]
				a.PlaintextSHA256 = strings.Repeat("0", 64)
				fixture.set.Archives["application"] = a
				fixture.manifest(nil)
			case "post-validation-tamper":
				fixture.runner.afterPG = func() {
					fixture.write(filepath.Join(fixture.root, "application.age"), []byte("changed after age verification"))
				}
			case "database-magic":
				fixture.archive("database", []byte("not-a-custom-archive"))
				fixture.manifest(nil)
			case "cancelled":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if _, _, err := fixture.runtime.verifyControllerBackupSet(ctx, fixture.plan, fixture.root, fixture.identity); err == nil {
					t.Fatal("cancelled import accepted")
				}
				if len(fixture.runner.calls) != 0 {
					t.Fatal("cancelled import dispatched command")
				}
				return
			}
			if _, _, err := fixture.verify(); err == nil {
				t.Fatal("failed verification accepted")
			}
			if _, err := os.Stat(filepath.Join(fixture.root, "database.age")); err != nil {
				t.Fatal("failed import deleted encrypted original")
			}
		})
	}
}

func TestControllerBackupImportRejectsUnsafeTarAndGzip(t *testing.T) {
	for _, name := range []string{"traversal", "absolute", "dot-component", "backslash", "duplicate", "symlink", "hardlink", "device", "fifo", "missing-required", "required-directory", "payload-tamper", "truncated-body", "missing-footer", "partial-footer", "trailing-nonzero", "gzip-trailer", "gzip-truncated", "gzip-extra-stream", "ancestor-file", "late-ancestor-file"} {
		t.Run(name, func(t *testing.T) {
			fixture := newControllerImportFixture(t)
			required := controllerImportTarMember{name: "usr/bin/lmm-api-go", data: []byte("fixture-go-provider")}
			members := []controllerImportTarMember{required}
			switch name {
			case "traversal":
				members = append(members, controllerImportTarMember{name: "../escape", data: []byte("x")})
			case "absolute":
				members = append(members, controllerImportTarMember{name: "/escape", data: []byte("x")})
			case "dot-component":
				members = append(members, controllerImportTarMember{name: "a/../escape", data: []byte("x")})
			case "backslash":
				members = append(members, controllerImportTarMember{name: "a\\escape", data: []byte("x")})
			case "duplicate":
				members = append(members, required)
			case "symlink":
				members = append(members, controllerImportTarMember{name: "link", kind: tar.TypeSymlink, link: "outside"})
			case "hardlink":
				members = append(members, controllerImportTarMember{name: "link", kind: tar.TypeLink, link: required.name})
			case "device":
				members = append(members, controllerImportTarMember{name: "device", kind: tar.TypeChar})
			case "fifo":
				members = append(members, controllerImportTarMember{name: "fifo", kind: tar.TypeFifo})
			case "missing-required":
				members = []controllerImportTarMember{{name: "irrelevant", data: []byte("x")}}
			case "required-directory":
				members = []controllerImportTarMember{{name: required.name + "/", kind: tar.TypeDir}}
			case "payload-tamper":
				members[0].data = []byte("different-provider")
			case "ancestor-file":
				members = append([]controllerImportTarMember{{name: "usr", data: []byte("x")}}, members...)
			case "late-ancestor-file":
				members = append(members, controllerImportTarMember{name: "usr", data: []byte("x")})
			}
			archive := controllerImportTar(t, members)
			switch name {
			case "truncated-body":
				archive = archive[:513]
			case "missing-footer":
				archive = archive[:len(archive)-1024]
			case "partial-footer":
				archive = archive[:len(archive)-512]
			case "trailing-nonzero":
				archive = append(archive, []byte("hidden-data")...)
			case "gzip-trailer":
				archive = controllerImportGzip(t, archive)
				archive[len(archive)-8] ^= 0xff
			case "gzip-truncated":
				archive = controllerImportGzip(t, archive)
				archive = archive[:len(archive)-1]
			case "gzip-extra-stream":
				archive = append(controllerImportGzip(t, archive), controllerImportGzip(t, []byte("hidden-data"))...)
			}
			fixture.archive("application", archive)
			fixture.manifest(nil)
			if _, _, err := fixture.verify(); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}

func TestControllerBackupImportRejectsUnsafeConfiguration(t *testing.T) {
	for _, environment := range []string{
		"DATABASE_URL=$(command)\n", "DATABASE_URL=sqlite://local\n", "DATABASE_URL=postgres://\n",
		"DATABASE_URL=postgresql://host/db\nSQL_DSN=postgresql://other/db\n", "DATABASE_URL=postgresql://host/db\nDATABASE_URL=postgresql://host/db\n",
		"DATABASE_URL=postgresql://host/db\nexport TOKEN=secret\n",
	} {
		t.Run(controllerBackupDigest([]byte(environment))[:8], func(t *testing.T) {
			fixture := newControllerImportFixture(t)
			fixture.set.EnvironmentSHA256 = controllerBackupDigest([]byte(environment))
			fixture.archive("configuration", controllerImportTar(t, []controllerImportTarMember{{name: "etc/lmm-api-go/lmm-api-go.env", data: []byte(environment)}}))
			fixture.manifest(nil)
			if _, _, err := fixture.verify(); err == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
}

func TestControllerBackupImportClockBoundaries(t *testing.T) {
	fixture := newControllerImportFixture(t)
	now := fixture.runtime.now()
	for _, offset := range []time.Duration{-24 * time.Hour, 30 * time.Second} {
		set := fixture.set
		set.CapturedUTC = now.Add(offset)
		if err := controllerBackupValidateSet(set, fixture.plan, now); err != nil {
			t.Fatalf("valid clock boundary rejected: %v", err)
		}
	}
}
