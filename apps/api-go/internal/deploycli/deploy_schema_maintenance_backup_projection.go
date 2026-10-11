package deploycli

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
)

func schemaMaintenanceBackupLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// Projection is fixed by the signed before-backup inventory, never caller SQL.
// It retains the real post physical/column/index identities. Its structure hash
// is the actual complete post-schema hash, not the pre-schema hash.
func schemaMaintenanceBackupProjectedSourceQuery(schema string, before productionSchemaMaintenanceBackupSource, ownerBytes []byte) (string, error) {
	if validateSchemaMaintenanceBackupSource(before) != nil || schema != before.Schema {
		return "", errors.New("schema projection before inventory is invalid")
	}
	owner, e := deploymentfence.ParseSchemaMaintenanceOwner(ownerBytes)
	if e != nil || owner.SystemIdentifier != before.SystemIdentifier || owner.Database != before.Database || owner.DatabaseOID != before.DatabaseOID || owner.Schema != before.Schema || owner.SchemaOID != before.SchemaOID {
		return "", errors.New("schema projection exact maintenance owner is invalid")
	}
	ownerKey := deploymentfence.OptionPrefix + owner.DeploymentID + ":" + owner.Host
	q, e := schemaMaintenanceBackupSourceQuery(schema)
	if e != nil {
		return "", e
	}
	literal := schemaMaintenanceBackupLiteral(schema)
	q = strings.Replace(q, strings.ReplaceAll(schemaMaintenanceBackupTablesSQL(), "SCHEMA_LITERAL", literal), strings.ReplaceAll(schemaMaintenanceBackupProjectedTablesSQL(before, ownerKey, ownerBytes), "SCHEMA_LITERAL", literal), 1)
	indexNames := []string{}
	for _, i := range before.Indexes {
		indexNames = append(indexNames, schemaMaintenanceBackupLiteral(i.Name))
	}
	indexPredicate := "false"
	if len(indexNames) > 0 {
		indexPredicate = "c.relname IN (" + strings.Join(indexNames, ",") + ")"
	}
	q = strings.Replace(q, "WHERE n.nspname="+schemaMaintenanceBackupLiteral(schema)+"),\n 'tables'", "WHERE n.nspname="+schemaMaintenanceBackupLiteral(schema)+" AND "+indexPredicate+"),\n 'tables'", 1)
	seqNames := []string{}
	for _, s := range before.Sequences {
		seqNames = append(seqNames, schemaMaintenanceBackupLiteral(s.Name))
	}
	seqPredicate := "false"
	if len(seqNames) > 0 {
		seqPredicate = "c.relname IN (" + strings.Join(seqNames, ",") + ")"
	}
	q = strings.Replace(q, "AND c.relkind='S'\n ) sequences)", "AND c.relkind='S' AND "+seqPredicate+"\n ) sequences)", 1)
	return q, nil
}
func decodeSchemaMaintenanceBackupProjectedSource(raw []byte) (productionSchemaMaintenanceBackupSource, error) {
	return decodeSchemaMaintenanceBackupSource(raw)
}
func validateSchemaMaintenanceBackupProjectedEqual(before, after productionSchemaMaintenanceBackupSource) error {
	if validateSchemaMaintenanceBackupSource(before) != nil || validateSchemaMaintenanceBackupSource(after) != nil || before.SystemIdentifier != after.SystemIdentifier || before.Database != after.Database || before.DatabaseOID != after.DatabaseOID || before.Schema != after.Schema || before.SchemaOID != after.SchemaOID || !reflect.DeepEqual(before.Roles, after.Roles) || !reflect.DeepEqual(before.Indexes, after.Indexes) || !reflect.DeepEqual(before.Sequences, after.Sequences) || len(before.Tables) != len(after.Tables) {
		return errors.New("schema projection physical/index/sequence closure changed")
	}
	for i, b := range before.Tables {
		a := after.Tables[i]
		if b.Name != a.Name || b.TableOID != a.TableOID || !reflect.DeepEqual(b.ColumnNames, a.ColumnNames) || len(b.Columns) != len(a.Columns) || b.ProjectedRows != a.Rows || b.ProjectedSHA256 != a.SHA256 || a.ProjectedRows != a.Rows || a.ProjectedSHA256 != a.SHA256 {
			return errors.New("schema projection old table rows or identity changed")
		}
		for j, bc := range b.Columns {
			ac := a.Columns[j]
			// Only this explicitly qualified additive migration changes an old column.
			if b.Name == "referral_rewards" && bc.Name == "top_up_id" && bc.NotNull && !ac.NotNull {
				bc.NotNull = false
			}
			if bc != ac {
				return errors.New("schema projection old column semantics changed")
			}
		}
	}
	return nil
}

func schemaMaintenanceBackupProjectedTablesSQL(before productionSchemaMaintenanceBackupSource, ownerKey string, ownerBytes []byte) string {
	scopes, filters := []string{}, []string{}
	for _, t := range before.Tables {
		names, cols := []string{}, []string{}
		for _, name := range t.ColumnNames {
			names = append(names, schemaMaintenanceBackupLiteral(name))
			cols = append(cols, schemaMaintenanceQuoteIdentifier(name))
		}
		scopes = append(scopes, "("+schemaMaintenanceBackupLiteral(t.Name)+","+schemaMaintenanceBackupLiteral(strings.Join(cols, ","))+")")
		filters = append(filters, "(c.relname="+schemaMaintenanceBackupLiteral(t.Name)+" AND a.attname IN ("+strings.Join(names, ",")+"))")
	}
	filter := strings.Join(filters, " OR ")
	predicate := strings.ReplaceAll("key="+schemaMaintenanceBackupLiteral(ownerKey)+" AND value="+schemaMaintenanceBackupLiteral(string(ownerBytes)), "'", "''")
	stats := `pg_catalog.query_to_xml(pg_catalog.format('SELECT count(*) AS rows, encode(sha256(convert_to(coalesce(string_agg(h, '''' ORDER BY h COLLATE "C"),''''),''UTF8'')),''hex'') AS sha256 FROM (SELECT encode(sha256(convert_to(to_jsonb(t)::text,''UTF8'')),''hex'') h FROM (SELECT %s FROM %I.%I %s) t) hashed_rows',scope.cols,n.nspname,c.relname,CASE WHEN c.relname='options' THEN 'WHERE NOT (` + predicate + `)' ELSE '' END),false,true,'')`
	return fmt.Sprintf(`(SELECT COALESCE(pg_catalog.json_agg(pg_catalog.json_build_object('name',relname,'table_oid',oid::bigint,'column_names',column_names,'columns',columns,'rows',((pg_catalog.xpath('/row/rows/text()',stats))[1]::text)::bigint,'sha256',(pg_catalog.xpath('/row/sha256/text()',stats))[1]::text,'projected_rows',((pg_catalog.xpath('/row/rows/text()',stats))[1]::text)::bigint,'projected_sha256',(pg_catalog.xpath('/row/sha256/text()',stats))[1]::text) ORDER BY relname COLLATE "C"),'[]'::json) FROM (
 SELECT c.relname,c.oid,
 (SELECT pg_catalog.json_agg(a.attname ORDER BY a.attnum) FROM pg_catalog.pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped AND (%s)) column_names,
 (SELECT pg_catalog.json_agg(pg_catalog.json_build_object('position',a.attnum,'name',a.attname,'type_name',pg_catalog.format_type(a.atttypid,a.atttypmod),'type_oid',a.atttypid::bigint,'type_mod',a.atttypmod,'collation_name',COALESCE(cn.nspname||'.'||co.collname,''),'collation_oid',a.attcollation::bigint,'not_null',a.attnotnull,'identity',a.attidentity,'generated',a.attgenerated,'default_expr',COALESCE(pg_catalog.pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY a.attnum) FROM pg_catalog.pg_attribute a LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum LEFT JOIN pg_catalog.pg_collation co ON co.oid=a.attcollation LEFT JOIN pg_catalog.pg_namespace cn ON cn.oid=co.collnamespace WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped AND (%s)) columns,
 %s stats FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN (VALUES %s) scope(name,cols) ON scope.name=c.relname WHERE n.nspname=SCHEMA_LITERAL AND c.relkind='r'
 ) tables)`, filter, filter, stats, strings.Join(scopes, ","))
}
