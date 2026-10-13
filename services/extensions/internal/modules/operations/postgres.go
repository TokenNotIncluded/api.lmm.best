// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package operations

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"time"

	"github.com/lib/pq"
)

//go:embed schema.sql
var schema string

// DatabaseConfig opens a new operations-owned pool. Never pass an existing core
// or another extension pool. Initialize is deliberately separate from Open.
type DatabaseConfig struct{ DSN string }

func Open(ctx context.Context, c DatabaseConfig) (*Releases, error) {
	if c.DSN == "" {
		return nil, ErrInvalid
	}
	db, err := sql.Open("postgres", c.DSN)
	if err != nil {
		return nil, ErrUnavailable
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err = Check(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return &Releases{db}, nil
}
func (s *Releases) Close() error { return s.db.Close() }

// Catalog fingerprint covers the installed schema, including ownership, ACLs,
// RLS flags/policies, columns, constraints, indexes and sequence bounds. Runtime
// cannot write the owner-installed contract row.
const fingerprintSQL = `SELECT md5(string_agg(item,E'\n' ORDER BY item)) FROM (
 SELECT 'relation:'||c.relname||':'||c.relkind::text||':'||c.relowner||':'||c.relrowsecurity||':'||c.relforcerowsecurity AS item FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='lmm_operations'
 UNION ALL SELECT 'schema:'||n.nspowner FROM pg_namespace n WHERE n.nspname='lmm_operations'
 UNION ALL SELECT 'schema-acl:'||a.grantor||':'||a.grantee||':'||a.privilege_type||':'||a.is_grantable FROM pg_namespace n CROSS JOIN LATERAL aclexplode(coalesce(n.nspacl,acldefault('n',n.nspowner))) a WHERE n.nspname='lmm_operations'
 UNION ALL SELECT 'relation-acl:'||c.relname||':'||a.grantor||':'||a.grantee||':'||a.privilege_type||':'||a.is_grantable FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace CROSS JOIN LATERAL aclexplode(coalesce(c.relacl,acldefault(CASE WHEN c.relkind='S' THEN 'S'::"char" ELSE 'r'::"char" END,c.relowner))) a WHERE n.nspname='lmm_operations'
 UNION ALL SELECT 'policy:'||c.relname||':'||p.polname||':'||p.polcmd::text||':'||p.polpermissive||':'||p.polroles::text||':'||coalesce(pg_get_expr(p.polqual,p.polrelid),'')||':'||coalesce(pg_get_expr(p.polwithcheck,p.polrelid),'') FROM pg_policy p JOIN pg_class c ON c.oid=p.polrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='lmm_operations'
 UNION ALL SELECT 'column:'||c.relname||':'||a.attnum||':'||a.attname||':'||format_type(a.atttypid,a.atttypmod)||':'||a.attnotnull||':'||coalesce(pg_get_expr(d.adbin,d.adrelid),'') FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE n.nspname='lmm_operations' AND a.attnum>0 AND NOT a.attisdropped
 UNION ALL SELECT 'constraint:'||c.relname||':'||pg_get_constraintdef(k.oid) FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='lmm_operations'
 UNION ALL SELECT 'index:'||indexdef FROM pg_indexes WHERE schemaname='lmm_operations'
 UNION ALL SELECT 'sequence:'||sequencename||':'||start_value||':'||min_value||':'||max_value||':'||increment_by||':'||cycle FROM pg_sequences WHERE schemaname='lmm_operations'
 UNION ALL SELECT 'function:'||p.proname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='lmm_operations'
 UNION ALL SELECT 'trigger:'||t.tgname FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='lmm_operations' AND NOT t.tgisinternal
) objects`

const isolatedSQL = `SELECT NOT EXISTS (
 SELECT 1 FROM pg_namespace WHERE nspname NOT IN ('public','lmm_operations','information_schema') AND left(nspname,3)<>'pg_'
) AND NOT EXISTS (
 SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public'
) AND NOT EXISTS (
 SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public'
)`

// Initialize installs ONLY into an empty, isolated database. It grants the exact
// runtime allowlist to an existing non-owner role. This is an offline owner call,
// not a migration and never called by Open or request handlers.
func Initialize(ctx context.Context, owner *sql.DB, runtimeRole string) error {
	if owner == nil || runtimeRole == "" {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := owner.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var safe bool
	if err = tx.QueryRowContext(ctx, isolatedSQL+` AND NOT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='lmm_operations')`).Scan(&safe); err != nil {
		return err
	}
	if !safe {
		return fmt.Errorf("operations initialization requires fresh isolated database")
	}
	var roleSafe bool
	if err = tx.QueryRowContext(ctx, `SELECT NOT (rolsuper OR rolcreaterole OR rolcreatedb OR rolreplication OR rolbypassrls) AND rolname<>current_user FROM pg_roles WHERE rolname=$1`, runtimeRole).Scan(&roleSafe); err != nil {
		return err
	}
	if !roleSafe {
		return fmt.Errorf("operations runtime role must be restricted and distinct from owner")
	}
	if _, err = tx.ExecContext(ctx, schema); err != nil {
		return err
	}
	var database string
	if err = tx.QueryRowContext(ctx, "SELECT current_database()").Scan(&database); err != nil {
		return err
	}
	role := pq.QuoteIdentifier(runtimeRole)
	grants := `REVOKE ALL ON DATABASE ` + pq.QuoteIdentifier(database) + ` FROM PUBLIC; REVOKE ALL ON SCHEMA public FROM PUBLIC;
 GRANT CONNECT ON DATABASE ` + pq.QuoteIdentifier(database) + ` TO ` + role + `;
 GRANT USAGE ON SCHEMA lmm_operations TO ` + role + `;
 GRANT SELECT ON lmm_operations.contract TO ` + role + `;
 GRANT SELECT,INSERT ON lmm_operations.publications,lmm_operations.reads TO ` + role + `;
 GRANT USAGE,SELECT ON SEQUENCE lmm_operations.publication_id TO ` + role + `;`
	if _, err = tx.ExecContext(ctx, grants); err != nil {
		return err
	}
	// Names do not exempt CREATE authority, including actual PostgreSQL system
	// schemas. Reject explicit pre-existing runtime grants rather than preserving
	// DDL authority after an otherwise successful installation.
	if err = tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM pg_namespace WHERE has_schema_privilege($1,oid,'CREATE'))`, runtimeRole).Scan(&safe); err != nil {
		return err
	}
	if !safe {
		return fmt.Errorf("operations runtime must not have CREATE on any schema")
	}
	var fingerprint string
	if err = tx.QueryRowContext(ctx, fingerprintSQL).Scan(&fingerprint); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE lmm_operations.contract SET fingerprint=$1", fingerprint); err != nil {
		return err
	}
	return tx.Commit()
}

// Check verifies installed schema, isolation and effective runtime privileges;
// it performs no DDL, even for a missing/incompatible schema.
func Check(ctx context.Context, runtime *sql.DB) error {
	if runtime == nil {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var safe bool
	err := runtime.QueryRowContext(ctx, isolatedSQL+` AND NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=current_user AND (rolsuper OR rolcreaterole OR rolcreatedb OR rolreplication OR rolbypassrls))
 AND NOT EXISTS(SELECT 1 FROM pg_auth_members WHERE member=(SELECT oid FROM pg_roles WHERE rolname=current_user))
 AND NOT has_database_privilege(current_database(),'CREATE') AND NOT has_database_privilege(current_database(),'TEMP')
 AND NOT has_schema_privilege('public','CREATE') AND NOT has_schema_privilege('lmm_operations','CREATE') AND has_schema_privilege('lmm_operations','USAGE')
 AND NOT EXISTS(SELECT 1 FROM pg_namespace WHERE has_schema_privilege(oid,'CREATE'))
 AND NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='lmm_operations' AND (c.relrowsecurity OR c.relforcerowsecurity))
 AND NOT EXISTS(SELECT 1 FROM pg_policy p JOIN pg_class c ON c.oid=p.polrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='lmm_operations')`).Scan(&safe)
	if err != nil || !safe {
		return ErrUnavailable
	}
	var installed, actual string
	if runtime.QueryRowContext(ctx, "SELECT fingerprint FROM lmm_operations.contract WHERE version=1").Scan(&installed) != nil || runtime.QueryRowContext(ctx, fingerprintSQL).Scan(&actual) != nil || installed != actual || len(actual) != 32 {
		return ErrUnavailable
	}
	for _, table := range []string{"contract", "publications", "reads"} {
		var privileges bool
		err = runtime.QueryRowContext(ctx, `SELECT has_table_privilege($1,'SELECT') AND has_table_privilege($1,'INSERT')=$2 AND NOT has_table_privilege($1,'UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')`, "lmm_operations."+table, table != "contract").Scan(&privileges)
		if err != nil || !privileges {
			return ErrUnavailable
		}
	}
	// Inspect ACLs too: future privileges (e.g. MAINTAIN) and column-level
	// grants must not silently widen the runtime allowlist.
	if runtime.QueryRowContext(ctx, `SELECT NOT EXISTS (
 SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace CROSS JOIN LATERAL aclexplode(coalesce(c.relacl,acldefault(CASE WHEN c.relkind='S' THEN 'S'::"char" ELSE 'r'::"char" END,c.relowner))) a
 WHERE n.nspname='lmm_operations' AND a.grantee IN (0,(SELECT oid FROM pg_roles WHERE rolname=current_user))
 AND (a.is_grantable OR a.privilege_type NOT IN ('SELECT',CASE WHEN c.relname IN ('publications','reads') THEN 'INSERT' WHEN c.relkind='S' THEN 'USAGE' ELSE 'SELECT' END))
 ) AND NOT EXISTS (
 SELECT 1 FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace CROSS JOIN LATERAL aclexplode(a.attacl) p
 WHERE n.nspname='lmm_operations' AND p.grantee IN (0,(SELECT oid FROM pg_roles WHERE rolname=current_user))
 )`).Scan(&safe) != nil || !safe {
		return ErrUnavailable
	}
	if runtime.QueryRowContext(ctx, `SELECT has_sequence_privilege('lmm_operations.publication_id','USAGE') AND has_sequence_privilege('lmm_operations.publication_id','SELECT') AND NOT has_sequence_privilege('lmm_operations.publication_id','UPDATE')`).Scan(&safe) != nil || !safe {
		return ErrUnavailable
	}
	return nil
}
