// Package composition owns process resources, never core data or authority.
package composition

import (
	"context"
	"database/sql"
	"errors"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/coreclient"
	"net/url"
	"regexp"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

var databaseName = regexp.MustCompile(`^lmm_(store|support|assistant|payments)(_[a-z0-9]+)*$`)

// StorageConfig names an independently provisioned extension database. Schema
// installation is explicit; startup never creates or migrates tables.
type StorageConfig struct {
	Component string
	URLFile   string
	Database  string
}

func (c StorageConfig) validate() error {
	if (c.Component != "store" && c.Component != "support" && c.Component != "assistant" && c.Component != "payments") || c.URLFile == "" ||
		!databaseName.MatchString(c.Database) || !strings.HasPrefix(c.Database, "lmm_"+c.Component) {
		return errors.New("invalid dedicated extension storage configuration")
	}
	return nil
}

func storageURL(c StorageConfig) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	b, err := coreclient.ReadPrivateFile(c.URLFile, 8192)
	if err != nil {
		return "", errors.New("cannot read bounded extension storage credential")
	}
	dsn := strings.TrimSpace(string(b))
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil || u.User.Username() == "" || u.Hostname() == "" ||
		u.Fragment != "" || strings.ContainsAny(dsn, "\r\n\x00") {
		return "", errors.New("invalid extension PostgreSQL URL")
	}
	name, err := url.PathUnescape(strings.TrimPrefix(u.EscapedPath(), "/"))
	if err != nil || name != c.Database {
		return "", errors.New("extension PostgreSQL database does not match its component")
	}
	// URL query parameters cannot replace the checked database, role or host, or
	// install a caller-controlled search_path/statement configuration.
	for key, values := range u.Query() {
		if len(values) != 1 || (key != "sslmode" && key != "sslrootcert" && key != "connect_timeout" && key != "application_name") {
			return "", errors.New("unsupported extension PostgreSQL URL option")
		}
	}
	return dsn, nil
}

// openStorage is the single production pool-opening boundary. Modules receive
// only their own pool. A runtime role must not administer PostgreSQL or connect
// to a core database; a dedicated database must not contain another module.
func openStorage(ctx context.Context, c StorageConfig) (*sql.DB, error) {
	dsn, err := storageURL(c)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, errors.New("extension storage unavailable")
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(5 * time.Minute)
	failed := true
	defer func() {
		if failed {
			_ = db.Close()
		}
	}()
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var name string
	var privileged, foreignSchema, coreConnect, rolePaths bool
	err = db.QueryRowContext(bounded, `SELECT current_database(),
	 EXISTS(SELECT 1 FROM pg_roles WHERE rolname=current_user AND (rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls)) OR has_database_privilege(current_user,current_database(),'CREATE') OR has_database_privilege(current_user,current_database(),'TEMP') OR EXISTS(SELECT 1 FROM pg_namespace WHERE nspname NOT LIKE 'pg\_%' ESCAPE '\' AND nspname<>'information_schema' AND has_schema_privilege(current_user,oid,'CREATE')),
	 EXISTS(SELECT 1 FROM pg_namespace WHERE nspname LIKE 'core\_%' ESCAPE '\' OR (nspname LIKE 'lmm\_%' ESCAPE '\' AND nspname <> $1)),
	 EXISTS(SELECT 1 FROM pg_database WHERE (datname LIKE '%core%' OR (datname LIKE 'lmm\_%' ESCAPE '\' AND datname <> $2)) AND has_database_privilege(current_user,oid,'CONNECT')),
	 EXISTS(SELECT 1 FROM pg_roles WHERE rolname<>current_user AND (pg_has_role(current_user,oid,'SET') OR pg_has_role(current_user,oid,'USAGE') OR pg_has_role(current_user,oid,'MEMBER')))`, "lmm_"+c.Component, c.Database).Scan(&name, &privileged, &foreignSchema, &coreConnect, &rolePaths)
	if err != nil {
		return nil, errors.New("extension storage guard unavailable")
	}
	if name != c.Database || privileged || foreignSchema || coreConnect || rolePaths {
		return nil, errors.New("extension storage isolation rejected")
	}
	tables := storageTables(c.Component)
	for _, table := range tables {
		var allowed bool
		for _, privilege := range table.privileges {
			if err := db.QueryRowContext(bounded, `SELECT COALESCE(has_table_privilege(current_user,to_regclass($1),$2),false)`, table.name, privilege).Scan(&allowed); err != nil || !allowed {
				return nil, errors.New("extension storage schema or runtime grants unavailable")
			}
		}
	}
	if c.Component == "assistant" {
		for _, seq := range []string{"lmm_assistant.conversations_id_seq", "lmm_assistant.history_messages_id_seq"} {
			var allowed bool
			if err := db.QueryRowContext(bounded, `SELECT COALESCE(has_sequence_privilege(current_user,to_regclass($1),'USAGE'),false)`, seq).Scan(&allowed); err != nil || !allowed {
				return nil, errors.New("assistant sequence grants unavailable")
			}
		}
		var allowed bool
		if err := db.QueryRowContext(bounded, `SELECT COALESCE(has_column_privilege(current_user,to_regclass('lmm_assistant.preset_stats'),'clicks','UPDATE'),false)`).Scan(&allowed); err != nil || !allowed {
			return nil, errors.New("assistant preset grants unavailable")
		}
	}
	failed = false
	return db, nil
}

type storageTable struct {
	name       string
	privileges []string
}

func storageTables(component string) []storageTable {
	var tables []storageTable
	add := func(name string, privs ...string) { tables = append(tables, storageTable{name, privs}) }
	switch component {
	case "store":
		for _, name := range []string{"shops", "products", "variants", "orders", "commands", "stock_movements", "money_operations"} {
			add("lmm_store."+name, "SELECT", "INSERT", "UPDATE")
		}
	case "support":
		for _, name := range []string{"customers", "conversations", "messages", "commands"} {
			add("lmm_support."+name, "SELECT", "INSERT", "UPDATE")
		}
	case "assistant":
		add("lmm_assistant.contract", "SELECT")
		add("lmm_assistant.records", "SELECT", "INSERT", "UPDATE")
		add("lmm_assistant.conversations", "SELECT", "INSERT", "UPDATE")
		add("lmm_assistant.history_messages", "SELECT", "INSERT", "DELETE")
		add("lmm_assistant.turn_receipts", "SELECT", "INSERT")
		add("lmm_assistant.preset_stats", "SELECT")
	case "payments":
		add("public.payment_storage_guard", "SELECT")
		add("public.payment_orders", "SELECT", "INSERT", "UPDATE")
		add("public.payment_events", "SELECT", "INSERT", "UPDATE")
	}
	return tables
}
