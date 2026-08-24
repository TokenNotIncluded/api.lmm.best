// Copyright (c) 2025-2026 QuantumNous. All rights reserved.

package controller

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetUpstreamURLsUsesPublishedChineseFeedPath(t *testing.T) {
	t.Setenv("SYNC_UPSTREAM_BASE", "https://catalog.example/root/")

	for _, locale := range []string{"zh", "zh-CN", "zh-TW"} {
		modelsURL, vendorsURL := getUpstreamURLs(locale)
		assert.Equal(t, "https://catalog.example/root/api/i18n/zh/newapi/models.json", modelsURL)
		assert.Equal(t, "https://catalog.example/root/api/i18n/zh/newapi/vendors.json", vendorsURL)
	}
}

func TestModelSyncCacheEvictsByTotalBytes(t *testing.T) {
	cache := newModelSyncCache(8, 180)
	cache.Store("first", modelSyncCacheEntry{ETag: "one", Body: []byte(strings.Repeat("a", 64))})
	cache.Store("second", modelSyncCacheEntry{ETag: "two", Body: []byte(strings.Repeat("b", 64))})

	assert.LessOrEqual(t, cache.Bytes(), int64(180))
	assert.LessOrEqual(t, cache.Len(), 1)
	_, firstExists := cache.Load("first")
	_, secondExists := cache.Load("second")
	assert.False(t, firstExists)
	assert.True(t, secondExists)
}
