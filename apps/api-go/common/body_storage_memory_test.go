package common

import (
	"bytes"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryStorageCloseDropsOwnedReferencesButKeepsOpenReplay(t *testing.T) {
	before := GetDiskCacheStats()
	payload := []byte("payload retained by an independent replay")
	storage := newMemoryStorage(payload)
	replay, err := storage.NewReader()
	require.NoError(t, err)
	defer replay.Close()

	require.NoError(t, storage.Close())
	require.NoError(t, storage.Close())
	assert.Nil(t, storage.data)
	assert.Nil(t, storage.reader)
	assert.EqualValues(t, len(payload), storage.Size())
	_, err = storage.Read(make([]byte, 1))
	require.ErrorIs(t, err, ErrStorageClosed)
	_, err = storage.Seek(0, io.SeekStart)
	require.ErrorIs(t, err, ErrStorageClosed)
	_, err = storage.Bytes()
	require.ErrorIs(t, err, ErrStorageClosed)
	_, err = storage.NewReader()
	require.ErrorIs(t, err, ErrStorageClosed)

	got, err := io.ReadAll(replay)
	require.NoError(t, err)
	assert.Equal(t, payload, got)
	after := GetDiskCacheStats()
	assert.Equal(t, before.ActiveMemoryBuffers, after.ActiveMemoryBuffers)
	assert.Equal(t, before.CurrentMemoryUsageBytes, after.CurrentMemoryUsageBytes)
}

func TestLegacyBodyCacheTransfersOwnership(t *testing.T) {
	for _, disk := range []bool{false, true} {
		name := "memory"
		if disk {
			name = "disk"
		}
		t.Run(name, func(t *testing.T) {
			previous := GetDiskCacheConfig()
			SetDiskCacheConfig(DiskCacheConfig{Enabled: disk, ThresholdMB: 0, MaxSizeMB: 16, Path: t.TempDir()})
			t.Cleanup(func() { SetDiskCacheConfig(previous) })
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			payload := []byte(`{"model":"legacy","input":"original"}`)
			c.Set(KeyRequestBody, payload)
			storage, err := GetBodyStorage(c)
			require.NoError(t, err)
			defer CleanupBodyStorage(c)
			assert.Equal(t, disk, storage.IsDisk())
			legacy, _ := c.Get(KeyRequestBody)
			assert.Nil(t, legacy, "the old []byte must not retain a spilled body")
			got, err := storage.Bytes()
			require.NoError(t, err)
			assert.True(t, bytes.Equal(payload, got))
			again, err := GetBodyStorage(c)
			require.NoError(t, err)
			assert.Same(t, storage, again)
			CleanupBodyStorage(c)
			_, err = storage.Bytes()
			require.ErrorIs(t, err, ErrStorageClosed)
		})
	}
}

func TestCleanupBodyStorageLeavesUnusedContextEmpty(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	CleanupBodyStorage(c)
	assert.Nil(t, c.Keys, "unused body cleanup must not allocate a context map")
}

func TestCleanupBodyStorageClearsUnconsumedLegacyCache(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(KeyRequestBody, []byte("adapter failed before creating storage"))
	CleanupBodyStorage(c)
	CleanupBodyStorage(c)
	legacy, _ := c.Get(KeyRequestBody)
	assert.Nil(t, legacy)
}
