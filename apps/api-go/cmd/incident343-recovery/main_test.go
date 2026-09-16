//go:build linux

package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func incidentTestDB(t *testing.T) (*gorm.DB, string, string) {
	t.Helper()
	dsn := os.Getenv("LMM_INCIDENT343_TEST_DSN")
	if dsn == "" {
		t.Skip("requires explicitly configured disposable loopback PostgreSQL")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || u.User == nil || !strings.HasPrefix(u.User.Username(), "lmm_test_") || !strings.HasPrefix(strings.TrimPrefix(u.Path, "/"), "lmm_test_") {
		t.Fatal("test DSN must be disposable loopback lmm_test_ role/database")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	schema := "lmm_test_incident343_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := db.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Error(err)
		}
		_ = pool.Close()
	})
	return db, dsn, schema
}

func targetRelationCount(t *testing.T, db *gorm.DB, schema string) int64 {
	t.Helper()
	var n int64
	if err := db.Raw("SELECT count(*) FROM pg_catalog.pg_tables WHERE schemaname=? AND tablename IN ('red_packets','red_packet_items','red_packet_claims')", schema).Scan(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIncident343PostgresCreatesOnlyAbsentTables(t *testing.T) {
	db, dsn, schema := incidentTestDB(t)
	for _, q := range []string{"CREATE TABLE " + schema + ".untouched (id integer primary key, value text)", "INSERT INTO " + schema + ".untouched VALUES (1,'unchanged')"} {
		if err := db.Exec(q).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := createAbsentRedPacketSchema(context.Background(), dsn, schema); err != nil {
		t.Fatal(err)
	}
	if n := targetRelationCount(t, db, schema); n != 3 {
		t.Fatalf("tables=%d", n)
	}
	var value string
	if err := db.Raw("SELECT value FROM " + schema + ".untouched WHERE id=1").Scan(&value).Error; err != nil || value != "unchanged" {
		t.Fatal("existing data changed", err)
	}
	var missing int64
	if err := db.Raw("SELECT count(*) FROM information_schema.columns WHERE table_schema=? AND table_name='red_packets' AND column_name IN ('id','slug','description','enabled','created_by')", schema).Scan(&missing).Error; err != nil || missing != 5 {
		t.Fatal("schema columns mismatch", missing, err)
	}
	var indexes int64
	if err := db.Raw("SELECT count(*) FROM pg_catalog.pg_indexes WHERE schemaname=? AND indexname IN ('idx_red_packet_source','idx_red_packet_user_claim')", schema).Scan(&indexes).Error; err != nil || indexes != 2 {
		t.Fatal("unique indexes missing", indexes, err)
	}
	if err := createAbsentRedPacketSchema(context.Background(), dsn, schema); err == nil {
		t.Fatal("replay must not modify existing tables")
	}
}

func TestIncident343PostgresRejectsPartialSchema(t *testing.T) {
	db, dsn, schema := incidentTestDB(t)
	if err := db.Exec("CREATE TABLE " + schema + ".red_packets (sentinel text)").Error; err != nil {
		t.Fatal(err)
	}
	if err := createAbsentRedPacketSchema(context.Background(), dsn, schema); err == nil {
		t.Fatal("existing table accepted")
	}
	if n := targetRelationCount(t, db, schema); n != 1 {
		t.Fatal("partial schema was modified", n)
	}
}

func TestIncident343PostgresAtomicRollbackOnIndexCollision(t *testing.T) {
	db, dsn, schema := incidentTestDB(t)
	if err := db.Exec("CREATE TABLE " + schema + ".idx_red_packet_source (sentinel text)").Error; err != nil {
		t.Fatal(err)
	}
	if err := createAbsentRedPacketSchema(context.Background(), dsn, schema); err == nil {
		t.Fatal("expected index collision")
	}
	if n := targetRelationCount(t, db, schema); n != 0 {
		t.Fatal("partial DDL escaped transaction", n)
	}
}

func TestIncident343PostgresHonorsMigrationLock(t *testing.T) {
	db, dsn, schema := incidentTestDB(t)
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), "SELECT pg_advisory_lock($1)", model.MigrationAdvisoryLockKey); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", model.MigrationAdvisoryLockKey)
	}()
	if err := createAbsentRedPacketSchema(context.Background(), dsn, schema); err == nil || !strings.Contains(err.Error(), "advisory lock") {
		t.Fatal("lock was ignored", err)
	}
	if n := targetRelationCount(t, db, schema); n != 0 {
		t.Fatal("tables created without lock", n)
	}
}

func TestIncident343InvalidSchemaRejectedBeforeConnection(t *testing.T) {
	for _, schema := range []string{"", "public;DROP SCHEMA public", "a.b", strings.Repeat("x", 64)} {
		if err := createAbsentRedPacketSchema(context.Background(), "", schema); err == nil {
			t.Fatal(fmt.Sprintf("accepted invalid schema %q", schema))
		}
	}
}
