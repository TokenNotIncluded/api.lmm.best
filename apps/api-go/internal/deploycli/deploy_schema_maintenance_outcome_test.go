//go:build !windows

package deploycli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type schemaMaintenanceOutcomeTestFixture struct {
	runtime  *productionRuntime
	plan     productionSchemaMaintenancePlan
	preSHA   string
	owner    productionMerchantStoreFenceOwner
	ownerSHA string
	worker   productionSchemaMaintenanceWorkerResult
	set      productionSchemaMaintenanceBackupSet
	proof    productionSchemaMaintenanceBackupProof
	out      productionSchemaMaintenanceOutcome
	state    string
}

func schemaMaintenanceOutcomeClone[T any](t *testing.T, value T) T {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var copy T
	if err := json.Unmarshal(raw, &copy); err != nil {
		t.Fatal(err)
	}
	return copy
}

func schemaMaintenanceOutcomeSourceFixture(schema productionExistingSchemaContract) productionSchemaMaintenanceBackupSource {
	column := func(position int, name string) productionSchemaMaintenanceBackupColumn {
		return productionSchemaMaintenanceBackupColumn{Position: position, Name: name, TypeName: "bigint", TypeOID: 20, TypeMod: -1, NotNull: true}
	}
	table := func(name string, oid int64, columns ...productionSchemaMaintenanceBackupColumn) productionSchemaMaintenanceBackupTable {
		names := []string{}
		for _, c := range columns {
			names = append(names, c.Name)
		}
		hash := startupContentSHA256([]byte(name + ":rows"))
		return productionSchemaMaintenanceBackupTable{Name: name, TableOID: oid, ColumnNames: names, Columns: columns, Rows: 2, SHA256: hash, ProjectedRows: 2, ProjectedSHA256: hash}
	}
	return productionSchemaMaintenanceBackupSource{Format: 1, TransactionReadOnly: "on", SystemIdentifier: schema.SystemIdentifier, Database: schema.Database,
		DatabaseOID: schema.DatabaseOID, Schema: schema.Schema, SchemaOID: schema.SchemaOID, Roles: []string{"lmm"}, StructureSHA256: startupContentSHA256([]byte("before-structure")),
		Tables:    []productionSchemaMaintenanceBackupTable{table("credits", 101, column(1, "id"), column(2, "amount")), table("referral_rewards", 102, column(1, "id"), column(2, "top_up_id"))},
		Indexes:   []productionSchemaMaintenanceBackupIndex{{Name: "credits_pkey", TableName: "credits", OID: 201, TableOID: 101, Definition: "CREATE UNIQUE INDEX credits_pkey ON credits USING btree (id)", Valid: true, Ready: true}},
		Sequences: []productionSchemaMaintenanceBackupSequence{{Name: "credits_id_seq", LastValue: 2, Called: true}}}
}

func newSchemaMaintenanceOutcomeTestFixture(t *testing.T) *schemaMaintenanceOutcomeTestFixture {
	t.Helper()
	// Synthetic source rows and the known test signing key qualify receipt
	// linkage here. The separate real-tools backup test proves actual restore.
	runtime, plan, _ := newSchemaMaintenancePlanFixture(t)
	preRaw, err := canonicalProductionSchemaMaintenancePlan(plan, runtime.paths)
	if err != nil {
		t.Fatal(err)
	}
	f := &schemaMaintenanceOutcomeTestFixture{runtime: runtime, plan: plan, preSHA: startupContentSHA256(preRaw), state: filepath.Join(plan.Origins[0].Workspace, "state")}
	captured := time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC)
	before := schemaMaintenanceOutcomeSourceFixture(plan.PreSchema)
	stops, err := schemaMaintenanceBackupStopBindings(plan, f.preSHA, schemaBackupTestStops(plan, f.preSHA, captured))
	if err != nil {
		t.Fatal(err)
	}
	f.set = productionSchemaMaintenanceBackupSet{Format: schemaMaintenanceBackupSetFormat, TransitionID: plan.TransitionID, PrePlanSHA256: f.preSHA, PreSchema: plan.PreSchema,
		CapturedUTC: captured, RecipientSHA256: plan.BackupPolicy.RecipientSHA256, StoppedOrigins: stops, SourceBefore: before, SourceAfter: before,
		Archive: controllerBackupArchive{CiphertextSHA256: strings.Repeat("a", 64), PlaintextSHA256: strings.Repeat("b", 64), CiphertextBytes: 100, PlaintextBytes: 50}}
	sourceRaw, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	f.proof = productionSchemaMaintenanceBackupProof{Format: schemaMaintenanceBackupProofFormat, TransitionID: plan.TransitionID, PrePlanSHA256: f.preSHA,
		PreSchema: plan.PreSchema, CapturedUTC: captured, VerifiedUTC: captured.Add(time.Second), RecipientSHA256: plan.BackupPolicy.RecipientSHA256, StoppedOrigins: stops,
		Archive: f.set.Archive, SourceSHA256: startupContentSHA256(sourceRaw), SourceLogicalSHA256: schemaMaintenanceBackupLogicalSHA(before), RestoredLogicalSHA256: schemaMaintenanceBackupLogicalSHA(before),
		RestoredSystemIdentifier: "123", RestoredDatabase: "schema_restore", RestoreKind: schemaMaintenanceBackupRestoreKind, ActualRestore: true, NoTCP: true, TableCount: 2, TotalRows: 4}
	f.owner = testSchemaMaintenanceOwner()
	f.owner.DeploymentID, f.owner.PlanSHA256, f.owner.ProviderSHA256 = plan.TransitionID, f.preSHA, plan.Worker.Provider.PayloadSHA256
	f.owner.HolderUnit = schemaMaintenanceHolderUnit(plan.TransitionID)
	f.owner.SystemIdentifier, f.owner.Database, f.owner.DatabaseOID, f.owner.Schema, f.owner.SchemaOID, f.owner.Role = plan.PreSchema.SystemIdentifier, plan.PreSchema.Database, plan.PreSchema.DatabaseOID, plan.PreSchema.Schema, plan.PreSchema.SchemaOID, plan.Role
	ownerRaw, err := canonicalMerchantStoreFenceOwner(f.owner)
	if err != nil {
		t.Fatal(err)
	}
	f.ownerSHA = startupContentSHA256(ownerRaw)
	schemaMaintenanceRecoveryWriteFile(t, filepath.Join(f.state, "schema-owner.json"), ownerRaw)
	output := []byte("PostgreSQL schema-only maintenance transaction committed; application data migrations were not run\n")
	f.worker = productionSchemaMaintenanceWorkerResult{Format: "lmm-schema-maintenance-worker-v1", TransitionID: plan.TransitionID, PlanSHA256: f.preSHA,
		OwnerSHA256: f.ownerSHA, ProviderSHA256: plan.Worker.Provider.PayloadSHA256, Phase: "WORKER_SUCCEEDED", OutputSHA256: startupContentSHA256(output),
		StartedUTC: captured.Add(24*time.Hour - time.Second), FinishedUTC: captured.Add(24*time.Hour + time.Second)}
	post := schemaMaintenanceOutcomeClone(t, before)
	post.StructureSHA256 = startupContentSHA256([]byte("post-structure"))
	post.Tables[0].Columns = append(post.Tables[0].Columns, productionSchemaMaintenanceBackupColumn{Position: 3, Name: "added", TypeName: "text", TypeOID: 25, TypeMod: -1})
	post.Tables[0].ColumnNames = append(post.Tables[0].ColumnNames, "added")
	post.Tables[0].SHA256, post.Tables[0].ProjectedSHA256 = strings.Repeat("c", 64), strings.Repeat("c", 64)
	post.Tables[1].Columns[1].NotNull = false
	empty := startupContentSHA256(nil)
	post.Tables = append(post.Tables, productionSchemaMaintenanceBackupTable{Name: "schema_features", TableOID: 103, ColumnNames: []string{"id"},
		Columns: []productionSchemaMaintenanceBackupColumn{{Position: 1, Name: "id", TypeName: "bigint", TypeOID: 20, TypeMod: -1}}, SHA256: empty, ProjectedSHA256: empty})
	post.Sequences = append(post.Sequences, productionSchemaMaintenanceBackupSequence{Name: "schema_features_id_seq", LastValue: 1})
	projected := schemaMaintenanceOutcomeClone(t, before)
	projected.StructureSHA256 = post.StructureSHA256
	projected.Tables[1].Columns[1].NotNull = false
	postSchema := plan.PreSchema
	postSchema.MetadataSHA256 = startupContentSHA256([]byte("post-full-catalog"))
	f.out = productionSchemaMaintenanceOutcome{Format: schemaMaintenanceOutcomeFormat, TransitionID: plan.TransitionID, PrePlanSHA256: f.preSHA, OwnerSHA256: f.ownerSHA,
		PreSchema: plan.PreSchema, PostSchema: postSchema, PostSource: post, ProjectedSource: projected, QualifiedProviderSHA256: plan.Worker.Provider.PayloadSHA256, CapturedUTC: f.worker.FinishedUTC.Add(time.Second)}
	f.runtime.now = func() time.Time { return captured.Add(72 * time.Hour) }
	f.writeSet(t)
	f.writeProof(t)
	f.writeWorker(t)
	schemaMaintenanceRecoveryWriteFile(t, filepath.Join(f.state, "schema-worker-output.log"), output)
	f.writeOutcome(t)
	if err := validateSchemaMaintenanceOutcome(f.out, f.plan, f.preSHA, f.ownerSHA, before, runtime.paths); err != nil {
		t.Fatalf("outcome fixture: %v", err)
	}
	return f
}

func (f *schemaMaintenanceOutcomeTestFixture) writeSet(t *testing.T) {
	raw := schemaMaintenanceRecoveryWriteJSON(t, filepath.Join(f.state, "schema-backup-set.json"), f.set)
	f.out.BackupSet = productionReleaseFilePlan{Path: filepath.Join(f.state, "schema-backup-set.json"), SHA256: startupContentSHA256(raw)}
	f.proof.BackupSetSHA256 = f.out.BackupSet.SHA256
}
func (f *schemaMaintenanceOutcomeTestFixture) writeProof(t *testing.T) {
	t.Helper()
	payload, err := json.Marshal(f.proof)
	if err != nil {
		t.Fatal(err)
	}
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, 32))
	raw := schemaMaintenanceRecoveryWriteJSON(t, filepath.Join(f.state, schemaMaintenanceBackupProofName), controllerBackupEnvelope{Format: 1, Payload: payload, Signature: hex.EncodeToString(ed25519.Sign(key, payload))})
	f.out.BackupProof = productionReleaseFilePlan{Path: filepath.Join(f.state, schemaMaintenanceBackupProofName), SHA256: startupContentSHA256(raw)}
}
func (f *schemaMaintenanceOutcomeTestFixture) writeWorker(t *testing.T) {
	raw := schemaMaintenanceRecoveryWriteJSON(t, filepath.Join(f.state, "schema-worker-result.json"), f.worker)
	f.out.WorkerResult = productionReleaseFilePlan{Path: filepath.Join(f.state, "schema-worker-result.json"), SHA256: startupContentSHA256(raw)}
	intent := f.worker
	intent.Phase, intent.OutputSHA256, intent.FinishedUTC = "WORKER_INTENT", "", time.Time{}
	schemaMaintenanceRecoveryWriteJSON(t, filepath.Join(f.state, "schema-worker-intent.json"), intent)
}
func (f *schemaMaintenanceOutcomeTestFixture) writeOutcome(t *testing.T) []byte {
	return schemaMaintenanceRecoveryWriteJSON(t, filepath.Join(f.state, "schema-outcome.json"), f.out)
}

func TestSchemaMaintenanceOutcomeRejectsChangedExecutionAndOldData(t *testing.T) {
	f := newSchemaMaintenanceOutcomeTestFixture(t)
	for name, change := range map[string]func(*productionSchemaMaintenanceOutcome){
		"untyped approval": func(o *productionSchemaMaintenanceOutcome) { o.Format = "approved" },
		"other transition": func(o *productionSchemaMaintenanceOutcome) { o.TransitionID += "-other" },
		"other preplan":    func(o *productionSchemaMaintenanceOutcome) { o.PrePlanSHA256 = strings.Repeat("0", 64) },
		"other owner":      func(o *productionSchemaMaintenanceOutcome) { o.OwnerSHA256 = strings.Repeat("0", 64) },
		"other provider": func(o *productionSchemaMaintenanceOutcome) {
			o.QualifiedProviderSHA256 = f.plan.Origins[0].Recovery.Candidate.PayloadSHA256
		},
		"wrong prior catalog":    func(o *productionSchemaMaintenanceOutcome) { o.PreSchema.MetadataSHA256 = strings.Repeat("0", 64) },
		"other physical cluster": func(o *productionSchemaMaintenanceOutcome) { o.PostSchema.SystemIdentifier = "1" },
		"other database OID":     func(o *productionSchemaMaintenanceOutcome) { o.PostSource.DatabaseOID++ },
		"other schema OID":       func(o *productionSchemaMaintenanceOutcome) { o.ProjectedSource.SchemaOID++ },
		"post startup claim": func(o *productionSchemaMaintenanceOutcome) {
			o.PostSchema.StartupSHA256 = strings.Repeat("0", 64)
			o.PostSchema.SignedUnitSHA256 = strings.Repeat("1", 64)
		},
		"no capture time":             func(o *productionSchemaMaintenanceOutcome) { o.CapturedUTC = time.Time{} },
		"redirected worker":           func(o *productionSchemaMaintenanceOutcome) { o.WorkerResult.Path += ".other" },
		"unsealed backup":             func(o *productionSchemaMaintenanceOutcome) { o.BackupSet.SHA256 = "" },
		"different full post catalog": func(o *productionSchemaMaintenanceOutcome) { o.PostSource.StructureSHA256 = strings.Repeat("0", 64) },
		"different full post roles":   func(o *productionSchemaMaintenanceOutcome) { o.PostSource.Roles = append(o.PostSource.Roles, "reader") },
		"old table removed":           func(o *productionSchemaMaintenanceOutcome) { o.PostSource.Tables = o.PostSource.Tables[1:] },
		"old table replaced":          func(o *productionSchemaMaintenanceOutcome) { o.ProjectedSource.Tables[0].TableOID++ },
		"old row removed": func(o *productionSchemaMaintenanceOutcome) {
			o.ProjectedSource.Tables[0].Rows--
			o.ProjectedSource.Tables[0].ProjectedRows--
		},
		"old row changed": func(o *productionSchemaMaintenanceOutcome) {
			o.ProjectedSource.Tables[0].SHA256 = strings.Repeat("0", 64)
			o.ProjectedSource.Tables[0].ProjectedSHA256 = strings.Repeat("0", 64)
		},
		"full old row count differs": func(o *productionSchemaMaintenanceOutcome) {
			o.PostSource.Tables[0].Rows++
			o.PostSource.Tables[0].ProjectedRows++
		},
		"old column type":           func(o *productionSchemaMaintenanceOutcome) { o.ProjectedSource.Tables[0].Columns[1].TypeOID = 23 },
		"old column default":        func(o *productionSchemaMaintenanceOutcome) { o.ProjectedSource.Tables[0].Columns[1].DefaultExpr = "1" },
		"old column position":       func(o *productionSchemaMaintenanceOutcome) { o.ProjectedSource.Tables[0].Columns[1].Position++ },
		"unapproved old NOT NULL":   func(o *productionSchemaMaintenanceOutcome) { o.ProjectedSource.Tables[0].Columns[1].NotNull = false },
		"full old column mismatch":  func(o *productionSchemaMaintenanceOutcome) { o.PostSource.Tables[0].Columns[1].TypeName = "integer" },
		"old index rebuilt":         func(o *productionSchemaMaintenanceOutcome) { o.ProjectedSource.Indexes[0].OID++ },
		"full index missing":        func(o *productionSchemaMaintenanceOutcome) { o.PostSource.Indexes = nil },
		"old sequence consumed":     func(o *productionSchemaMaintenanceOutcome) { o.ProjectedSource.Sequences[0].LastValue++ },
		"full sequence missing":     func(o *productionSchemaMaintenanceOutcome) { o.PostSource.Sequences = o.PostSource.Sequences[1:] },
		"new table application row": func(o *productionSchemaMaintenanceOutcome) { o.PostSource.Tables[2].Rows = 1 },
		"new sequence consumed":     func(o *productionSchemaMaintenanceOutcome) { o.PostSource.Sequences[1].Called = true },
	} {
		t.Run(name, func(t *testing.T) {
			out := schemaMaintenanceOutcomeClone(t, f.out)
			change(&out)
			if validateSchemaMaintenanceOutcome(out, f.plan, f.preSHA, f.ownerSHA, f.set.SourceBefore, f.runtime.paths) == nil {
				t.Fatal("accepted changed execution authority or inconsistent preserved old data")
			}
		})
	}
}

func schemaMaintenanceOutcomeRequireRootFixture(t *testing.T) {
	t.Helper()
	if os.Getenv("LMM_SCHEMA_OUTCOME_ROOT_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("requires explicit root-private local receipt fixture")
	}
	// The owner reader intentionally rejects every writable or non-root-owned
	// ancestor, including /tmp. This uses /run only for newly owned test files.
	t.Setenv("TMPDIR", "/run")
}

func TestSchemaMaintenanceOutcomeNativeReaderPreservesOriginalDispatchFreshness(t *testing.T) {
	schemaMaintenanceOutcomeRequireRootFixture(t)
	f := newSchemaMaintenanceOutcomeTestFixture(t)
	actual, digest, err := f.runtime.readSchemaMaintenanceOutcome(f.plan, f.preSHA, f.ownerSHA)
	raw, readErr := os.ReadFile(filepath.Join(f.state, "schema-outcome.json"))
	if err != nil || readErr != nil || !reflect.DeepEqual(actual, f.out) || digest != startupContentSHA256(raw) {
		t.Fatalf("exact root-private signed-backup/worker outcome: %v %v", err, readErr)
	}
	if !f.worker.StartedUTC.After(f.proof.CapturedUTC.Add(23*time.Hour)) || !f.runtime.now().After(f.proof.CapturedUTC.Add(24*time.Hour)) {
		t.Fatal("freshness fixture did not cross the 24 hour boundary")
	}
	// Reading a completed transition days later retains the original dispatch
	// authorization instant. It does not grant a new worker invocation.
	f.runtime.now = func() time.Time { return f.out.CapturedUTC.Add(30 * 24 * time.Hour) }
	if _, _, err := f.runtime.readSchemaMaintenanceOutcome(f.plan, f.preSHA, f.ownerSHA); err != nil {
		t.Fatalf("completed outcome expired after dispatch: %v", err)
	}
}

func TestSchemaMaintenanceOutcomeNativeReaderRejectsUnboundEvidence(t *testing.T) {
	schemaMaintenanceOutcomeRequireRootFixture(t)
	for name, change := range map[string]func(*testing.T, *schemaMaintenanceOutcomeTestFixture){
		"signed backup source hash": func(t *testing.T, f *schemaMaintenanceOutcomeTestFixture) {
			f.proof.SourceSHA256 = strings.Repeat("0", 64)
			f.writeProof(t)
		},
		"signed backup logical source": func(t *testing.T, f *schemaMaintenanceOutcomeTestFixture) {
			f.proof.SourceLogicalSHA256, f.proof.RestoredLogicalSHA256 = strings.Repeat("0", 64), strings.Repeat("0", 64)
			f.writeProof(t)
		},
		"signed backup set hash": func(t *testing.T, f *schemaMaintenanceOutcomeTestFixture) {
			f.proof.BackupSetSHA256 = strings.Repeat("0", 64)
			f.writeProof(t)
		},
		"signed archive differs": func(t *testing.T, f *schemaMaintenanceOutcomeTestFixture) {
			f.proof.Archive.PlaintextSHA256 = strings.Repeat("0", 64)
			f.writeProof(t)
		},
		"recipe only backup proof": func(t *testing.T, f *schemaMaintenanceOutcomeTestFixture) {
			f.proof.ActualRestore = false
			f.writeProof(t)
		},
		"proof after dispatch": func(t *testing.T, f *schemaMaintenanceOutcomeTestFixture) {
			f.proof.VerifiedUTC = f.worker.StartedUTC.Add(time.Second)
			f.writeProof(t)
		},
		"backup older than dispatch authorization": func(t *testing.T, f *schemaMaintenanceOutcomeTestFixture) {
			f.worker.StartedUTC = f.proof.CapturedUTC.Add(24*time.Hour + time.Second)
			f.worker.FinishedUTC = f.worker.StartedUTC.Add(time.Second)
			f.out.CapturedUTC = f.worker.FinishedUTC.Add(time.Second)
			f.writeWorker(t)
		},
		"capture before worker completion": func(_ *testing.T, f *schemaMaintenanceOutcomeTestFixture) {
			f.out.CapturedUTC = f.worker.FinishedUTC.Add(-time.Nanosecond)
		},
		"capture in future": func(_ *testing.T, f *schemaMaintenanceOutcomeTestFixture) {
			f.out.CapturedUTC = f.runtime.now().Add(31 * time.Second)
		},
		"successful result replaced after outcome": func(t *testing.T, f *schemaMaintenanceOutcomeTestFixture) {
			f.worker.FinishedUTC = f.worker.FinishedUTC.Add(time.Millisecond)
			schemaMaintenanceRecoveryWriteJSON(t, filepath.Join(f.state, "schema-worker-result.json"), f.worker)
		},
		"worker intent replaced": func(t *testing.T, f *schemaMaintenanceOutcomeTestFixture) {
			schemaMaintenanceRecoveryWriteFile(t, filepath.Join(f.state, "schema-worker-intent.json"), []byte("{}\n"))
		},
		"worker output replaced": func(t *testing.T, f *schemaMaintenanceOutcomeTestFixture) {
			schemaMaintenanceRecoveryWriteFile(t, filepath.Join(f.state, "schema-worker-output.log"), []byte("success\n"))
		},
		"owner physical identity changed": func(t *testing.T, f *schemaMaintenanceOutcomeTestFixture) {
			f.owner.DatabaseOID++
			raw, err := canonicalMerchantStoreFenceOwner(f.owner)
			if err != nil {
				t.Fatal(err)
			}
			f.ownerSHA = startupContentSHA256(raw)
			f.out.OwnerSHA256, f.worker.OwnerSHA256 = f.ownerSHA, f.ownerSHA
			schemaMaintenanceRecoveryWriteFile(t, filepath.Join(f.state, "schema-owner.json"), raw)
			f.writeWorker(t)
		},
		"signed proof damaged": func(t *testing.T, f *schemaMaintenanceOutcomeTestFixture) {
			path := filepath.Join(f.state, schemaMaintenanceBackupProofName)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			raw[len(raw)-5] ^= 1
			schemaMaintenanceRecoveryWriteFile(t, path, raw)
			f.out.BackupProof.SHA256 = startupContentSHA256(raw)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSchemaMaintenanceOutcomeTestFixture(t)
			change(t, f)
			f.writeOutcome(t)
			if _, _, err := f.runtime.readSchemaMaintenanceOutcome(f.plan, f.preSHA, f.ownerSHA); err == nil {
				t.Fatal("accepted changed signed backup, original owner, worker or chronology")
			}
		})
	}
}

func TestSchemaMaintenanceOutcomeNativeReaderRequiresRootPrivateCanonicalFiles(t *testing.T) {
	schemaMaintenanceOutcomeRequireRootFixture(t)
	for name, change := range map[string]func(*testing.T, string){
		"public": func(t *testing.T, p string) {
			if err := os.Chmod(p, 0644); err != nil {
				t.Fatal(err)
			}
		},
		"foreign owner": func(t *testing.T, p string) {
			if err := os.Chown(p, 65534, 65534); err != nil {
				t.Fatal(err)
			}
		},
		"hard linked": func(t *testing.T, p string) {
			if err := os.Link(p, p+".alias"); err != nil {
				t.Fatal(err)
			}
		},
		"symlinked": func(t *testing.T, p string) {
			if err := os.Rename(p, p+".original"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(p+".original", p); err != nil {
				t.Fatal(err)
			}
		},
		"missing": func(t *testing.T, p string) {
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
		},
	} {
		for _, file := range []string{"schema-outcome.json", "schema-owner.json", "schema-worker-intent.json", "schema-worker-result.json", "schema-worker-output.log", "schema-backup-set.json", schemaMaintenanceBackupProofName} {
			t.Run(file+"/"+name, func(t *testing.T) {
				f := newSchemaMaintenanceOutcomeTestFixture(t)
				change(t, filepath.Join(f.state, file))
				if _, _, err := f.runtime.readSchemaMaintenanceOutcome(f.plan, f.preSHA, f.ownerSHA); err == nil {
					t.Fatal("unsafe source file admitted by typed outcome reader")
				}
			})
		}
	}
	for name, change := range map[string]func([]byte) []byte{
		"noncanonical whitespace": func(raw []byte) []byte { return append([]byte(" "), raw...) },
		"duplicate field": func(raw []byte) []byte {
			return bytes.Replace(raw, []byte("{\n"), []byte("{\n  \"format\": \""+schemaMaintenanceOutcomeFormat+"\",\n"), 1)
		},
		"unknown field": func(raw []byte) []byte {
			return bytes.Replace(raw, []byte("{\n"), []byte("{\n  \"approved\": true,\n"), 1)
		},
		"trailing record": func(raw []byte) []byte { return append(raw, []byte("{}\n")...) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newSchemaMaintenanceOutcomeTestFixture(t)
			path := filepath.Join(f.state, "schema-outcome.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			schemaMaintenanceRecoveryWriteFile(t, path, change(raw))
			if _, _, err := f.runtime.readSchemaMaintenanceOutcome(f.plan, f.preSHA, f.ownerSHA); err == nil {
				t.Fatal("ambiguous or noncanonical native outcome admitted")
			}
		})
	}
}

func TestSchemaMaintenanceOutcomeSealedReaderHasExplicitBound(t *testing.T) {
	r := &productionRuntime{requiredOwnerUID: uint32(os.Geteuid())}
	root := t.TempDir()
	path := filepath.Join(root, "large-private.json")
	raw := bytes.Repeat([]byte("x"), (1<<20)+17)
	schemaMaintenanceRecoveryWriteFile(t, path, raw)
	if _, err := r.readExistingSchemaSealedFile(path, true); err == nil {
		t.Fatal("ordinary startup/capsule reader silently gained a larger budget")
	}
	actual, err := r.readExistingSchemaSealedFileBounded(path, true, int64(len(raw)))
	if err != nil || !bytes.Equal(actual, raw) {
		t.Fatalf("explicit >1MiB private projection read: %v", err)
	}
	for _, bound := range []int64{-1, 0, 1 << 20, (16 << 20) + 1} {
		if _, err := r.readExistingSchemaSealedFileBounded(path, true, bound); err == nil {
			t.Fatalf("invalid or insufficient bound accepted: %d", bound)
		}
	}
	for name, change := range map[string]func(*testing.T, string){
		"public": func(t *testing.T, p string) {
			if err := os.Chmod(p, 0644); err != nil {
				t.Fatal(err)
			}
		},
		"hard link": func(t *testing.T, p string) {
			if err := os.Link(p, p+".alias"); err != nil {
				t.Fatal(err)
			}
		},
		"symlink": func(t *testing.T, p string) {
			if err := os.Rename(p, p+".original"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(p+".original", p); err != nil {
				t.Fatal(err)
			}
		},
		"writable ancestor": func(t *testing.T, p string) {
			if err := os.Chmod(filepath.Dir(p), 0777); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "large.json")
			schemaMaintenanceRecoveryWriteFile(t, p, raw)
			change(t, p)
			if _, err := r.readExistingSchemaSealedFileBounded(p, true, 16<<20); err == nil {
				t.Fatal("larger bound bypassed file/ancestor safety")
			}
		})
	}
	file, err := os.OpenFile(filepath.Join(root, "over-budget.json"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate((16 << 20) + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.readExistingSchemaSealedFileBounded(file.Name(), true, 16<<20); err == nil {
		t.Fatal("outcome reader exceeded the 16MiB hard ceiling")
	}
}

// Every connection is made to this test's newly initialized private cluster.
// In particular, no application DSN, libpq service file, or existing PG server
// is an input. This qualifies only the fixed SQL guard, not systemd or the
// outer same-process physical deployment owner.
func TestSchemaMaintenanceOutcomeGuardActualPrivatePostgres(t *testing.T) {
	if os.Getenv("LMM_TEST_POSTGRES") != "1" {
		t.Skip("set LMM_TEST_POSTGRES=1 for own isolated PostgreSQL")
	}
	if os.Geteuid() == 0 {
		t.Skip("initdb requires unprivileged test process")
	}
	for _, binary := range []string{"initdb", "pg_ctl"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skip("owned PostgreSQL tools unavailable")
		}
	}
	for _, assignment := range os.Environ() {
		key, _, _ := strings.Cut(assignment, "=")
		if strings.HasPrefix(key, "PG") || key == "SQL_DSN" || key == "LOG_SQL_DSN" || key == "DATABASE_URL" {
			t.Setenv(key, "")
		}
	}
	root := t.TempDir()
	data := filepath.Join(root, "data")
	socket, err := os.MkdirTemp("/tmp", "lmm-outcome-guard-pg-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socket) })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	run := func(binary string, args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		body, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("owned %s failed: %v\n%s", binary, err, body)
		}
	}
	run("initdb", "-D", data, "--auth=trust", "--no-locale", "--encoding=UTF8", "--username=lmm_outcome_guard_test")
	run("pg_ctl", "-D", data, "-l", filepath.Join(root, "postgres.log"), "-w", "-t", "20", "-o", fmt.Sprintf("-h 127.0.0.1 -k %s -p %d", socket, port), "start")
	t.Cleanup(func() { run("pg_ctl", "-D", data, "-m", "fast", "-w", "-t", "20", "stop") })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dsn := fmt.Sprintf("postgres://lmm_outcome_guard_test@127.0.0.1:%d/postgres?sslmode=disable", port)
	holder, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close(context.Background())
	capture, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Close(context.Background())
	holderPID, capturePID := int(holder.PgConn().PID()), int(capture.PgConn().PID())
	guarded, err := schemaMaintenanceOutcomeGuardQuery(`SELECT 1;`, holderPID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := capture.Exec(ctx, guarded); err != nil {
		t.Fatalf("guard rejected its own query plus the actual live holder: %v", err)
	}
	if capture.PgConn().TxStatus() != 'I' {
		t.Fatal("successful guarded capture did not commit")
	}

	// The holder blocks only the body, after the leading guard has completed.
	// Observing this actual advisory wait is a synchronization barrier; no
	// sleep is used to guess whether the leading pg_stat_activity scan ran.
	const bodyLock int64 = 0x4c4d4d4f55540001
	if _, err := holder.Exec(ctx, `SELECT pg_catalog.pg_advisory_lock($1)`, bodyLock); err != nil {
		t.Fatal(err)
	}
	guarded, err = schemaMaintenanceOutcomeGuardQuery(fmt.Sprintf(`SELECT pg_catalog.pg_advisory_lock(%d); SELECT pg_catalog.pg_advisory_unlock(%d);`, bodyLock, bodyLock), holderPID)
	if err != nil {
		t.Fatal(err)
	}
	queryCtx, queryCancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := capture.Exec(queryCtx, guarded)
		done <- err
	}()
	queryCollected := false
	defer func() {
		queryCancel()
		if !queryCollected {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("guarded query did not stop after cancellation")
			}
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := holder.Exec(ctx, `SELECT pg_catalog.pg_stat_clear_snapshot()`); err != nil {
			t.Fatal(err)
		}
		var blocked bool
		if err := holder.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE pid=$1 AND state='active' AND wait_event_type='Lock' AND wait_event='advisory')`, capturePID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-done:
			queryCollected = true
			t.Fatalf("guarded query completed before the body synchronization barrier: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("guarded query never reached the body advisory wait")
		}
		time.Sleep(10 * time.Millisecond)
	}
	third, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close(context.Background())
	// Keep this third connection alive through the trailing guard. An extra
	// observer is deliberately not used: the original holder observes it.
	if _, err := holder.Exec(ctx, `SELECT pg_catalog.pg_stat_clear_snapshot()`); err != nil {
		t.Fatal(err)
	}
	var clients int
	if err := holder.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_stat_activity WHERE datid=(SELECT oid FROM pg_catalog.pg_database WHERE datname=pg_catalog.current_database()) AND backend_type='client backend'`).Scan(&clients); err != nil || clients != 3 {
		t.Fatalf("late client was not actually present: clients=%d error=%v", clients, err)
	}
	var unlocked bool
	if err := holder.QueryRow(ctx, `SELECT pg_catalog.pg_advisory_unlock($1)`, bodyLock).Scan(&unlocked); err != nil || !unlocked {
		t.Fatalf("could not release body barrier: unlocked=%t error=%v", unlocked, err)
	}
	select {
	case err = <-done:
		queryCollected = true
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "P0001" || pgError.Message != "schema maintenance outcome has another client" {
		t.Fatalf("trailing guard accepted the late client or failed for another reason: %v", err)
	}
	if capture.PgConn().TxStatus() != 'E' {
		t.Fatal("rejected outcome transaction was not left aborted")
	}
	if _, err := capture.Exec(ctx, `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	t.Log("private_cluster=true actual_live_holder_accepted=true body_wait_observed=true late_third_client_present=true trailing_guard_rejected=true aborted_transaction_rolled_back=true")
}
