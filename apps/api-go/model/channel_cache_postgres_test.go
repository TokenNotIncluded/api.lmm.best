package model

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/jackc/pgx/v5"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func postgresDSNWithSearchPath(dsn, schema string) string {
	parsed, err := url.Parse(dsn)
	if err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return strings.TrimSpace(dsn) + " search_path=" + schema
}

func openIsolatedPostgresCacheTestDB(t *testing.T, models ...any) *gorm.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set TEST_POSTGRES_DSN to run PostgreSQL cache integration tests")
	}
	if os.Getenv("TEST_POSTGRES_ISOLATED_SCHEMA") != "1" {
		t.Skip("set TEST_POSTGRES_ISOLATED_SCHEMA=1 to acknowledge isolated test-schema creation")
	}

	base, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open PostgreSQL test database: %v", err)
	}
	baseSQL, err := base.DB()
	if err != nil {
		t.Fatalf("get PostgreSQL test database handle: %v", err)
	}
	connectCtx, cancelConnect := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelConnect()
	if err := baseSQL.PingContext(connectCtx); err != nil {
		_ = baseSQL.Close()
		t.Fatalf("ping PostgreSQL test database: %v", err)
	}

	schema := fmt.Sprintf("lmm_cache_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	createCtx, cancelCreate := context.WithTimeout(context.Background(), 5*time.Second)
	if err := base.WithContext(createCtx).Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		cancelCreate()
		_ = baseSQL.Close()
		t.Fatalf("create isolated PostgreSQL schema: %v", err)
	}
	cancelCreate()
	t.Cleanup(func() {
		dropCtx, cancelDrop := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelDrop()
		if err := base.WithContext(dropCtx).Exec("DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE").Error; err != nil {
			t.Errorf("drop isolated PostgreSQL schema: %v", err)
		}
		_ = baseSQL.Close()
	})

	isolated, err := gorm.Open(
		postgres.New(postgres.Config{DSN: postgresDSNWithSearchPath(dsn, schema), PreferSimpleProtocol: true}),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("open isolated PostgreSQL schema: %v", err)
	}
	isolatedSQL, err := isolated.DB()
	if err != nil {
		t.Fatalf("get isolated PostgreSQL handle: %v", err)
	}
	isolatedSQL.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = isolatedSQL.Close() })

	migrateCtx, cancelMigrate := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelMigrate()
	if err := isolated.WithContext(migrateCtx).AutoMigrate(models...); err != nil {
		t.Fatalf("migrate isolated PostgreSQL schema: %v", err)
	}
	return isolated
}

func usePostgresDatabaseType(t *testing.T) {
	t.Helper()
	previousMain := common.MainDatabaseType()
	previousLog := common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	initCol()
	t.Cleanup(func() {
		common.SetDatabaseTypes(previousMain, previousLog)
		initCol()
	})
}

func postgresTestChannel(id int, name string, status int, tag string) Channel {
	jsonObject := "{}"
	channel := Channel{
		Id:                id,
		Name:              name,
		Type:              constant.ChannelTypeOpenAI,
		Key:               "test-key",
		Status:            status,
		Group:             "default",
		Models:            name + "-model",
		StatusCodeMapping: &jsonObject,
		// PostgreSQL migrations store these legacy string fields as JSON. The
		// production model accepts empty strings for old rows, but new fixtures
		// must insert syntactically valid JSON.
		Other:         "{}",
		OtherInfo:     "{}",
		OtherSettings: "{}",
	}
	if tag != "" {
		channel.SetTag(tag)
	}
	return channel
}

func TestPostgresMutationReturningReportsOnlyAffectedIDs(t *testing.T) {
	preserveChannelTestState(t)
	usePostgresDatabaseType(t)
	db := openIsolatedPostgresCacheTestDB(t, &Channel{}, &Ability{})
	operationCtx, cancelOperation := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelOperation()
	DB = db.WithContext(operationCtx)
	common.MemoryCacheEnabled = true

	channels := []Channel{
		postgresTestChannel(1101, "tag-one", common.ChannelStatusEnabled, "target-tag"),
		postgresTestChannel(1102, "tag-two", common.ChannelStatusEnabled, "target-tag"),
		postgresTestChannel(1103, "tag-other", common.ChannelStatusEnabled, "other-tag"),
		postgresTestChannel(1201, "status-one", 9, ""),
		postgresTestChannel(1202, "status-other", common.ChannelStatusEnabled, ""),
		postgresTestChannel(1301, "batch-one", common.ChannelStatusEnabled, ""),
		postgresTestChannel(1302, "batch-other", common.ChannelStatusEnabled, ""),
	}
	// ChannelInfo.Value returns []byte, which the PostgreSQL simple protocol
	// encodes as bytea rather than JSON. These cache tests do not exercise
	// multi-key metadata, so omit that unrelated field from seed inserts.
	if err := DB.Omit("channel_info").Create(&channels).Error; err != nil {
		t.Fatalf("seed PostgreSQL channels: %v", err)
	}

	tagTx := DB.Begin()
	if tagTx.Error != nil {
		t.Fatalf("begin tag mutation: %v", tagTx.Error)
	}
	tagIDs, err := updateChannelStatusReturningIDs(tagTx, common.ChannelStatusManuallyDisabled, "tag = ?", "target-tag")
	if err != nil {
		_ = tagTx.Rollback().Error
		t.Fatalf("tag mutation returning IDs: %v", err)
	}
	if err := tagTx.Commit().Error; err != nil {
		t.Fatalf("commit tag mutation: %v", err)
	}
	sort.Ints(tagIDs)
	if fmt.Sprint(tagIDs) != fmt.Sprint([]int{1101, 1102}) {
		t.Fatalf("tag mutation IDs = %v, want [1101 1102]", tagIDs)
	}

	statusTx := DB.Begin()
	if statusTx.Error != nil {
		t.Fatalf("begin status mutation: %v", statusTx.Error)
	}
	statusIDs, statusCount, err := deleteChannelsReturningIDs(statusTx, "status = ?", 9)
	if err != nil {
		_ = statusTx.Rollback().Error
		t.Fatalf("status mutation returning IDs: %v", err)
	}
	if err := statusTx.Commit().Error; err != nil {
		t.Fatalf("commit status mutation: %v", err)
	}
	if statusCount != 1 || len(statusIDs) != 1 || statusIDs[0] != 1201 {
		t.Fatalf("status mutation count=%d IDs=%v, want count=1 IDs=[1201]", statusCount, statusIDs)
	}

	batchTx := DB.Begin()
	if batchTx.Error != nil {
		t.Fatalf("begin batch mutation: %v", batchTx.Error)
	}
	batchIDs, batchCount, err := deleteChannelsReturningIDs(batchTx, "id in (?)", []int{1301, 999999})
	if err != nil {
		_ = batchTx.Rollback().Error
		t.Fatalf("batch mutation returning IDs: %v", err)
	}
	if err := batchTx.Commit().Error; err != nil {
		t.Fatalf("commit batch mutation: %v", err)
	}
	if batchCount != 1 || len(batchIDs) != 1 || batchIDs[0] != 1301 {
		t.Fatalf("batch mutation count=%d IDs=%v, want count=1 IDs=[1301]", batchCount, batchIDs)
	}
}

func TestPostgresChannelRefreshUsesConsistentRepeatableReadSnapshot(t *testing.T) {
	preserveChannelTestState(t)
	usePostgresDatabaseType(t)
	db := openIsolatedPostgresCacheTestDB(t, &Channel{}, &Ability{})
	operationCtx, cancelOperation := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelOperation()
	DB = db.WithContext(operationCtx)
	common.MemoryCacheEnabled = true

	channel := postgresTestChannel(2101, "old", common.ChannelStatusEnabled, "")
	channel.Group = "default"
	channel.Models = "old-model"
	if err := DB.Omit("channel_info").Create(&channel).Error; err != nil {
		t.Fatalf("seed repeatable-read channel: %v", err)
	}
	ability := Ability{Group: "default", Model: "old-model", ChannelId: channel.Id, Enabled: true}
	if err := DB.Create(&ability).Error; err != nil {
		t.Fatalf("seed repeatable-read ability: %v", err)
	}

	var interleaveErr error
	channelAfterChannelsQueryHook = func() {
		interleaveErr = db.WithContext(operationCtx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&Channel{}).Where("id = ?", channel.Id).Updates(map[string]any{
				"group":  "new-group",
				"models": "new-model",
			}).Error; err != nil {
				return err
			}
			return tx.Model(&Ability{}).Where(
				`"group" = ? AND model = ? AND channel_id = ?`,
				ability.Group,
				ability.Model,
				ability.ChannelId,
			).Updates(map[string]any{
				"group": "new-group",
				"model": "new-model",
			}).Error
		})
	}

	if err := refreshChannelCache(); err != nil {
		t.Fatalf("refresh repeatable-read channel snapshot: %v", err)
	}
	if interleaveErr != nil {
		t.Fatalf("interleaved PostgreSQL update: %v", interleaveErr)
	}
	oldChannel, err := GetRandomSatisfiedChannel("default", "old-model", 0, "")
	if err != nil || oldChannel == nil || oldChannel.Id != channel.Id {
		t.Fatalf("old snapshot route channel=%#v err=%v", oldChannel, err)
	}
	newChannel, err := GetRandomSatisfiedChannel("new-group", "new-model", 0, "")
	if err != nil {
		t.Fatalf("select new snapshot route: %v", err)
	}
	if newChannel != nil {
		t.Fatalf("refresh mixed post-interleave rows into snapshot: %#v", newChannel)
	}
}

func TestPostgresRefreshQueriesHonorContextCancellation(t *testing.T) {
	preserveChannelTestState(t)
	preservePricingTestState(t)
	usePostgresDatabaseType(t)
	db := openIsolatedPostgresCacheTestDB(t, &Channel{}, &Ability{}, &Model{}, &Vendor{})
	operationCtx, cancelOperation := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelOperation()
	DB = db.WithContext(operationCtx)
	common.MemoryCacheEnabled = true

	channelCtx, cancelChannel := context.WithCancel(context.Background())
	channelContextHook = func() (context.Context, context.CancelFunc) { return channelCtx, func() {} }
	channelAfterChannelsQueryHook = cancelChannel
	if err := refreshChannelCache(); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("second PostgreSQL channel query error = %v, want context canceled", err)
	}
	channelContextHook = nil
	channelAfterChannelsQueryHook = nil

	channel := postgresTestChannel(3101, "gpt-context", common.ChannelStatusEnabled, "")
	channel.Models = "gpt-postgres-context-test"
	if err := DB.Omit("channel_info").Create(&channel).Error; err != nil {
		t.Fatalf("seed pricing cancellation channel: %v", err)
	}
	if err := DB.Create(&Ability{
		Group:     "default",
		Model:     channel.Models,
		ChannelId: channel.Id,
		Enabled:   true,
	}).Error; err != nil {
		t.Fatalf("seed pricing cancellation ability: %v", err)
	}

	pricingCtx, cancelPricing := context.WithCancel(context.Background())
	pricingContextHook = func() (context.Context, context.CancelFunc) { return pricingCtx, func() {} }
	pricingVendorHook = cancelPricing
	pricingCache.Store(nil)
	if err := refreshPricingNow(); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("PostgreSQL vendor write error = %v, want context canceled", err)
	}
	if pricingCache.Load() != nil {
		t.Fatal("canceled PostgreSQL vendor write published pricing snapshot")
	}
}
