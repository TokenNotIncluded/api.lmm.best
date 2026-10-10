package types

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestRT18MemoryCacheConcurrentReadAndClose(t *testing.T) {
	cache := NewMemoryCachedData("YWJj", "application/octet-stream", 3)
	const workers = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for j := 0; j < 40; j++ {
				switch i % 3 {
				case 0:
					cache.SetBase64Data("YWJj")
				case 1:
					data, err := cache.GetBase64Data()
					if err == nil && data != "YWJj" {
						t.Errorf("unexpected memory data: %q", data)
					}
				case 2:
					if err := cache.Close(); err != nil {
						t.Error(err)
					}
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if data, err := cache.GetBase64Data(); err == nil || data != "" {
		t.Fatalf("closed memory cache remains readable: %q %v", data, err)
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRT18DiskCacheCloseIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "media.b64")
	if err := os.WriteFile(path, []byte("YWJj"), 0600); err != nil {
		t.Fatal(err)
	}
	cache := NewDiskCachedData(path, "application/octet-stream", 3)
	cache.DiskSize = 4
	var closed atomic.Int32
	cache.OnClose = func(size int64) {
		if size != 4 {
			t.Errorf("wrong disk size: %d", size)
		}
		closed.Add(1)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := cache.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if closed.Load() != 1 {
		t.Errorf("disk cleanup callback invoked %d times", closed.Load())
	}
	if _, err := cache.GetBase64Data(); err == nil {
		t.Fatal("closed disk cache remains readable")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("disk cache was not removed: %v", err)
	}
}
