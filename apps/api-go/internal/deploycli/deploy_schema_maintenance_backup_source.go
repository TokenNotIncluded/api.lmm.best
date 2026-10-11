package deploycli

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

type productionSchemaMaintenanceBackupTable struct {
	Name   string `json:"name"`
	Rows   int64  `json:"rows"`
	SHA256 string `json:"sha256"`
}
type productionSchemaMaintenanceBackupSequence struct {
	Name      string `json:"name"`
	LastValue int64  `json:"last_value"`
	Called    bool   `json:"called"`
}
type productionSchemaMaintenanceBackupSource struct {
	Format              int                                         `json:"format"`
	TransactionReadOnly string                                      `json:"transaction_read_only"`
	UnsupportedObjects  int                                         `json:"unsupported_objects"`
	SystemIdentifier    string                                      `json:"system_identifier"`
	Database            string                                      `json:"database"`
	DatabaseOID         int64                                       `json:"database_oid"`
	Schema              string                                      `json:"schema"`
	SchemaOID           int64                                       `json:"schema_oid"`
	Roles               []string                                    `json:"roles"`
	StructureSHA256     string                                      `json:"structure_sha256"`
	Tables              []productionSchemaMaintenanceBackupTable    `json:"tables"`
	Sequences           []productionSchemaMaintenanceBackupSequence `json:"sequences"`
}

func decodeSchemaMaintenanceBackupSource(raw []byte) (productionSchemaMaintenanceBackupSource, error) {
	var s productionSchemaMaintenanceBackupSource
	if len(raw) > 1<<20 || decodeControllerBackupJSON(raw, &s) != nil || validateSchemaMaintenanceBackupSource(s) != nil {
		return s, errors.New("schema backup source snapshot is incomplete")
	}
	return s, nil
}
func validateSchemaMaintenanceBackupSource(s productionSchemaMaintenanceBackupSource) error {
	if s.Format != 1 || s.TransactionReadOnly != "on" || s.UnsupportedObjects != 0 || !isDatabaseSchema(s.Schema) || s.Database == "" || s.DatabaseOID <= 0 || s.SchemaOID <= 0 || s.SystemIdentifier == "" || !productionSHA256Pattern.MatchString(s.StructureSHA256) || len(s.Tables) == 0 || len(s.Tables) > 10000 || len(s.Roles) == 0 || len(s.Roles) > 256 || !slices.IsSorted(s.Roles) {
		return errors.New("schema backup source identity or inventory is invalid")
	}
	last := ""
	for _, r := range s.Roles {
		if r == "" || len(r) > 63 || strings.ContainsAny(r, "\x00\r\n") || strings.HasPrefix(r, "pg_") || r <= last {
			return errors.New("schema backup role inventory is invalid")
		}
		last = r
	}
	last = ""
	for _, t := range s.Tables {
		if t.Name == "" || len(t.Name) > 63 || strings.ContainsAny(t.Name, "\x00\r\n") || t.Name <= last || t.Rows < 0 || !productionSHA256Pattern.MatchString(t.SHA256) {
			return errors.New("schema backup table inventory is invalid")
		}
		last = t.Name
	}
	last = ""
	for _, s := range s.Sequences {
		if s.Name == "" || len(s.Name) > 63 || strings.ContainsAny(s.Name, "\x00\r\n") || s.Name <= last {
			return errors.New("schema backup sequence inventory is invalid")
		}
		last = s.Name
	}
	return nil
}

// The caller uses one explicit primary connection and a read-only repeatable-read
// transaction. Only counts and multiset SHA256 digests leave the server; no row
// payloads, credentials, role passwords or user/order identifiers are returned.
func schemaMaintenanceBackupSourceQuery(schema string) (string, error) {
	if !isDatabaseSchema(schema) {
		return "", errors.New("schema backup source schema is invalid")
	}
	literal := "'" + strings.ReplaceAll(schema, "'", "''") + "'"
	q := `BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL search_path=pg_catalog;
SET LOCAL row_security=off;
SET LOCAL TimeZone='UTC';
SET LOCAL DateStyle='ISO, YMD';
SET LOCAL IntervalStyle='postgres';
SET LOCAL extra_float_digits=3;
SET LOCAL bytea_output='hex';
SELECT pg_catalog.json_build_object(
'format',1,'transaction_read_only',pg_catalog.current_setting('transaction_read_only'),
'unsupported_objects',((SELECT count(*) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=SCHEMA_LITERAL AND (c.relkind IN ('p','m','f') OR c.relispartition)) + (SELECT count(*) FROM pg_catalog.pg_inherits i JOIN pg_catalog.pg_class c ON c.oid=i.inhrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=SCHEMA_LITERAL) + (SELECT count(*) FROM pg_catalog.pg_type t JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname=SCHEMA_LITERAL AND t.typtype IN ('e','d','r','m'))),
'system_identifier',(SELECT system_identifier::text FROM pg_catalog.pg_control_system()),
'database',pg_catalog.current_database(),'database_oid',(SELECT oid::bigint FROM pg_catalog.pg_database WHERE datname=pg_catalog.current_database()),
'schema',SCHEMA_LITERAL,'schema_oid',(SELECT oid::bigint FROM pg_catalog.pg_namespace WHERE nspname=SCHEMA_LITERAL),
'roles',(SELECT pg_catalog.json_agg(rolname ORDER BY rolname COLLATE "C") FROM pg_catalog.pg_roles WHERE rolname NOT LIKE 'pg\_%' ESCAPE '\'),
'structure_sha256',(SELECT pg_catalog.encode(pg_catalog.sha256(pg_catalog.convert_to(pg_catalog.jsonb_build_object(
 'namespace',(SELECT pg_catalog.jsonb_build_array(n.nspname,pg_catalog.pg_get_userbyid(n.nspowner),n.nspacl::text) FROM pg_catalog.pg_namespace n WHERE n.nspname=SCHEMA_LITERAL),
 'relations',(SELECT COALESCE(pg_catalog.jsonb_agg(pg_catalog.jsonb_build_array(c.relname,c.relkind,c.relpersistence,c.relrowsecurity,c.relforcerowsecurity,pg_catalog.pg_get_userbyid(c.relowner),c.relacl::text) ORDER BY c.relname COLLATE "C"),'[]'::jsonb) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=SCHEMA_LITERAL AND c.relkind IN ('r','p','v','m','S','f')),
 'columns',(SELECT COALESCE(pg_catalog.jsonb_agg(pg_catalog.jsonb_build_array(c.relname,a.attname,a.attnum,pg_catalog.format_type(a.atttypid,a.atttypmod),a.attnotnull,a.attidentity,a.attgenerated,pg_catalog.pg_get_expr(d.adbin,d.adrelid)) ORDER BY c.relname COLLATE "C",a.attnum),'[]'::jsonb) FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE n.nspname=SCHEMA_LITERAL AND c.relkind IN ('r','p','v','m','f') AND a.attnum>0 AND NOT a.attisdropped),
 'indexes',(SELECT COALESCE(pg_catalog.jsonb_agg(pg_catalog.jsonb_build_array(c.relname,pg_catalog.pg_get_indexdef(i.indexrelid),i.indisvalid,i.indisready) ORDER BY c.relname COLLATE "C"),'[]'::jsonb) FROM pg_catalog.pg_index i JOIN pg_catalog.pg_class c ON c.oid=i.indexrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=SCHEMA_LITERAL),
 'constraints',(SELECT COALESCE(pg_catalog.jsonb_agg(pg_catalog.jsonb_build_array(c.relname,x.conname,pg_catalog.pg_get_constraintdef(x.oid),x.convalidated) ORDER BY c.relname COLLATE "C",x.conname COLLATE "C"),'[]'::jsonb) FROM pg_catalog.pg_constraint x JOIN pg_catalog.pg_class c ON c.oid=x.conrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=SCHEMA_LITERAL AND x.contype<>'n'),
 'triggers',(SELECT COALESCE(pg_catalog.jsonb_agg(pg_catalog.jsonb_build_array(c.relname,t.tgname,pg_catalog.pg_get_triggerdef(t.oid),t.tgenabled) ORDER BY c.relname COLLATE "C",t.tgname COLLATE "C"),'[]'::jsonb) FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_class c ON c.oid=t.tgrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=SCHEMA_LITERAL AND NOT t.tgisinternal),
 'sequence_options',(SELECT COALESCE(pg_catalog.jsonb_agg(pg_catalog.jsonb_build_array(c.relname,pg_catalog.format_type(s.seqtypid,NULL),s.seqstart,s.seqincrement,s.seqmin,s.seqmax,s.seqcache,s.seqcycle) ORDER BY c.relname COLLATE "C"),'[]'::jsonb) FROM pg_catalog.pg_sequence s JOIN pg_catalog.pg_class c ON c.oid=s.seqrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=SCHEMA_LITERAL),
 'sequence_ownership',(SELECT COALESCE(pg_catalog.jsonb_agg(pg_catalog.jsonb_build_array(s.relname,t.relname,a.attname,d.deptype) ORDER BY s.relname COLLATE "C"),'[]'::jsonb) FROM pg_catalog.pg_depend d JOIN pg_catalog.pg_class s ON s.oid=d.objid JOIN pg_catalog.pg_namespace n ON n.oid=s.relnamespace JOIN pg_catalog.pg_class t ON t.oid=d.refobjid JOIN pg_catalog.pg_attribute a ON a.attrelid=t.oid AND a.attnum=d.refobjsubid WHERE n.nspname=SCHEMA_LITERAL AND s.relkind='S' AND d.classid='pg_class'::regclass AND d.refclassid='pg_class'::regclass AND d.deptype IN ('a','i')),
 'policies',(SELECT COALESCE(pg_catalog.jsonb_agg(pg_catalog.jsonb_build_array(c.relname,p.polname,p.polcmd,p.polpermissive,(SELECT pg_catalog.jsonb_agg(CASE WHEN r=0 THEN 'PUBLIC' ELSE pg_catalog.pg_get_userbyid(r) END ORDER BY CASE WHEN r=0 THEN 'PUBLIC' ELSE pg_catalog.pg_get_userbyid(r) END COLLATE "C") FROM unnest(p.polroles) r),pg_catalog.pg_get_expr(p.polqual,p.polrelid),pg_catalog.pg_get_expr(p.polwithcheck,p.polrelid)) ORDER BY c.relname COLLATE "C",p.polname COLLATE "C"),'[]'::jsonb) FROM pg_catalog.pg_policy p JOIN pg_catalog.pg_class c ON c.oid=p.polrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=SCHEMA_LITERAL),
 'views',(SELECT COALESCE(pg_catalog.jsonb_agg(pg_catalog.jsonb_build_array(c.relname,pg_catalog.pg_get_viewdef(c.oid)) ORDER BY c.relname COLLATE "C"),'[]'::jsonb) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=SCHEMA_LITERAL AND c.relkind IN ('v','m')),
 'routines',(SELECT COALESCE(pg_catalog.jsonb_agg(pg_catalog.jsonb_build_array(p.proname,pg_catalog.pg_get_function_identity_arguments(p.oid),pg_catalog.pg_get_functiondef(p.oid),pg_catalog.pg_get_userbyid(p.proowner),p.proacl::text) ORDER BY p.proname COLLATE "C",pg_catalog.pg_get_function_identity_arguments(p.oid) COLLATE "C"),'[]'::jsonb) FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=SCHEMA_LITERAL AND p.prokind IN ('f','p'))
 )::text,'UTF8')),'hex')),
 'tables',(SELECT COALESCE(pg_catalog.json_agg(pg_catalog.json_build_object('name',relname,'rows',((pg_catalog.xpath('/row/rows/text()',stats))[1]::text)::bigint,'sha256',(pg_catalog.xpath('/row/sha256/text()',stats))[1]::text) ORDER BY relname COLLATE "C"),'[]'::json) FROM (
 SELECT c.relname,pg_catalog.query_to_xml(pg_catalog.format('SELECT count(*) AS rows, encode(sha256(convert_to(coalesce(string_agg(h, '''' ORDER BY h COLLATE "C"),''''),''UTF8'')),''hex'') AS sha256 FROM (SELECT encode(sha256(convert_to(to_jsonb(t)::text,''UTF8'')),''hex'') h FROM %I.%I t) hashed_rows',n.nspname,c.relname),false,true,'') stats FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=SCHEMA_LITERAL AND c.relkind IN ('r','p')
 ) tables),
 'sequences',(SELECT COALESCE(pg_catalog.json_agg(pg_catalog.json_build_object('name',relname,'last_value',((pg_catalog.xpath('/row/last_value/text()',stats))[1]::text)::bigint,'called',((pg_catalog.xpath('/row/is_called/text()',stats))[1]::text)::boolean) ORDER BY relname COLLATE "C"),'[]'::json) FROM (
 SELECT c.relname,pg_catalog.query_to_xml(pg_catalog.format('SELECT last_value,is_called FROM %I.%I',n.nspname,c.relname),false,true,'') stats FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=SCHEMA_LITERAL AND c.relkind='S'
 ) sequences)
);
COMMIT;`
	return strings.ReplaceAll(q, "SCHEMA_LITERAL", literal), nil
}

func schemaMaintenanceBackupLogicalEqual(a, b productionSchemaMaintenanceBackupSource) bool {
	// Physical OIDs/cluster identifiers necessarily change during a logical restore.
	// Owner/ACL names, definitions, every table's full row multiset and sequences do not.
	a.SystemIdentifier, b.SystemIdentifier = "", ""
	a.Database, b.Database = "", ""
	a.DatabaseOID, b.DatabaseOID = 0, 0
	a.SchemaOID, b.SchemaOID = 0, 0
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}
func schemaMaintenanceBackupStopBindings(plan productionSchemaMaintenancePlan, preSHA string, stopped []productionSchemaMaintenanceHostCapture) ([]productionSchemaMaintenanceBackupStop, error) {
	if len(stopped) != 2 || len(plan.Origins) != 2 || !productionSHA256Pattern.MatchString(preSHA) {
		return nil, errors.New("schema backup requires both native stopped origins")
	}
	result := make([]productionSchemaMaintenanceBackupStop, 0, 2)
	for i, c := range stopped {
		if c.Format != 1 || c.TransitionID != plan.TransitionID || c.PlanSHA256 != preSHA || c.Host != plan.Origins[i].Host || c.Phase != "WRITER_STOPPED" || c.CapsuleSHA256 != plan.Origins[i].OldCapsule.SHA256 || c.ProviderSHA256 != plan.Origins[i].Recovery.Candidate.PayloadSHA256 || !productionSHA256Pattern.MatchString(c.OriginalAdmissionSHA256) || c.PID <= 1 || !existingSchemaInvocationPattern.MatchString(c.InvocationID) || c.StopStartedUTC.IsZero() || !productionSHA256Pattern.MatchString(c.ShutdownJournalSHA256) || !productionSHA256Pattern.MatchString(c.ClosedAdmissionSHA256) {
			return nil, errors.New("schema backup stopped generation is incomplete")
		}
		raw, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			return nil, err
		}
		result = append(result, productionSchemaMaintenanceBackupStop{plan.Origins[i].Alias, c.Host, controllerBackupDigest(append(raw, '\n'))})
	}
	return result, nil
}
func schemaMaintenanceQuoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
func schemaMaintenanceBackupSourceIdentity(s productionSchemaMaintenanceBackupSource, c productionExistingSchemaContract) error {
	if validateSchemaMaintenanceBackupSource(s) != nil || s.SystemIdentifier != c.SystemIdentifier || s.Database != c.Database || s.DatabaseOID != c.DatabaseOID || s.Schema != c.Schema || s.SchemaOID != c.SchemaOID {
		return fmt.Errorf("schema backup snapshot differs from immutable primary PostgreSQL identity")
	}
	return nil
}
