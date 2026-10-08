package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type commerceImportStockWorkerInput struct {
	DSN                  string
	Actor                int
	ConnectionID, Owner  string
	LeaseExpiresAt       int64
	RequestID, BatchRaw  string
	StartPath, ReadyPath string
}

// Invoked by two independent application processes, not two goroutines or
// handles. The inherited receipt lease models duplicate job delivery; every
// stock commit still locks and verifies its durable owner in the database.
func TestCommerceImportStockSubprocessWorker(t *testing.T) {
	configPath := os.Getenv("LMM_COMMERCE_STOCK_WORKER")
	if configPath == "" {
		t.Skip("private subprocess worker")
	}
	bytes, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var config commerceImportStockWorkerInput
	require.NoError(t, json.Unmarshal(bytes, &config))
	dsn, err := merchantStoreLocalPostgresDSN(config.DSN)
	require.NoError(t, err)
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	defer pool.Close()
	pool.SetMaxOpenConns(1)
	DB = db
	common.RedisEnabled = false
	usePostgresDatabaseType(t)
	require.NoError(t, os.WriteFile(config.ReadyPath, []byte("ready"), 0600))
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(config.StartPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker start barrier expired")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var wire struct {
		GrantID         string   `json:"grant_id"`
		ProductID       string   `json:"product_id"`
		VariantID       string   `json:"variant_id"`
		BatchID         string   `json:"batch_id"`
		Count           int      `json:"count"`
		Codes           []string `json:"codes"`
		RecoveryExpires int64    `json:"recovery_expires"`
	}
	require.NoError(t, json.Unmarshal([]byte(config.BatchRaw), &wire))
	batch := CommerceImportBatchInput{GrantID: wire.GrantID, ExternalProductID: wire.ProductID, ExternalVariantID: wire.VariantID, BatchID: wire.BatchID, Count: wire.Count, Codes: wire.Codes, RawJSON: config.BatchRaw, RecoveryExpiresAt: wire.RecoveryExpires}
	row, created, err := ReceiveCommerceImportBatch(config.Actor, CommerceImportLease{ConnectionID: config.ConnectionID, Owner: config.Owner, ExpiresAt: config.LeaseExpiresAt}, config.RequestID, batch)
	require.NoError(t, err)
	require.Equal(t, "imported", row.Status)
	fmt.Printf("commerce-stock-created=%t\n", created)
}

func TestCommerceImportPostgresTwoProcessesSameBatchAddStockExactlyOnce(t *testing.T) {
	db, schema, _, _ := merchantStorePGDB(t)
	f := commerceImportInventorySetup(t, db)
	mapped, err := ImportCommerceImportProduct(f.seller.Id, f.lease, commerceImportInventoryDraft())
	require.NoError(t, err)
	request, _, err := PrepareCommerceImportRestock(f.seller.Id, f.lease, commerceImportInventoryRequest(mapped, "two-process-stock-key"))
	require.NoError(t, err)
	batch := commerceImportInventoryBatch(request)
	parsed, err := url.Parse(os.Getenv("MERCHANT_STORE_POSTGRES_TEST_DSN"))
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	query.Set("application_name", "commerce_stock_worker")
	query.Set("statement_timeout", "15000")
	query.Set("lock_timeout", "10000")
	parsed.RawQuery = query.Encode()
	dir := t.TempDir()
	start := filepath.Join(dir, "start")
	commands := make([]*exec.Cmd, 2)
	outputs := make([]strings.Builder, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		config := commerceImportStockWorkerInput{DSN: parsed.String(), Actor: f.seller.Id, ConnectionID: f.lease.ConnectionID, Owner: f.lease.Owner, LeaseExpiresAt: f.lease.ExpiresAt, RequestID: request.ID, BatchRaw: batch.RawJSON, StartPath: start, ReadyPath: filepath.Join(dir, "ready-"+strconv.Itoa(i))}
		bytes, err := json.Marshal(config)
		require.NoError(t, err)
		path := filepath.Join(dir, "worker-"+strconv.Itoa(i)+".json")
		require.NoError(t, os.WriteFile(path, bytes, 0600))
		commands[i] = exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCommerceImportStockSubprocessWorker$", "-test.count=1")
		commands[i].Env = append(os.Environ(), "LMM_COMMERCE_STOCK_WORKER="+path, "GOMAXPROCS=2")
		commands[i].Stdout = &outputs[i]
		commands[i].Stderr = &outputs[i]
		require.NoError(t, commands[i].Start())
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		ready := 0
		for i := 0; i < 2; i++ {
			if _, err := os.Stat(filepath.Join(dir, "ready-"+strconv.Itoa(i))); err == nil {
				ready++
			} else if !errors.Is(err, os.ErrNotExist) {
				require.NoError(t, err)
			}
		}
		if ready == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stock subprocesses did not reach barrier")
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.NoError(t, os.WriteFile(start, []byte("start"), 0600))
	created, repeated := 0, 0
	for i, command := range commands {
		require.NoError(t, command.Wait(), outputs[i].String())
		if strings.Contains(outputs[i].String(), "commerce-stock-created=true") {
			created++
		}
		if strings.Contains(outputs[i].String(), "commerce-stock-created=false") {
			repeated++
		}
	}
	require.Equal(t, 1, created)
	require.Equal(t, 1, repeated)
	var count int64
	require.NoError(t, db.Model(&MerchantStoreStock{}).Where("product_id = ?", mapped.LocalProductID).Count(&count).Error)
	require.EqualValues(t, 2, count)
	require.NoError(t, db.Model(&MerchantStoreCommerceCardBatch{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	stored, err := GetCommerceImportRestockRequest(f.seller.Id, f.connection.ID, request.ID)
	require.NoError(t, err)
	require.Equal(t, "imported", stored.Status)
	t.Log("two independent application processes: one batch import, one replay, exactly two encrypted stock rows")
}
