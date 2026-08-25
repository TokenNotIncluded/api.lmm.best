package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type assistantKeyPostgresHarness struct {
	base   *gorm.DB
	db     *gorm.DB
	schema string
}

const zeroRatioWarningMessage = "This routing group is community-operated. Availability, model coverage, privacy handling, and billing behavior may be less predictable. Do not send secrets or sensitive data. Continue only if you accept these risks."

func assistantKeyPostgresDSN(dsn, schema string) string {
	parsed, err := url.Parse(dsn)
	if err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return strings.TrimSpace(dsn) + " search_path=" + schema
}

func createAssistantKeyPostgresSchema(db *gorm.DB, schema string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT set_config('lmm.test_schema', ?, true)", schema).Error; err != nil {
			return err
		}
		return tx.Exec("DO $body$ BEGIN EXECUTE format('CREATE SCHEMA %I', current_setting('lmm.test_schema')); END $body$").Error
	})
}

func dropAssistantKeyPostgresSchema(db *gorm.DB, schema string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT set_config('lmm.test_schema', ?, true)", schema).Error; err != nil {
			return err
		}
		return tx.Exec("DO $body$ BEGIN EXECUTE format('DROP SCHEMA IF EXISTS %I CASCADE', current_setting('lmm.test_schema')); END $body$").Error
	})
}

func openAssistantKeyPostgresHarness(t *testing.T) *assistantKeyPostgresHarness {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set TEST_POSTGRES_DSN to run assistant-key PostgreSQL integration tests")
	}
	if os.Getenv("TEST_POSTGRES_ISOLATED_SCHEMA") != "1" {
		t.Skip("set TEST_POSTGRES_ISOLATED_SCHEMA=1 to acknowledge isolated test-schema creation")
	}
	base, err := gorm.Open(
		postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}),
		&gorm.Config{},
	)
	require.NoError(t, err)
	baseSQL, err := base.DB()
	require.NoError(t, err)
	require.NoError(t, baseSQL.PingContext(t.Context()))

	schema := fmt.Sprintf("assistant_key_%d_%d", os.Getpid(), time.Now().UnixNano())
	require.NoError(t, createAssistantKeyPostgresSchema(base, schema))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		require.NoError(t, dropAssistantKeyPostgresSchema(base.WithContext(ctx), schema))
		require.NoError(t, baseSQL.Close())
	})

	db, err := gorm.Open(
		postgres.New(postgres.Config{
			DSN:                  assistantKeyPostgresDSN(dsn, schema),
			PreferSimpleProtocol: true,
		}),
		&gorm.Config{},
	)
	require.NoError(t, err)
	dbSQL, err := db.DB()
	require.NoError(t, err)
	dbSQL.SetMaxOpenConns(12)
	t.Cleanup(func() { require.NoError(t, dbSQL.Close()) })
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.UserSession{},
		&model.TwoFA{},
		&model.TwoFABackupCode{},
		&model.TopUp{},
		&model.Option{},
		&model.AuthFlow{},
		&model.Token{},
		&model.AssistantSecureCard{},
	))

	previousDB := model.DB
	previousMain := common.MainDatabaseType()
	previousLog := common.LogDatabaseType()
	previousRedisEnabled := common.RedisEnabled
	model.DB = db
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = previousRedisEnabled
		common.SetDatabaseTypes(previousMain, previousLog)
	})

	require.NoError(t, db.Create(&model.User{
		Id: 7, Username: "assistant-user", Password: "not-used-in-test", Role: 1,
		Status: 1, Group: "default", AuthVersion: 1, ConsoleActivatedAt: 1,
	}).Error)
	seedAssistantKeyPostgresOptions(t, db, map[string]string{
		"UserUsableGroups":                               `{"default":"Default","vip":"VIP"}`,
		"GroupRatio":                                     `{"default":1,"vip":2}`,
		"group_ratio_setting.group_warnings":             `{}`,
		"group_ratio_setting.group_special_usable_group": `{}`,
	})
	return &assistantKeyPostgresHarness{base: base, db: db, schema: schema}
}

func seedAssistantKeyPostgresOptions(t *testing.T, db *gorm.DB, values map[string]string) {
	t.Helper()
	options := make([]model.Option, 0, len(values))
	for key, value := range values {
		options = append(options, model.Option{Key: key, Value: value})
	}
	require.NoError(t, db.Save(&options).Error)
}

func createAssistantKeyPostgresFlow(
	t *testing.T,
	sessionID string,
	warning *ratio_setting.GroupWarning,
) string {
	t.Helper()
	require.NoError(t, model.DB.Save(&model.UserSession{
		SID: sessionID, UserID: 7, Version: 1, UserAuthVersion: 1,
		Status: model.UserSessionStatusActive, RefreshHash: strings.Repeat("a", 64),
		LoginMethod: "password", CreatedAt: time.Now().Unix(), LastActiveAt: time.Now().Unix(),
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}).Error)
	draft := assistantPreparedKeyDraft{
		Version: assistantKeyDraftVersion,
		Name:    "assistant-created",
		Group:   realSelectableGroup("default"),
		Warning: warning,
	}
	payload, err := json.Marshal(draft)
	require.NoError(t, err)
	token, flow, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose:   model.AuthFlowPurposeAssistantKey,
		UserId:    7,
		SessionId: sessionID,
		Payload:   string(payload),
		ExpiresAt: time.Now().Add(10 * time.Minute),
	})
	require.NoError(t, err)
	require.NotNil(t, flow)
	return token
}

func consumeAssistantKeyPostgresFlow(token, sessionID string) error {
	return consumeAssistantKeyPostgresFlowWithTwoFactor(token, sessionID, "")
}

func consumeAssistantKeyPostgresFlowWithTwoFactor(token, sessionID, twoFactorCode string) error {
	fence, err := model.NewAssistantKeyAuthorizationFence(7, sessionID, 1, 1, model.CurrentDeveloperAccessPolicy())
	if err != nil {
		return err
	}
	createdToken, card, err := model.ConsumeAssistantKeyFlowAndCreateSecureCard(
		token,
		fence,
		twoFactorCode,
		100,
		func(tx *gorm.DB, flow *model.AuthFlow) (*model.AssistantKeyMaterial, error) {
			return buildAssistantKeyMaterialTx(tx, flow, 7)
		},
	)
	if err == nil && (createdToken == nil || card == nil) {
		return errors.New("assistant key transaction returned incomplete material")
	}
	return err
}

func waitForAssistantKeyPostgresBlockedBy(t *testing.T, db *gorm.DB, blockerPID int) {
	t.Helper()
	for attempt := 0; attempt < 200; attempt++ {
		var blocked bool
		require.NoError(t, db.Raw(`
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity activity
				WHERE ? = ANY(pg_blocking_pids(activity.pid))
			)`, blockerPID).Scan(&blocked).Error)
		if blocked {
			return
		}
		runtime.Gosched()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no PostgreSQL process was blocked by backend %d", blockerPID)
}

func waitForAssistantKeyPostgresRelationLock(t *testing.T, db *gorm.DB, schema, table, mode string, granted bool) {
	t.Helper()
	for attempt := 0; attempt < 200; attempt++ {
		var locked bool
		require.NoError(t, db.Raw(`
			SELECT EXISTS (
				SELECT 1
				FROM pg_locks l
				JOIN pg_class c ON c.oid = l.relation
				JOIN pg_namespace n ON n.oid = c.relnamespace
				WHERE n.nspname = ? AND c.relname = ? AND l.mode = ? AND l.granted = ?
			)`, schema, table, mode, granted).Scan(&locked).Error)
		if locked {
			return
		}
		runtime.Gosched()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("relation lock %s on %s.%s not observed", mode, schema, table)
}

func countAssistantKeyPostgresRows(t *testing.T, db *gorm.DB, table string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Table(table).Count(&count).Error)
	return count
}
