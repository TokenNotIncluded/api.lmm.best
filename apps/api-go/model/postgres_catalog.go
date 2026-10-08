package model

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"gorm.io/gorm"
)

type postgresSchemaObject struct {
	model  interface{}
	table  string
	column string
}

type postgresIndexSpec struct {
	Table     string
	Name      string
	Method    string
	Unique    bool
	Primary   bool
	Valid     bool
	KeyTerms  []string
	Included  []string
	Predicate string
}

type postgresConstraintKind string

const (
	postgresPrimaryConstraint postgresConstraintKind = "primary"
	postgresUniqueConstraint  postgresConstraintKind = "unique"
	postgresForeignConstraint postgresConstraintKind = "foreign_key"
	postgresCheckConstraint   postgresConstraintKind = "check"
)

type postgresConstraintSpec struct {
	Table           string
	Names           []string
	Kind            postgresConstraintKind
	Columns         []string
	ReferenceSchema string
	ReferenceTable  string
	ReferenceCols   []string
	OnUpdate        string
	OnDelete        string
	Check           string
	Validated       bool
}

type postgresSchemaInventory struct {
	Schema      string
	Objects     []postgresSchemaObject
	Indexes     []postgresIndexSpec
	Constraints []postgresConstraintSpec
}

type postgresCatalogKey struct {
	Table string
	Name  string
}

type postgresCatalogSnapshot struct {
	Indexes     map[postgresCatalogKey]postgresIndexSpec
	Constraints map[postgresCatalogKey]postgresConstraintSpec
}

func buildPostgresSchemaInventory(db *gorm.DB, schema string, models []interface{}) (postgresSchemaInventory, error) {
	if !isSafePostgresApplicationSchema(schema) {
		return postgresSchemaInventory{}, fmt.Errorf("unsafe PostgreSQL application schema %q", schema)
	}
	inventory := postgresSchemaInventory{Schema: schema}
	for _, model := range models {
		statement := &gorm.Statement{DB: db}
		if err := statement.Parse(model); err != nil {
			return postgresSchemaInventory{}, fmt.Errorf("parse required PostgreSQL schema for %T: %w", model, err)
		}
		table := statement.Schema.Table
		inventory.Objects = append(inventory.Objects, postgresSchemaObject{model: model, table: table})
		for _, field := range statement.Schema.Fields {
			if field.DBName == "" {
				continue
			}
			inventory.Objects = append(inventory.Objects, postgresSchemaObject{model: model, table: table, column: field.DBName})
			if field.TagSettings["UNIQUE"] != "" {
				inventory.Constraints = append(inventory.Constraints, postgresConstraintSpec{
					Table: table,
					Names: pgNames(
						pgJoin(table, field.DBName, "key"),
						statement.DB.NamingStrategy.IndexName(table, field.DBName),
					),
					Kind:      postgresUniqueConstraint,
					Columns:   []string{field.DBName},
					Validated: true,
				})
			}
		}

		if len(statement.Schema.PrimaryFields) > 0 {
			columns := make([]string, 0, len(statement.Schema.PrimaryFields))
			for _, field := range statement.Schema.PrimaryFields {
				columns = append(columns, field.DBName)
			}
			inventory.Constraints = append(inventory.Constraints, postgresConstraintSpec{
				Table: table, Names: pgNames(pgJoin(table, "pkey")),
				Kind: postgresPrimaryConstraint, Columns: columns, Validated: true,
			})
		}

		indexes := statement.Schema.ParseIndexes()
		for _, name := range sortedCatalogNames(indexes) {
			index := indexes[name]
			terms := make([]string, 0, len(index.Fields))
			for _, option := range index.Fields {
				term := option.DBName
				if option.Expression != "" {
					term = normalizePostgresSQL(option.Expression)
				}
				terms = append(terms, term)
			}
			method := strings.ToLower(strings.TrimSpace(index.Type))
			if method == "" {
				method = "btree"
			}
			inventory.Indexes = append(inventory.Indexes, postgresIndexSpec{
				Table: table, Name: pgIdent(index.Name), Method: method,
				Unique: strings.EqualFold(index.Class, "UNIQUE"), Valid: true,
				KeyTerms: terms, Included: parsePostgresIncludedColumns(index.Option),
				Predicate: normalizePostgresSQL(index.Where),
			})
		}

		checks := statement.Schema.ParseCheckConstraints()
		for _, name := range sortedCatalogNames(checks) {
			check := checks[name]
			columns := []string(nil)
			if check.Field != nil && check.Field.DBName != "" {
				columns = []string{check.Field.DBName}
			}
			inventory.Constraints = append(inventory.Constraints, postgresConstraintSpec{
				Table: table, Names: pgNames(name), Kind: postgresCheckConstraint,
				Columns: columns, Check: normalizePostgresSQL(check.Constraint), Validated: true,
			})
		}

		relationshipNames := make([]string, 0, len(statement.Schema.Relationships.Relations))
		for name := range statement.Schema.Relationships.Relations {
			relationshipNames = append(relationshipNames, name)
		}
		sort.Strings(relationshipNames)
		seenConstraints := make(map[string]struct{})
		for _, name := range relationshipNames {
			relationship := statement.Schema.Relationships.Relations[name]
			if relationship.Field.IgnoreMigration {
				continue
			}
			constraint := relationship.ParseConstraint()
			if constraint == nil || constraint.Schema != statement.Schema {
				continue
			}
			name := pgIdent(constraint.Name)
			if _, exists := seenConstraints[name]; exists {
				continue
			}
			seenConstraints[name] = struct{}{}
			columns := make([]string, 0, len(constraint.ForeignKeys))
			for _, field := range constraint.ForeignKeys {
				columns = append(columns, field.DBName)
			}
			referenceColumns := make([]string, 0, len(constraint.References))
			for _, field := range constraint.References {
				referenceColumns = append(referenceColumns, field.DBName)
			}
			inventory.Constraints = append(inventory.Constraints, postgresConstraintSpec{
				Table: table, Names: pgNames(name), Kind: postgresForeignConstraint,
				Columns: columns, ReferenceSchema: schema,
				ReferenceTable: constraint.ReferenceSchema.Table, ReferenceCols: referenceColumns,
				OnUpdate:  normalizePostgresAction(constraint.OnUpdate),
				OnDelete:  normalizePostgresAction(constraint.OnDelete),
				Validated: true,
			})
		}
	}
	return inventory, nil
}

const pgIdentMax = 63

// PostgreSQL truncates unquoted identifiers to 63 bytes. GORM permits 64-byte
// generated names, so catalog verification must compare the server identity,
// not the pre-SQL spelling.
func pgIdent(name string) string {
	if len(name) > pgIdentMax {
		return name[:pgIdentMax]
	}
	return name
}

func pgJoin(parts ...string) string {
	return pgIdent(strings.Join(parts, "_"))
}

func pgNames(names ...string) []string {
	result := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = pgIdent(name)
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}

func sortedCatalogNames[T any](objects map[string]T) []string {
	names := make([]string, 0, len(objects))
	for name := range objects {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func parsePostgresIncludedColumns(option string) []string {
	upper := strings.ToUpper(option)
	start := strings.Index(upper, "INCLUDE")
	if start < 0 {
		return nil
	}
	open := strings.Index(option[start:], "(")
	if open < 0 {
		return nil
	}
	open += start
	close := strings.Index(option[open+1:], ")")
	if close < 0 {
		return nil
	}
	parts := strings.Split(option[open+1:open+1+close], ",")
	columns := make([]string, 0, len(parts))
	for _, part := range parts {
		column := strings.Trim(strings.TrimSpace(part), `"`)
		if column != "" {
			columns = append(columns, column)
		}
	}
	return columns
}

func verifyPostgresSchemaInventoryAgainstCatalog(db *gorm.DB, inventory postgresSchemaInventory) error {
	for _, object := range inventory.Objects {
		if object.column == "" {
			if !db.Migrator().HasTable(object.model) {
				return fmt.Errorf("required PostgreSQL table %s is missing", object.table)
			}
			continue
		}
		if !db.Migrator().HasColumn(object.model, object.column) {
			return fmt.Errorf("required PostgreSQL column %s.%s is missing", object.table, object.column)
		}
	}
	snapshot, err := loadPostgresCatalogSnapshot(db, inventory.Schema)
	if err != nil {
		return err
	}
	return verifyPostgresCatalogSnapshot(inventory, snapshot)
}

func verifyPostgresCatalogSnapshot(inventory postgresSchemaInventory, snapshot postgresCatalogSnapshot) error {
	for _, expected := range inventory.Indexes {
		actual, ok := snapshot.Indexes[postgresCatalogKey{Table: expected.Table, Name: expected.Name}]
		if !ok {
			return fmt.Errorf("required PostgreSQL index %s.%s is missing", expected.Table, expected.Name)
		}
		if !postgresIndexSpecsEqual(expected, actual) {
			return fmt.Errorf("required PostgreSQL index %s.%s has incompatible semantics", expected.Table, expected.Name)
		}
	}
	for _, expected := range inventory.Constraints {
		matches := make([]postgresConstraintSpec, 0, len(expected.Names))
		for _, name := range expected.Names {
			if actual, ok := snapshot.Constraints[postgresCatalogKey{Table: expected.Table, Name: name}]; ok {
				matches = append(matches, actual)
			}
		}
		if len(matches) == 0 {
			return fmt.Errorf("required PostgreSQL %s constraint %s.%s is missing", expected.Kind, expected.Table, expected.Names[0])
		}
		if len(matches) != 1 || !postgresConstraintSpecsEqual(expected, matches[0]) {
			return fmt.Errorf("required PostgreSQL %s constraint %s.%s has incompatible semantics", expected.Kind, expected.Table, expected.Names[0])
		}
	}
	return nil
}

func postgresIndexSpecsEqual(expected, actual postgresIndexSpec) bool {
	return expected.Table == actual.Table && expected.Name == actual.Name &&
		expected.Method == actual.Method && expected.Unique == actual.Unique &&
		expected.Primary == actual.Primary && expected.Valid == actual.Valid &&
		equalStrings(expected.KeyTerms, actual.KeyTerms) && equalStrings(expected.Included, actual.Included) &&
		normalizePostgresSQL(expected.Predicate) == normalizePostgresSQL(actual.Predicate)
}

func postgresConstraintSpecsEqual(expected, actual postgresConstraintSpec) bool {
	return expected.Table == actual.Table && expected.Kind == actual.Kind &&
		equalStrings(expected.Columns, actual.Columns) &&
		expected.ReferenceSchema == actual.ReferenceSchema && expected.ReferenceTable == actual.ReferenceTable &&
		equalStrings(expected.ReferenceCols, actual.ReferenceCols) &&
		normalizePostgresAction(expected.OnUpdate) == normalizePostgresAction(actual.OnUpdate) &&
		normalizePostgresAction(expected.OnDelete) == normalizePostgresAction(actual.OnDelete) &&
		normalizePostgresSQL(expected.Check) == normalizePostgresSQL(actual.Check) &&
		expected.Validated == actual.Validated
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if normalizePostgresSQL(left[index]) != normalizePostgresSQL(right[index]) {
			return false
		}
	}
	return true
}

func normalizePostgresAction(action string) string {
	action = strings.ToUpper(strings.Join(strings.Fields(action), " "))
	if action == "" {
		return "NO ACTION"
	}
	switch action {
	case "A":
		return "NO ACTION"
	case "R":
		return "RESTRICT"
	case "C":
		return "CASCADE"
	case "N":
		return "SET NULL"
	case "D":
		return "SET DEFAULT"
	default:
		return action
	}
}

func normalizePostgresSQL(expression string) string {
	expression = collapsePostgresWhitespace(strings.TrimSpace(expression))
	for hasSingleOuterParentheses(expression) {
		expression = collapsePostgresWhitespace(strings.TrimSpace(expression[1 : len(expression)-1]))
	}
	return expression
}

func collapsePostgresWhitespace(value string) string {
	var builder strings.Builder
	spacePending := false
	var quoted rune
	for _, char := range value {
		if quoted != 0 {
			builder.WriteRune(char)
			if char == quoted {
				quoted = 0
			}
			continue
		}
		if char == '\'' || char == '"' {
			if spacePending && builder.Len() > 0 {
				builder.WriteByte(' ')
			}
			spacePending = false
			quoted = char
			builder.WriteRune(char)
			continue
		}
		if unicode.IsSpace(char) {
			spacePending = true
			continue
		}
		if spacePending && builder.Len() > 0 {
			builder.WriteByte(' ')
		}
		spacePending = false
		builder.WriteRune(char)
	}
	return strings.TrimSpace(builder.String())
}

func hasSingleOuterParentheses(value string) bool {
	if len(value) < 2 || value[0] != '(' || value[len(value)-1] != ')' {
		return false
	}
	depth := 0
	var quoted byte
	for index := 0; index < len(value); index++ {
		char := value[index]
		if quoted != 0 {
			if char == quoted {
				quoted = 0
			}
			continue
		}
		if char == '\'' || char == '"' {
			quoted = char
			continue
		}
		switch char {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 && index != len(value)-1 {
				return false
			}
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

const postgresIndexesCatalogQuery = `
SELECT table_rel.relname, index_rel.relname, access_method.amname,
       index_meta.indisunique, index_meta.indisprimary, index_meta.indisvalid,
       COALESCE((
         SELECT pg_catalog.json_agg(CASE WHEN key_part.attnum OPERATOR(pg_catalog.=) 0
                              THEN pg_catalog.pg_get_indexdef(index_meta.indexrelid, key_part.ordinality::pg_catalog.int4, false)
                              ELSE key_attribute.attname END ORDER BY key_part.ordinality)::text
         FROM pg_catalog.unnest(index_meta.indkey::pg_catalog.int2[]) WITH ORDINALITY AS key_part(attnum, ordinality)
         LEFT JOIN pg_catalog.pg_attribute AS key_attribute
           ON key_attribute.attrelid OPERATOR(pg_catalog.=) table_rel.oid
          AND key_attribute.attnum OPERATOR(pg_catalog.=) key_part.attnum
         WHERE key_part.ordinality OPERATOR(pg_catalog.<=) index_meta.indnkeyatts
       ), '[]'),
       COALESCE((
         SELECT pg_catalog.json_agg(include_attribute.attname ORDER BY include_part.ordinality)::text
         FROM pg_catalog.unnest(index_meta.indkey::pg_catalog.int2[]) WITH ORDINALITY AS include_part(attnum, ordinality)
         JOIN pg_catalog.pg_attribute AS include_attribute
           ON include_attribute.attrelid OPERATOR(pg_catalog.=) table_rel.oid
          AND include_attribute.attnum OPERATOR(pg_catalog.=) include_part.attnum
         WHERE include_part.ordinality OPERATOR(pg_catalog.>) index_meta.indnkeyatts
       ), '[]'),
       COALESCE(pg_catalog.pg_get_expr(index_meta.indpred, index_meta.indrelid, true), '')
FROM pg_catalog.pg_index AS index_meta
JOIN pg_catalog.pg_class AS index_rel
  ON index_rel.oid OPERATOR(pg_catalog.=) index_meta.indexrelid
JOIN pg_catalog.pg_class AS table_rel
  ON table_rel.oid OPERATOR(pg_catalog.=) index_meta.indrelid
JOIN pg_catalog.pg_namespace AS table_namespace
  ON table_namespace.oid OPERATOR(pg_catalog.=) table_rel.relnamespace
JOIN pg_catalog.pg_am AS access_method
  ON access_method.oid OPERATOR(pg_catalog.=) index_rel.relam
WHERE table_namespace.nspname OPERATOR(pg_catalog.=) $1
  AND table_rel.relkind OPERATOR(pg_catalog.=) ANY (
    ARRAY['r'::pg_catalog."char", 'p'::pg_catalog."char"]
  )`

const postgresConstraintsCatalogQuery = `
SELECT table_rel.relname, constraint_meta.conname, constraint_meta.contype::text,
       COALESCE((
         SELECT pg_catalog.json_agg(source_attribute.attname ORDER BY source_key.ordinality)::text
         FROM pg_catalog.unnest(constraint_meta.conkey) WITH ORDINALITY AS source_key(attnum, ordinality)
         JOIN pg_catalog.pg_attribute AS source_attribute
           ON source_attribute.attrelid OPERATOR(pg_catalog.=) table_rel.oid
          AND source_attribute.attnum OPERATOR(pg_catalog.=) source_key.attnum
       ), '[]'),
       COALESCE(reference_namespace.nspname, ''), COALESCE(reference_rel.relname, ''),
       COALESCE((
         SELECT pg_catalog.json_agg(reference_attribute.attname ORDER BY reference_key.ordinality)::text
         FROM pg_catalog.unnest(constraint_meta.confkey) WITH ORDINALITY AS reference_key(attnum, ordinality)
         JOIN pg_catalog.pg_attribute AS reference_attribute
           ON reference_attribute.attrelid OPERATOR(pg_catalog.=) reference_rel.oid
          AND reference_attribute.attnum OPERATOR(pg_catalog.=) reference_key.attnum
       ), '[]'),
       constraint_meta.confupdtype::text, constraint_meta.confdeltype::text,
       COALESCE(pg_catalog.pg_get_expr(constraint_meta.conbin, constraint_meta.conrelid, true), ''),
       constraint_meta.convalidated
FROM pg_catalog.pg_constraint AS constraint_meta
JOIN pg_catalog.pg_class AS table_rel
  ON table_rel.oid OPERATOR(pg_catalog.=) constraint_meta.conrelid
JOIN pg_catalog.pg_namespace AS table_namespace
  ON table_namespace.oid OPERATOR(pg_catalog.=) table_rel.relnamespace
LEFT JOIN pg_catalog.pg_class AS reference_rel
  ON reference_rel.oid OPERATOR(pg_catalog.=) constraint_meta.confrelid
LEFT JOIN pg_catalog.pg_namespace AS reference_namespace
  ON reference_namespace.oid OPERATOR(pg_catalog.=) reference_rel.relnamespace
WHERE table_namespace.nspname OPERATOR(pg_catalog.=) $1
  AND constraint_meta.contype OPERATOR(pg_catalog.=) ANY (
    ARRAY['p'::pg_catalog."char", 'u'::pg_catalog."char",
          'f'::pg_catalog."char", 'c'::pg_catalog."char"]
  )`

func loadPostgresCatalogSnapshot(db *gorm.DB, schema string) (postgresCatalogSnapshot, error) {
	if !isSafePostgresApplicationSchema(schema) {
		return postgresCatalogSnapshot{}, fmt.Errorf("unsafe PostgreSQL application schema %q", schema)
	}
	// Keep reads on the supplied transaction/connection and honor its context.
	// db.DB() would escape a caller's read-only transaction back to the pool.
	conn := db.Statement.ConnPool
	ctx := db.Statement.Context
	snapshot := postgresCatalogSnapshot{
		Indexes:     make(map[postgresCatalogKey]postgresIndexSpec),
		Constraints: make(map[postgresCatalogKey]postgresConstraintSpec),
	}
	indexRows, err := conn.QueryContext(ctx, postgresIndexesCatalogQuery, schema)
	if err != nil {
		return postgresCatalogSnapshot{}, fmt.Errorf("query PostgreSQL index catalog: %w", err)
	}
	for indexRows.Next() {
		var spec postgresIndexSpec
		var keyTermsJSON, includedJSON string
		if err := indexRows.Scan(&spec.Table, &spec.Name, &spec.Method, &spec.Unique, &spec.Primary, &spec.Valid,
			&keyTermsJSON, &includedJSON, &spec.Predicate); err != nil {
			_ = indexRows.Close()
			return postgresCatalogSnapshot{}, fmt.Errorf("scan PostgreSQL index catalog: %w", err)
		}
		if err := json.Unmarshal([]byte(keyTermsJSON), &spec.KeyTerms); err != nil {
			_ = indexRows.Close()
			return postgresCatalogSnapshot{}, fmt.Errorf("decode PostgreSQL index keys for %s.%s: %w", spec.Table, spec.Name, err)
		}
		if err := json.Unmarshal([]byte(includedJSON), &spec.Included); err != nil {
			_ = indexRows.Close()
			return postgresCatalogSnapshot{}, fmt.Errorf("decode PostgreSQL included columns for %s.%s: %w", spec.Table, spec.Name, err)
		}
		spec.Method = strings.ToLower(spec.Method)
		spec.Predicate = normalizePostgresSQL(spec.Predicate)
		snapshot.Indexes[postgresCatalogKey{Table: spec.Table, Name: spec.Name}] = spec
	}
	if err := indexRows.Err(); err != nil {
		_ = indexRows.Close()
		return postgresCatalogSnapshot{}, fmt.Errorf("iterate PostgreSQL index catalog: %w", err)
	}
	if err := indexRows.Close(); err != nil {
		return postgresCatalogSnapshot{}, fmt.Errorf("close PostgreSQL index catalog: %w", err)
	}

	constraintRows, err := conn.QueryContext(ctx, postgresConstraintsCatalogQuery, schema)
	if err != nil {
		return postgresCatalogSnapshot{}, fmt.Errorf("query PostgreSQL constraint catalog: %w", err)
	}
	defer constraintRows.Close()
	for constraintRows.Next() {
		var spec postgresConstraintSpec
		var name, kind, columnsJSON, referenceColumnsJSON string
		if err := constraintRows.Scan(&spec.Table, &name, &kind, &columnsJSON, &spec.ReferenceSchema, &spec.ReferenceTable,
			&referenceColumnsJSON, &spec.OnUpdate, &spec.OnDelete, &spec.Check, &spec.Validated); err != nil {
			return postgresCatalogSnapshot{}, fmt.Errorf("scan PostgreSQL constraint catalog: %w", err)
		}
		spec.Names = []string{name}
		spec.Kind, err = postgresConstraintKindFromCatalog(kind)
		if err != nil {
			return postgresCatalogSnapshot{}, err
		}
		if err := json.Unmarshal([]byte(columnsJSON), &spec.Columns); err != nil {
			return postgresCatalogSnapshot{}, fmt.Errorf("decode PostgreSQL constraint columns for %s.%s: %w", spec.Table, name, err)
		}
		if err := json.Unmarshal([]byte(referenceColumnsJSON), &spec.ReferenceCols); err != nil {
			return postgresCatalogSnapshot{}, fmt.Errorf("decode PostgreSQL reference columns for %s.%s: %w", spec.Table, name, err)
		}
		spec.OnUpdate = normalizePostgresAction(spec.OnUpdate)
		spec.OnDelete = normalizePostgresAction(spec.OnDelete)
		spec.Check = normalizePostgresSQL(spec.Check)
		snapshot.Constraints[postgresCatalogKey{Table: spec.Table, Name: name}] = spec
	}
	if err := constraintRows.Err(); err != nil {
		return postgresCatalogSnapshot{}, fmt.Errorf("iterate PostgreSQL constraint catalog: %w", err)
	}
	return snapshot, nil
}

func postgresConstraintKindFromCatalog(kind string) (postgresConstraintKind, error) {
	switch kind {
	case "p":
		return postgresPrimaryConstraint, nil
	case "u":
		return postgresUniqueConstraint, nil
	case "f":
		return postgresForeignConstraint, nil
	case "c":
		return postgresCheckConstraint, nil
	default:
		return "", fmt.Errorf("unsupported PostgreSQL constraint type %q", kind)
	}
}
