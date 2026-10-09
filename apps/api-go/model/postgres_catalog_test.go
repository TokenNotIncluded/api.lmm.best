package model

import (
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPostgresCatalogQueriesBindExplicitApplicationNamespace(t *testing.T) {
	for _, query := range []string{postgresIndexesCatalogQuery, postgresConstraintsCatalogQuery} {
		require.Contains(t, query, "table_namespace.nspname OPERATOR(pg_catalog.=) $1")
		require.NotContains(t, query, "'public'")
		require.NotContains(t, strings.ToLower(query), "current_schema()")
	}
}

func TestPostgresCatalogQueriesQualifySystemObjectsAndOperators(t *testing.T) {
	queries := []string{postgresIndexesCatalogQuery, postgresConstraintsCatalogQuery}
	for _, query := range queries {
		for _, required := range []string{
			"pg_catalog.pg_", "COALESCE(", "pg_catalog.json_agg(",
			"pg_catalog.unnest(", "OPERATOR(pg_catalog.=)",
		} {
			require.Contains(t, query, required)
		}
		for _, forbidden := range []string{
			"FROM pg_index", "FROM pg_constraint", "JOIN pg_class", "JOIN pg_namespace",
			"JOIN pg_attribute", "JOIN pg_am", "FROM unnest(", " THEN pg_get_",
			"SELECT json_agg(", "pg_catalog.coalesce(",
		} {
			require.NotContains(t, query, forbidden)
		}
	}
}

func TestPostgresCatalogVerificationMatchesAuthoritativeSemantics(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	inventory, err := buildPostgresSchemaInventory(db, "public", []interface{}{
		&ExternalIdentityClaim{}, &CasbinRule{}, &migrationCapabilityModel{},
	})
	require.NoError(t, err)

	requirePostgresIndexSpec(t, inventory, "external_identity_claims", "idx_external_identity_subject", true,
		[]string{"provider", "subject"})
	requirePostgresIndexSpec(t, inventory, "external_identity_claims", "idx_external_identity_user", true,
		[]string{"provider", "user_id"})
	requirePostgresIndexSpec(t, inventory, "casbin_rule", "idx_casbin_rule_unique", true,
		[]string{"ptype", "v0", "v1", "v2", "v3", "v4", "v5"})
	require.Equal(t, "public", findPostgresConstraint(t, inventory, postgresForeignConstraint).ReferenceSchema)

	snapshot := catalogSnapshotForInventory(inventory)
	snapshot.Indexes[postgresCatalogKey{Table: "unrelated", Name: "idx_unrelated"}] = postgresIndexSpec{
		Table: "unrelated", Name: "idx_unrelated", Method: "btree", Valid: true, KeyTerms: []string{"id"},
	}
	require.NoError(t, verifyPostgresCatalogSnapshot(inventory, snapshot))
}

func TestPostgresCatalogInventoryCarriesVersionedApplicationSchema(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	inventory, err := buildPostgresSchemaInventory(db, "lmm_prod_20260802", []interface{}{
		&migrationCapabilityModel{},
	})
	require.NoError(t, err)
	require.Equal(t, "lmm_prod_20260802", inventory.Schema)
	require.Equal(t, "lmm_prod_20260802", findPostgresConstraint(t, inventory, postgresForeignConstraint).ReferenceSchema)
}

func TestPostgresCatalogUsesServerIdentifierLength(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	inventory, err := buildPostgresSchemaInventory(db, "lmm_prod_20260802", []interface{}{
		&PromptConversionRef{}, &PromptConversationRef{},
	})
	require.NoError(t, err)

	const full = "idx_assistant_pre_conversation_conversion_attributions_preset_id"
	require.Len(t, full, pgIdentMax+1)
	requirePostgresIndexSpec(t, inventory,
		"assistant_pre_conversation_conversion_attributions", pgIdent(full), false, []string{"preset_id"})
	for _, index := range inventory.Indexes {
		require.LessOrEqual(t, len(index.Name), pgIdentMax, index.Name)
	}
	for _, constraint := range inventory.Constraints {
		for _, name := range constraint.Names {
			require.LessOrEqual(t, len(name), pgIdentMax, name)
		}
	}
}

func TestPostgresCatalogVerificationRejectsWrongIndexVariants(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	inventory, err := buildPostgresSchemaInventory(db, "public", []interface{}{
		&ExternalIdentityClaim{}, &CasbinRule{}, &migrationCapabilityModel{},
	})
	require.NoError(t, err)

	tests := []struct {
		name   string
		key    postgresCatalogKey
		mutate func(postgresIndexSpec) postgresIndexSpec
	}{
		{
			name: "same name wrong ordered columns",
			key:  postgresCatalogKey{Table: "external_identity_claims", Name: "idx_external_identity_user"},
			mutate: func(spec postgresIndexSpec) postgresIndexSpec {
				spec.KeyTerms = []string{"user_id", "provider"}
				return spec
			},
		},
		{
			name: "same name nonunique",
			key:  postgresCatalogKey{Table: "external_identity_claims", Name: "idx_external_identity_subject"},
			mutate: func(spec postgresIndexSpec) postgresIndexSpec {
				spec.Unique = false
				return spec
			},
		},
		{
			name: "casbin policy key incomplete",
			key:  postgresCatalogKey{Table: "casbin_rule", Name: "idx_casbin_rule_unique"},
			mutate: func(spec postgresIndexSpec) postgresIndexSpec {
				spec.KeyTerms = spec.KeyTerms[:len(spec.KeyTerms)-1]
				return spec
			},
		},
		{
			name: "partial predicate changed",
			key:  postgresCatalogKey{Table: "migration_capability_models", Name: "idx_migration_capability_name"},
			mutate: func(spec postgresIndexSpec) postgresIndexSpec {
				spec.Predicate = "category IS NULL"
				return spec
			},
		},
		{
			name: "included column removed",
			key:  postgresCatalogKey{Table: "migration_capability_models", Name: "idx_migration_capability_name"},
			mutate: func(spec postgresIndexSpec) postgresIndexSpec {
				spec.Included = nil
				return spec
			},
		},
		{
			name: "expression changed",
			key:  postgresCatalogKey{Table: "migration_capability_models", Name: "idx_migration_capability_expression"},
			mutate: func(spec postgresIndexSpec) postgresIndexSpec {
				spec.KeyTerms = []string{"upper(name)"}
				return spec
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := catalogSnapshotForInventory(inventory)
			snapshot.Indexes[test.key] = test.mutate(snapshot.Indexes[test.key])
			require.ErrorContains(t, verifyPostgresCatalogSnapshot(inventory, snapshot), "incompatible semantics")
		})
	}
}

func TestPostgresCatalogVerificationRejectsMissingAndDuplicateCriticalObjects(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	inventory, err := buildPostgresSchemaInventory(db, "public", []interface{}{&migrationCapabilityModel{}})
	require.NoError(t, err)

	snapshot := catalogSnapshotForInventory(inventory)
	delete(snapshot.Indexes, postgresCatalogKey{Table: "migration_capability_models", Name: "idx_migration_capability_name"})
	require.ErrorContains(t, verifyPostgresCatalogSnapshot(inventory, snapshot), "is missing")

	unique := findPostgresConstraint(t, inventory, postgresUniqueConstraint)
	snapshot = catalogSnapshotForInventory(inventory)
	duplicate := snapshot.Constraints[postgresCatalogKey{Table: unique.Table, Name: unique.Names[0]}]
	duplicate.Names = []string{unique.Names[1]}
	snapshot.Constraints[postgresCatalogKey{Table: unique.Table, Name: unique.Names[1]}] = duplicate
	require.ErrorContains(t, verifyPostgresCatalogSnapshot(inventory, snapshot), "incompatible semantics")

	primary := findPostgresConstraint(t, inventory, postgresPrimaryConstraint)
	snapshot = catalogSnapshotForInventory(inventory)
	delete(snapshot.Constraints, postgresCatalogKey{Table: primary.Table, Name: primary.Names[0]})
	require.ErrorContains(t, verifyPostgresCatalogSnapshot(inventory, snapshot), "is missing")
}

func TestPostgresCatalogVerificationRejectsWrongConstraintVariants(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	inventory, err := buildPostgresSchemaInventory(db, "public", []interface{}{&migrationCapabilityModel{}})
	require.NoError(t, err)

	tests := []struct {
		name   string
		kind   postgresConstraintKind
		mutate func(postgresConstraintSpec) postgresConstraintSpec
	}{
		{
			name: "primary has wrong type",
			kind: postgresPrimaryConstraint,
			mutate: func(spec postgresConstraintSpec) postgresConstraintSpec {
				spec.Kind = postgresUniqueConstraint
				return spec
			},
		},
		{
			name: "foreign key has wrong target and action",
			kind: postgresForeignConstraint,
			mutate: func(spec postgresConstraintSpec) postgresConstraintSpec {
				spec.ReferenceTable = "wrong_owners"
				spec.OnDelete = "NO ACTION"
				return spec
			},
		},
		{
			name: "foreign key references wrong schema",
			kind: postgresForeignConstraint,
			mutate: func(spec postgresConstraintSpec) postgresConstraintSpec {
				spec.ReferenceSchema = "lmm_meta"
				return spec
			},
		},
		{
			name: "check has wrong definition",
			kind: postgresCheckConstraint,
			mutate: func(spec postgresConstraintSpec) postgresConstraintSpec {
				spec.Check = "category = ''"
				return spec
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := catalogSnapshotForInventory(inventory)
			expected := findPostgresConstraint(t, inventory, test.kind)
			key := postgresCatalogKey{Table: expected.Table, Name: expected.Names[0]}
			snapshot.Constraints[key] = test.mutate(snapshot.Constraints[key])
			require.ErrorContains(t, verifyPostgresCatalogSnapshot(inventory, snapshot), "incompatible semantics")
		})
	}
}

func catalogSnapshotForInventory(inventory postgresSchemaInventory) postgresCatalogSnapshot {
	snapshot := postgresCatalogSnapshot{
		Indexes:     make(map[postgresCatalogKey]postgresIndexSpec),
		Constraints: make(map[postgresCatalogKey]postgresConstraintSpec),
	}
	for _, spec := range inventory.Indexes {
		snapshot.Indexes[postgresCatalogKey{Table: spec.Table, Name: spec.Name}] = spec
	}
	for _, spec := range inventory.Constraints {
		actual := spec
		actual.Names = []string{spec.Names[0]}
		snapshot.Constraints[postgresCatalogKey{Table: spec.Table, Name: spec.Names[0]}] = actual
	}
	return snapshot
}

func requirePostgresIndexSpec(t *testing.T, inventory postgresSchemaInventory, table, name string, unique bool, columns []string) {
	t.Helper()
	for _, spec := range inventory.Indexes {
		if spec.Table == table && spec.Name == name {
			require.Equal(t, unique, spec.Unique)
			require.Equal(t, columns, spec.KeyTerms)
			return
		}
	}
	t.Fatalf("required index %s.%s was not derived", table, name)
}

func findPostgresConstraint(t *testing.T, inventory postgresSchemaInventory, kind postgresConstraintKind) postgresConstraintSpec {
	t.Helper()
	for _, spec := range inventory.Constraints {
		if spec.Kind == kind {
			return spec
		}
	}
	t.Fatalf("required %s constraint was not derived", kind)
	return postgresConstraintSpec{}
}

// Portable DDL and the PostgreSQL rendering may differ, but both remain exact
// contracts. Declared forms must not silently outlive a model tag change.
type postgresCheckCatalogTestModel struct {
	ID      int64 `gorm:"primaryKey;autoIncrement:false;check:ck_catalog_pair,id > 0 AND id = owner_id"`
	OwnerID int64
	Forms   map[string]postgresCheckCatalogForm `gorm:"-"`
}

func (m postgresCheckCatalogTestModel) postgresCheckCatalogForms() map[string]postgresCheckCatalogForm {
	return m.Forms
}

func TestPostgresCatalogExplicitCheckFormsRemainStrict(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	form := postgresCheckCatalogForm{
		Source: "id > 0 AND id = owner_id", Expression: "id > 0 AND id = owner_id",
		Columns: []string{"id", "owner_id"},
	}
	m := &postgresCheckCatalogTestModel{Forms: map[string]postgresCheckCatalogForm{"ck_catalog_pair": form}}
	inventory, err := buildPostgresSchemaInventory(db, "public", []interface{}{m})
	require.NoError(t, err)
	check := findPostgresConstraint(t, inventory, postgresCheckConstraint)
	require.Equal(t, form.Columns, check.Columns)
	require.Equal(t, form.Expression, check.Check)
	form.Columns[0] = "changed_after_build"
	require.Equal(t, "id", check.Columns[0], "inventory must not retain a mutable alias")
	snapshot := catalogSnapshotForInventory(inventory)
	key := postgresCatalogKey{Table: check.Table, Name: check.Names[0]}
	actual := snapshot.Constraints[key]
	actual.Check = "id > 0"
	snapshot.Constraints[key] = actual
	require.ErrorContains(t, verifyPostgresCatalogSnapshot(inventory, snapshot), "incompatible semantics")
	for _, invalid := range []postgresCheckCatalogForm{
		{Source: "id > 1", Expression: "id > 1", Columns: []string{"id"}},
		{Source: form.Source, Expression: form.Expression},
		{Source: form.Source, Columns: []string{"id"}},
		{Source: form.Source, Expression: form.Expression, Columns: []string{"missing"}},
	} {
		m.Forms["ck_catalog_pair"] = invalid
		_, err := buildPostgresSchemaInventory(db, "public", []interface{}{m})
		require.Error(t, err)
	}
	m.Forms = map[string]postgresCheckCatalogForm{"missing_check": {Source: "id > 0", Expression: "id > 0", Columns: []string{"id"}}}
	_, err = buildPostgresSchemaInventory(db, "public", []interface{}{m})
	require.Error(t, err)
}
