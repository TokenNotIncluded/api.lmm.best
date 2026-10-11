package model

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Accept no external DSN: the test owns its entire no-TCP cluster and socket.
func TestPostgresReadAliasActualPrivateCluster(t *testing.T) {
	if os.Getenv("LMM_TEST_POSTGRES") != "1" {
		t.Skip("set LMM_TEST_POSTGRES=1 for an owned local PostgreSQL cluster")
	}
	if os.Geteuid() == 0 {
		t.Fatal("initdb fixture must run as an unprivileged user")
	}
	for _, name := range []string{"initdb", "pg_ctl"} {
		_, err := exec.LookPath(name)
		require.NoError(t, err)
	}
	root, err := os.MkdirTemp("", "lmm-log-alias-pg-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(root)) })
	socket := filepath.Join(root, "socket")
	require.NoError(t, os.Mkdir(socket, 0700))
	data := filepath.Join(root, "data")
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "LANG=C", "LC_ALL=C"}
	run := func(name string, args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, name, args...)
		command.Env = env
		output, err := command.CombinedOutput()
		require.NoError(t, err, "%s: %s", name, output)
	}
	run("initdb", "-D", data, "--auth=trust", "--no-locale", "--encoding=UTF8", "--username=lmm_log_alias_test")
	run("pg_ctl", "-D", data, "-l", filepath.Join(root, "postgres.log"), "-w", "-t", "20", "-o", "-h '' -k "+socket, "start")
	t.Cleanup(func() { run("pg_ctl", "-D", data, "-m", "fast", "-w", "-t", "20", "stop") })
	dsn := fmt.Sprintf("host=%s user=lmm_log_alias_test dbname=postgres sslmode=disable search_path=app", socket)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	require.NoError(t, db.Exec("CREATE SCHEMA app").Error)
	var listens, version string
	require.NoError(t, db.Raw("SHOW listen_addresses").Scan(&listens).Error)
	require.Empty(t, listens)
	require.NoError(t, db.Raw("SHOW server_version").Scan(&version).Error)
	require.NoError(t, db.AutoMigrate(&Log{}))
	inventory, err := buildPostgresSchemaInventory(db, "app", []interface{}{&Log{}})
	require.NoError(t, err)
	require.NoError(t, verifyPostgresSchemaInventory(db, inventory))
	for _, name := range []string{"quota_stored_valid", "quota_usd", "quota_money_available", "quota_money_basis"} {
		require.False(t, db.Migrator().HasColumn(&Log{}, name), name)
	}
	require.NoError(t, db.Exec(`INSERT INTO app.logs (id,type,quota,other) VALUES (1,2,250000,'{"pricing_credits_per_usd":500000}'),(2,2,NULL,'{"pricing_credits_per_usd":500000}')`).Error)
	var logs []Log
	require.NoError(t, db.Raw("SELECT logs.*, logs.quota IS NOT NULL AS quota_stored_valid FROM app.logs ORDER BY id").Scan(&logs).Error)
	require.Len(t, logs, 2)
	projectReadLogQuota(&logs[0])
	projectReadLogQuota(&logs[1])
	require.True(t, logs[0].QuotaStoredValid)
	require.Equal(t, "0.5", *logs[0].QuotaUSD)
	require.Equal(t, 250000, logs[0].Quota)
	require.False(t, logs[1].QuotaStoredValid)
	require.Nil(t, logs[1].QuotaUSD)
	require.Equal(t, "invalid_stored_quota", logs[1].QuotaMoneyBasis)
	var retained bool
	require.NoError(t, db.Raw("SELECT (SELECT quota=250000 FROM app.logs WHERE id=1) AND (SELECT quota IS NULL FROM app.logs WHERE id=2)").Scan(&retained).Error)
	require.True(t, retained)
	require.NoError(t, db.Exec("ALTER TABLE app.logs DROP COLUMN quota").Error)
	require.ErrorContains(t, verifyPostgresSchemaInventory(db, inventory), "required PostgreSQL column logs.quota is missing")
	t.Logf("postgres=%s no_tcp=true alias_scan=true raw_quota_unchanged=true missing_stored_column_rejected=true", version)
}
