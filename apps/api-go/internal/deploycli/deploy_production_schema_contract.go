package deploycli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// This is a contract for an observed PostgreSQL schema, not an API route
// revision. Business rows and sequence positions are deliberately excluded.
// Both provider binaries must still verify their compiled schema/data contract.
type productionExistingSchemaContract struct {
	Format           int    `json:"format"`
	SystemIdentifier string `json:"system_identifier"`
	Database         string `json:"database"`
	DatabaseOID      int64  `json:"database_oid"`
	Schema           string `json:"schema"`
	SchemaOID        int64  `json:"schema_oid"`
	MetadataSHA256   string `json:"metadata_sha256"`
	StartupSHA256    string `json:"startup_sha256,omitempty"`
	SignedUnitSHA256 string `json:"signed_unit_sha256,omitempty"`
}

func validateProductionExistingSchemaContract(contract *productionExistingSchemaContract) error {
	if contract == nil || contract.Format != 1 || !isDatabaseSchema(contract.Schema) ||
		contract.Database == "" || len(contract.Database) > 63 || strings.ContainsRune(contract.Database, 0) ||
		contract.DatabaseOID <= 0 || contract.SchemaOID <= 0 || !productionSHA256Pattern.MatchString(contract.MetadataSHA256) {
		return errors.New("existing PostgreSQL schema contract is incomplete")
	}
	if (contract.StartupSHA256 == "") != (contract.SignedUnitSHA256 == "") ||
		(contract.StartupSHA256 != "" && (!productionSHA256Pattern.MatchString(contract.StartupSHA256) || !productionSHA256Pattern.MatchString(contract.SignedUnitSHA256))) {
		return errors.New("existing PostgreSQL startup seal is incomplete")
	}
	identifier, err := strconv.ParseUint(contract.SystemIdentifier, 10, 64)
	if err != nil || identifier == 0 || strconv.FormatUint(identifier, 10) != contract.SystemIdentifier {
		return errors.New("existing PostgreSQL cluster identity is invalid")
	}
	return nil
}

func loadProductionExistingSchemaContract(path, expectedSHA256 string) (*productionExistingSchemaContract, error) {
	clean, err := cleanAbsoluteNonRoot(path)
	if err != nil || clean != path || !productionSHA256Pattern.MatchString(expectedSHA256) {
		return nil, errors.New("existing schema contract requires an absolute path and sealed SHA-256")
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil || resolved != clean {
		return nil, errors.New("existing schema contract has a symlink component")
	}
	before, err := os.Lstat(clean)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0o600 || before.Size() > 16384 {
		return nil, errors.New("existing schema contract must be a private regular file")
	}
	owner, links, owned := deploymentFileOwnership(before)
	if !owned || owner != uint32(os.Geteuid()) || links != 1 {
		return nil, errors.New("existing schema contract ownership or hard links are unsafe")
	}
	file, err := os.Open(clean)
	if err != nil {
		return nil, fmt.Errorf("open sealed schema contract: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, errors.New("existing schema contract changed before opening")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 16385))
	after, statErr := file.Stat()
	current, pathErr := os.Lstat(clean)
	if err != nil || statErr != nil || pathErr != nil || len(raw) > 16384 ||
		!os.SameFile(opened, current) || current.Mode()&os.ModeSymlink != 0 ||
		opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) || len(raw) != int(opened.Size()) {
		return nil, errors.New("existing schema contract changed while reading")
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != expectedSHA256 {
		return nil, errors.New("sealed existing schema contract SHA-256 mismatch")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var contract productionExistingSchemaContract
	if err := decoder.Decode(&contract); err != nil {
		return nil, fmt.Errorf("decode existing schema contract: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("existing schema contract contains trailing JSON")
	}
	if err := validateProductionExistingSchemaContract(&contract); err != nil {
		return nil, err
	}
	canonical, err := json.MarshalIndent(contract, "", "  ")
	if err != nil || !bytes.Equal(append(canonical, '\n'), raw) {
		return nil, errors.New("existing schema contract is not canonical JSON")
	}
	return &contract, nil
}

func (runtime *productionRuntime) verifyExistingSchemaContract(ctx context.Context, expected *productionExistingSchemaContract) error {
	if err := validateProductionExistingSchemaContract(expected); err != nil {
		return err
	}
	actual, err := runtime.captureExistingSchemaContract(ctx, expected.Schema)
	if err != nil {
		return err
	}
	schemaExpected := *expected
	schemaExpected.StartupSHA256, schemaExpected.SignedUnitSHA256 = "", ""
	if actual != schemaExpected {
		return errors.New("existing PostgreSQL database identity or schema metadata changed")
	}
	return nil
}

func (runtime *productionRuntime) captureExistingSchemaContract(ctx context.Context, schema string) (productionExistingSchemaContract, error) {
	if !isDatabaseSchema(schema) {
		return productionExistingSchemaContract{}, errors.New("existing schema name is unsafe")
	}
	environment, err := readPrivateRegularFile(filepath.Join(runtime.paths.ConfigDir, "lmm-api-go.env"), 1<<20)
	if err != nil {
		return productionExistingSchemaContract{}, fmt.Errorf("read schema verification environment: %w", err)
	}
	values, err := parseProductionEnvironment(environment)
	if err != nil {
		return productionExistingSchemaContract{}, err
	}
	databaseURL, childEnvironment, err := productionDatabaseCommand(values)
	if err != nil {
		return productionExistingSchemaContract{}, err
	}
	output, err := runtime.runner.Run(ctx, productionCommand{
		Name: commandPSQL, Args: []string{"-X", "-q", "-v", "ON_ERROR_STOP=1", "--no-align", "--tuples-only", "--command", existingSchemaMetadataQuery(schema), databaseURL},
		Env: childEnvironment, Sensitive: true, Timeout: 30 * time.Second, OutputLimit: 8 << 20,
	})
	if err != nil {
		return productionExistingSchemaContract{}, fmt.Errorf("inspect existing PostgreSQL schema in a read-only transaction: %w", err)
	}
	return decodeExistingSchemaSnapshot(output)
}

func decodeExistingSchemaSnapshot(output []byte) (productionExistingSchemaContract, error) {
	var snapshot struct {
		SystemIdentifier    string          `json:"system_identifier"`
		Database            string          `json:"database"`
		DatabaseOID         int64           `json:"database_oid"`
		Schema              string          `json:"schema"`
		SchemaOID           int64           `json:"schema_oid"`
		TransactionReadOnly string          `json:"transaction_read_only"`
		Metadata            json.RawMessage `json:"metadata"`
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil || decoder.Decode(&struct{}{}) != io.EOF || snapshot.TransactionReadOnly != "on" {
		return productionExistingSchemaContract{}, errors.New("existing schema inspection did not return one read-only identity snapshot")
	}
	metadataDecoder := json.NewDecoder(bytes.NewReader(snapshot.Metadata))
	metadataDecoder.UseNumber()
	var metadata map[string]any
	if err := metadataDecoder.Decode(&metadata); err != nil || len(metadata) != len(existingSchemaMetadataSections) {
		return productionExistingSchemaContract{}, errors.New("existing schema metadata is incomplete")
	}
	for _, section := range existingSchemaMetadataSections {
		value, present := metadata[section]
		if !present || value == nil {
			return productionExistingSchemaContract{}, errors.New("existing schema metadata section is missing")
		}
		if section == "namespace" {
			if object, ok := value.(map[string]any); !ok || len(object) == 0 {
				return productionExistingSchemaContract{}, errors.New("existing schema namespace metadata is missing")
			}
		} else if section == "server_version" {
			if version, ok := value.(string); !ok || version == "" {
				return productionExistingSchemaContract{}, errors.New("existing schema server version metadata is missing")
			}
		} else if _, ok := value.([]any); !ok {
			return productionExistingSchemaContract{}, errors.New("existing schema catalog section is not an array")
		}
	}
	canonical, err := json.Marshal(metadata)
	if err != nil {
		return productionExistingSchemaContract{}, errors.New("existing schema metadata cannot be canonicalized")
	}
	digest := sha256.Sum256(canonical)
	contract := productionExistingSchemaContract{Format: 1, SystemIdentifier: snapshot.SystemIdentifier, Database: snapshot.Database,
		DatabaseOID: snapshot.DatabaseOID, Schema: snapshot.Schema, SchemaOID: snapshot.SchemaOID, MetadataSHA256: hex.EncodeToString(digest[:])}
	return contract, validateProductionExistingSchemaContract(&contract)
}

var existingSchemaMetadataSections = []string{"namespace", "relations", "columns", "constraints", "indexes", "sequences", "policies", "triggers", "routines", "types", "enums", "ranges", "collations", "inheritance", "dependencies", "rewrite_rules", "partition_keys", "foreign_tables", "security_labels", "default_acls", "extensions", "server_version"}

// Every catalog reference/function is qualified. One READ ONLY transaction
// captures identity and definitions coherently; no application table is read.
// Neither statistics nor nextval/sequence state enter this schema contract.
func existingSchemaMetadataQuery(schema string) string {
	return `BEGIN READ ONLY;
SET LOCAL search_path TO pg_catalog;
WITH ns AS (SELECT oid,nspname,nspowner,nspacl FROM pg_catalog.pg_namespace WHERE nspname = '` + schema + `'),
rels AS (SELECT c.* FROM pg_catalog.pg_class c JOIN ns ON c.relnamespace = ns.oid),
types AS (SELECT t.* FROM pg_catalog.pg_type t JOIN ns ON t.typnamespace = ns.oid)
SELECT pg_catalog.jsonb_build_object(
 'system_identifier',(SELECT system_identifier::pg_catalog.text FROM pg_catalog.pg_control_system()),
 'database',pg_catalog.current_database(),
 'database_oid',(SELECT oid::pg_catalog.int8 FROM pg_catalog.pg_database WHERE datname = pg_catalog.current_database()),
 'schema',(SELECT nspname FROM ns),'schema_oid',(SELECT oid::pg_catalog.int8 FROM ns),
 'transaction_read_only',pg_catalog.current_setting('transaction_read_only'),
 'metadata',pg_catalog.jsonb_build_object(
 'namespace',(SELECT pg_catalog.jsonb_build_object('oid',oid,'name',nspname,'owner',nspowner,'acl',nspacl::pg_catalog.text) FROM ns),
 'relations',COALESCE((SELECT pg_catalog.jsonb_agg(pg_catalog.jsonb_build_object('oid',oid,'name',relname,'kind',relkind,'owner',relowner,'acl',relacl::pg_catalog.text,'options',reloptions,'persistence',relpersistence,'tablespace',reltablespace,'access_method',relam,'row_security',relrowsecurity,'force_row_security',relforcerowsecurity,'replica_identity',relreplident,'partition',relispartition,'partition_bound',pg_catalog.pg_get_expr(relpartbound,oid),'view',CASE WHEN relkind IN ('v','m') THEN pg_catalog.pg_get_viewdef(oid,true) END) ORDER BY relname,oid) FROM rels),'[]'::pg_catalog.jsonb),
 'columns',COALESCE((SELECT pg_catalog.jsonb_agg(pg_catalog.jsonb_build_object('relation',a.attrelid,'number',a.attnum,'name',a.attname,'type',a.atttypid,'type_modifier',a.atttypmod,'not_null',a.attnotnull,'identity',a.attidentity,'generated',a.attgenerated,'collation',a.attcollation,'storage',a.attstorage,'compression',a.attcompression,'acl',a.attacl::pg_catalog.text,'missing',a.attmissingval::pg_catalog.text,'default',pg_catalog.pg_get_expr(d.adbin,d.adrelid),'dropped',a.attisdropped) ORDER BY a.attrelid,a.attnum) FROM pg_catalog.pg_attribute a JOIN rels r ON a.attrelid=r.oid LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE a.attnum>0),'[]'::pg_catalog.jsonb),
 'constraints',COALESCE((SELECT pg_catalog.jsonb_agg(pg_catalog.jsonb_build_object('oid',c.oid,'name',c.conname,'relation',c.conrelid,'type',c.contypid,'kind',c.contype,'validated',c.convalidated,'deferrable',c.condeferrable,'deferred',c.condeferred,'definition',pg_catalog.pg_get_constraintdef(c.oid,true)) ORDER BY c.oid) FROM pg_catalog.pg_constraint c WHERE c.connamespace=(SELECT oid FROM ns)),'[]'::pg_catalog.jsonb),
 'indexes',COALESCE((SELECT pg_catalog.jsonb_agg(pg_catalog.jsonb_build_object('oid',i.indexrelid,'relation',i.indrelid,'unique',i.indisunique,'primary',i.indisprimary,'exclusion',i.indisexclusion,'valid',i.indisvalid,'ready',i.indisready,'live',i.indislive,'replica_identity',i.indisreplident,'definition',pg_catalog.pg_get_indexdef(i.indexrelid)) ORDER BY i.indexrelid) FROM pg_catalog.pg_index i JOIN rels r ON i.indrelid=r.oid),'[]'::pg_catalog.jsonb),
 'sequences',COALESCE((SELECT pg_catalog.jsonb_agg(pg_catalog.jsonb_build_object('oid',s.seqrelid,'type',s.seqtypid,'start',s.seqstart,'increment',s.seqincrement,'max',s.seqmax,'min',s.seqmin,'cache',s.seqcache,'cycle',s.seqcycle) ORDER BY s.seqrelid) FROM pg_catalog.pg_sequence s JOIN rels r ON s.seqrelid=r.oid),'[]'::pg_catalog.jsonb),
 'policies',COALESCE((SELECT pg_catalog.jsonb_agg(pg_catalog.jsonb_build_object('oid',p.oid,'relation',p.polrelid,'name',p.polname,'command',p.polcmd,'permissive',p.polpermissive,'roles',p.polroles,'using',pg_catalog.pg_get_expr(p.polqual,p.polrelid),'check',pg_catalog.pg_get_expr(p.polwithcheck,p.polrelid)) ORDER BY p.oid) FROM pg_catalog.pg_policy p JOIN rels r ON p.polrelid=r.oid),'[]'::pg_catalog.jsonb),
 'triggers',COALESCE((SELECT pg_catalog.jsonb_agg(pg_catalog.jsonb_build_object('oid',t.oid,'relation',t.tgrelid,'enabled',t.tgenabled,'definition',pg_catalog.pg_get_triggerdef(t.oid,true)) ORDER BY t.oid) FROM pg_catalog.pg_trigger t JOIN rels r ON t.tgrelid=r.oid),'[]'::pg_catalog.jsonb),
 'routines',COALESCE((SELECT pg_catalog.jsonb_agg(pg_catalog.to_jsonb(p) ORDER BY p.oid) FROM pg_catalog.pg_proc p WHERE p.pronamespace=(SELECT oid FROM ns)),'[]'::pg_catalog.jsonb),
 'types',COALESCE((SELECT pg_catalog.jsonb_agg(pg_catalog.to_jsonb(types) ORDER BY oid) FROM types),'[]'::pg_catalog.jsonb),
 'enums',COALESCE((SELECT pg_catalog.jsonb_agg(pg_catalog.jsonb_build_object('type',e.enumtypid,'order',e.enumsortorder,'label',e.enumlabel) ORDER BY e.enumtypid,e.enumsortorder) FROM pg_catalog.pg_enum e JOIN types t ON t.oid=e.enumtypid),'[]'::pg_catalog.jsonb),
 'ranges',COALESCE((SELECT pg_catalog.jsonb_agg(pg_catalog.to_jsonb(r) ORDER BY r.rngtypid) FROM pg_catalog.pg_range r JOIN types t ON t.oid=r.rngtypid),'[]'::pg_catalog.jsonb),
 'collations',COALESCE((SELECT pg_catalog.jsonb_agg(pg_catalog.to_jsonb(c) ORDER BY c.oid) FROM pg_catalog.pg_collation c WHERE c.collnamespace=(SELECT oid FROM ns)),'[]'::pg_catalog.jsonb),
 'inheritance',COALESCE((SELECT pg_catalog.jsonb_agg(pg_catalog.to_jsonb(i) ORDER BY i.inhrelid,i.inhseqno) FROM pg_catalog.pg_inherits i WHERE i.inhrelid IN (SELECT oid FROM rels) OR i.inhparent IN (SELECT oid FROM rels)),'[]'::pg_catalog.jsonb),
 'dependencies',COALESCE((SELECT pg_catalog.jsonb_agg(pg_catalog.to_jsonb(d) ORDER BY d.classid,d.objid,d.objsubid,d.refclassid,d.refobjid,d.refobjsubid,d.deptype) FROM pg_catalog.pg_depend d WHERE (d.classid='pg_catalog.pg_class'::pg_catalog.regclass AND d.objid IN (SELECT oid FROM rels)) OR (d.classid='pg_catalog.pg_type'::pg_catalog.regclass AND d.objid IN (SELECT oid FROM types)) OR (d.classid='pg_catalog.pg_proc'::pg_catalog.regclass AND d.objid IN (SELECT oid FROM pg_catalog.pg_proc WHERE pronamespace=(SELECT oid FROM ns)))),'[]'::pg_catalog.jsonb),
 'rewrite_rules',COALESCE((SELECT pg_catalog.jsonb_agg(pg_catalog.jsonb_build_object('oid',w.oid,'relation',w.ev_class,'enabled',w.ev_enabled,'definition',pg_catalog.pg_get_ruledef(w.oid,true)) ORDER BY w.oid) FROM pg_catalog.pg_rewrite w JOIN rels r ON r.oid=w.ev_class),'[]'::pg_catalog.jsonb),
 'partition_keys',COALESCE((SELECT pg_catalog.jsonb_agg(pg_catalog.jsonb_build_object('relation',p.partrelid,'definition',pg_catalog.pg_get_partkeydef(p.partrelid)) ORDER BY p.partrelid) FROM pg_catalog.pg_partitioned_table p JOIN rels r ON r.oid=p.partrelid),'[]'::pg_catalog.jsonb),
 'foreign_tables',COALESCE((SELECT pg_catalog.jsonb_agg(pg_catalog.jsonb_build_object('table',pg_catalog.to_jsonb(f),'server',pg_catalog.to_jsonb(s),'wrapper',pg_catalog.to_jsonb(w)) ORDER BY f.ftrelid) FROM pg_catalog.pg_foreign_table f JOIN rels r ON r.oid=f.ftrelid JOIN pg_catalog.pg_foreign_server s ON s.oid=f.ftserver JOIN pg_catalog.pg_foreign_data_wrapper w ON w.oid=s.srvfdw),'[]'::pg_catalog.jsonb),
 'security_labels',COALESCE((SELECT pg_catalog.jsonb_agg(pg_catalog.to_jsonb(s) ORDER BY s.classoid,s.objoid,s.objsubid,s.provider) FROM pg_catalog.pg_seclabel s WHERE (s.classoid='pg_catalog.pg_class'::pg_catalog.regclass AND s.objoid IN (SELECT oid FROM rels)) OR (s.classoid='pg_catalog.pg_type'::pg_catalog.regclass AND s.objoid IN (SELECT oid FROM types)) OR (s.classoid='pg_catalog.pg_namespace'::pg_catalog.regclass AND s.objoid=(SELECT oid FROM ns)) OR (s.classoid='pg_catalog.pg_proc'::pg_catalog.regclass AND s.objoid IN (SELECT oid FROM pg_catalog.pg_proc WHERE pronamespace=(SELECT oid FROM ns)))),'[]'::pg_catalog.jsonb),
 'default_acls',COALESCE((SELECT pg_catalog.jsonb_agg(pg_catalog.jsonb_build_object('role',d.defaclrole,'namespace',d.defaclnamespace,'kind',d.defaclobjtype,'acl',d.defaclacl::pg_catalog.text) ORDER BY d.oid) FROM pg_catalog.pg_default_acl d WHERE d.defaclnamespace IN (0,(SELECT oid FROM ns))),'[]'::pg_catalog.jsonb),
 'extensions',COALESCE((SELECT pg_catalog.jsonb_agg(pg_catalog.jsonb_build_object('oid',e.oid,'name',e.extname,'owner',e.extowner,'namespace',e.extnamespace,'version',e.extversion,'relocatable',e.extrelocatable,'config',e.extconfig,'condition',e.extcondition) ORDER BY e.oid) FROM pg_catalog.pg_extension e),'[]'::pg_catalog.jsonb),
 'server_version',pg_catalog.current_setting('server_version_num')));
ROLLBACK;`
}

func runProductionSchemaContract(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(DeployProgramName+" production schema-contract", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var schema string
	var sealStartup bool
	flags.BoolVar(&sealStartup, "seal-startup", false, "seal root-owned effective startup configuration and read-only readiness hooks")
	flags.StringVar(&schema, "schema", "", "existing production PostgreSQL schema to inspect read-only")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		return ExitOK
	} else if err != nil || flags.NArg() != 0 || !isDatabaseSchema(schema) {
		_, _ = fmt.Fprintln(stderr, "schema-contract requires --schema and no positional arguments")
		return ExitUsage
	}
	runtime := defaultProductionRuntime()
	if runtime.effectiveUID() != 0 {
		_, _ = fmt.Fprintln(stderr, "production schema-contract requires root")
		return ExitError
	}
	contract, err := runtime.captureExistingSchemaContract(context.Background(), schema)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "inspect production schema contract: %v\n", err)
		return ExitError
	}
	if sealStartup {
		if err := runtime.sealExistingSchemaStartup(context.Background(), &contract); err != nil {
			_, _ = fmt.Fprintf(stderr, "seal production startup: %v\n", err)
			return ExitError
		}
	}
	return writeJSONCommandResult(contract, stdout, stderr, "existing production schema contract")
}
