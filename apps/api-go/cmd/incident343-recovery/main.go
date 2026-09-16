//go:build linux

// A one-incident helper, not an alternative release or HTTP server.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/appcli"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	os.Exit(appcli.RunIncident343Recovery(os.Args[1:], os.Stdout, os.Stderr, createAbsentRedPacketSchema))
}

func createAbsentRedPacketSchema(ctx context.Context, dsn, schema string) error {
	if !regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,62}$`).MatchString(schema) {
		return errors.New("invalid schema identifier")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), DisableAutomaticPing: true, DisableForeignKeyConstraintWhenMigrating: true})
	if err != nil {
		return errors.New("open primary PostgreSQL for schema repair")
	}
	pool, err := db.DB()
	if err != nil {
		return errors.New("open schema repair connection pool")
	}
	defer pool.Close()
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	if err := pool.PingContext(ctx); err != nil {
		return errors.New("connect to primary PostgreSQL within recovery deadline")
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, statement := range []string{"SET LOCAL lock_timeout='5s'", "SET LOCAL statement_timeout='30s'"} {
			if err := tx.Exec(statement).Error; err != nil {
				return errors.New("set bounded schema repair transaction")
			}
		}
		var locked bool
		if err := tx.Raw("SELECT pg_catalog.pg_try_advisory_xact_lock(?)", model.MigrationAdvisoryLockKey).Scan(&locked).Error; err != nil || !locked {
			return errors.New("startup migration advisory lock is not exclusively available")
		}
		if err := tx.Exec("SELECT pg_catalog.set_config('search_path',pg_catalog.quote_ident(?),true)", schema).Error; err != nil {
			return errors.New("set recorded schema search path")
		}
		var actual string
		if err := tx.Raw("SELECT pg_catalog.current_schema()").Scan(&actual).Error; err != nil || actual != schema {
			return errors.New("recorded database schema does not exist")
		}
		var count int64
		tables := []string{"red_packets", "red_packet_items", "red_packet_claims"}
		if err := tx.Raw("SELECT count(*) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=? AND c.relname IN ?", schema, tables).Scan(&count).Error; err != nil {
			return errors.New("read target relation catalog")
		}
		if count != 0 {
			return errors.New("at least one target relation exists; no existing relation may be changed")
		}
		// CreateTable, NOT AutoMigrate: never alter an existing business table.
		for _, entry := range []struct {
			name  string
			value any
		}{
			{"red_packets", &model.RedPacket{}}, {"red_packet_items", &model.RedPacketItem{}}, {"red_packet_claims", &model.RedPacketClaim{}},
		} {
			if err := tx.Migrator().CreateTable(entry.value); err != nil {
				return fmt.Errorf("create %s with reviewed model: transaction will roll back", entry.name)
			}
			// PostgreSQL's GORM driver uses CREATE INDEX IF NOT EXISTS. A name
			// collision must not silently leave a new table without its indexes.
			statement := &gorm.Statement{DB: tx}
			if err := statement.Parse(entry.value); err != nil {
				return errors.New("parse reviewed index inventory")
			}
			for _, index := range statement.Schema.ParseIndexes() {
				columns := make([]string, 0, len(index.Fields))
				for _, field := range index.Fields {
					columns = append(columns, field.DBName)
				}
				var matches int64
				query := `SELECT count(*) FROM pg_catalog.pg_index x
JOIN pg_catalog.pg_class i ON i.oid=x.indexrelid
JOIN pg_catalog.pg_class t ON t.oid=x.indrelid
JOIN pg_catalog.pg_namespace n ON n.oid=t.relnamespace
WHERE n.nspname=? AND t.relname=? AND i.relname=?
AND x.indisvalid AND x.indisready AND x.indisunique=?
AND x.indpred IS NULL AND x.indexprs IS NULL
AND (SELECT string_agg(a.attname,',' ORDER BY k.ord)
 FROM unnest(x.indkey) WITH ORDINALITY k(attnum,ord)
 JOIN pg_catalog.pg_attribute a ON a.attrelid=t.oid AND a.attnum=k.attnum
 WHERE k.ord<=x.indnkeyatts)=?`
				if err := tx.Raw(query, schema, entry.name, index.Name, index.Class == "UNIQUE", strings.Join(columns, ",")).Scan(&matches).Error; err != nil || matches != 1 {
					return errors.New("created table index identity mismatch; transaction will roll back")
				}
			}
		}
		return nil
	})
}
