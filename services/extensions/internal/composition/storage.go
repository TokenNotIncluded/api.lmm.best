// Package composition owns process resources, never core data or authority.
package composition

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

var databaseName = regexp.MustCompile(`^lmm_(store|support)(_[a-z0-9]+)*$`)

// StorageConfig names an independently provisioned extension database. Schema
// installation is explicit; startup never creates or migrates tables.
type StorageConfig struct {
	Component string
	URLFile   string
	Database  string
}

func (c StorageConfig) validate() error {
	if (c.Component != "store" && c.Component != "support") || c.URLFile == "" ||
		!databaseName.MatchString(c.Database) || !strings.HasPrefix(c.Database, "lmm_"+c.Component) {
		return errors.New("invalid dedicated extension storage configuration")
	}
	return nil
}

func storageURL(c StorageConfig) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	info, err := os.Lstat(c.URLFile)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("extension storage credential must be a private regular file")
	}
	f, err := os.Open(c.URLFile)
	if err != nil {
		return "", errors.New("cannot read extension storage credential")
	}
	b, readErr := io.ReadAll(io.LimitReader(f, 8193))
	closeErr := f.Close()
	if readErr != nil || closeErr != nil || len(b) > 8192 {
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
	 EXISTS(SELECT 1 FROM pg_roles WHERE rolname=current_user AND (rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication)),
	 EXISTS(SELECT 1 FROM pg_namespace WHERE nspname LIKE 'core\_%' ESCAPE '\' OR (nspname LIKE 'lmm\_%' ESCAPE '\' AND nspname <> $1)),
	 EXISTS(SELECT 1 FROM pg_database WHERE (datname LIKE '%core%' OR (datname LIKE 'lmm\_%' ESCAPE '\' AND datname <> $2)) AND has_database_privilege(current_user,oid,'CONNECT')),
	 EXISTS(SELECT 1 FROM pg_roles WHERE rolname<>current_user AND (pg_has_role(current_user,oid,'SET') OR pg_has_role(current_user,oid,'USAGE') OR pg_has_role(current_user,oid,'MEMBER')))`, "lmm_"+c.Component, c.Database).Scan(&name, &privileged, &foreignSchema, &coreConnect, &rolePaths)
	if err != nil {
		return nil, errors.New("extension storage guard unavailable")
	}
	if name != c.Database || privileged || foreignSchema || coreConnect || rolePaths {
		return nil, errors.New("extension storage isolation rejected")
	}
	tables := []string{"customers", "conversations", "messages", "commands"}
	if c.Component == "store" {
		tables = []string{"shops", "products", "variants", "orders", "commands", "stock_movements", "money_operations"}
	}
	for _, table := range tables {
		var allowed bool
		if err := db.QueryRowContext(bounded, `SELECT COALESCE(has_table_privilege(current_user,to_regclass($1),'SELECT') AND has_table_privilege(current_user,to_regclass($1),'INSERT') AND has_table_privilege(current_user,to_regclass($1),'UPDATE'),false)`, "lmm_"+c.Component+"."+table).Scan(&allowed); err != nil || !allowed {
			return nil, errors.New("extension storage schema or runtime grants unavailable")
		}
	}
	failed = false
	return db, nil
}
