// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package composition

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStorageURLRejectsUnisolatedConfiguration(t *testing.T) {
	for _, test := range []struct {
		name, url, database, component string
		mode                           os.FileMode
		ok                             bool
	}{
		{"valid", "postgres://role:synthetic@127.0.0.1:5432/lmm_store_test?sslmode=disable", "lmm_store_test", "store", 0600, true},
		{"wrong database", "postgres://role:synthetic@127.0.0.1/core_test", "lmm_store_test", "store", 0600, false},
		{"query override", "postgres://role@127.0.0.1/lmm_store_test?dbname=core_test", "lmm_store_test", "store", 0600, false},
		{"session options", "postgres://role@127.0.0.1/lmm_store_test?options=-csearch_path%3Dcore_identity", "lmm_store_test", "store", 0600, false},
		{"duplicate ssl", "postgres://role@127.0.0.1/lmm_store_test?sslmode=disable&sslmode=require", "lmm_store_test", "store", 0600, false},
		{"keyword DSN", "host=localhost dbname=lmm_store_test", "lmm_store_test", "store", 0600, false},
		{"public file", "postgres://role@127.0.0.1/lmm_store_test", "lmm_store_test", "store", 0644, false},
		{"component mismatch", "postgres://role@127.0.0.1/lmm_support_test", "lmm_support_test", "store", 0600, false},
		{"unrestricted name", "postgres://role@127.0.0.1/store", "store", "store", 0600, false},
		{"newline", "postgres://role@127.0.0.1/lmm_store_test\npassword=secret", "lmm_store_test", "store", 0600, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := filepath.Join(t.TempDir(), "url")
			if err := os.WriteFile(f, []byte(test.url), test.mode); err != nil {
				t.Fatal(err)
			}
			// WriteFile is affected by the process umask. Set the fixture's
			// intended mode explicitly so the public-file rejection is tested
			// even when the caller uses a restrictive private-file umask.
			if err := os.Chmod(f, test.mode); err != nil {
				t.Fatal(err)
			}
			got, err := storageURL(StorageConfig{test.component, f, test.database})
			if (err == nil) != test.ok {
				t.Fatalf("accepted=%v want %v", err == nil, test.ok)
			}
			if test.ok && got != test.url {
				t.Fatal("URL changed")
			}
			if err != nil && strings.Contains(err.Error(), "synthetic") {
				t.Fatal("credential in error")
			}
		})
	}
	root := t.TempDir()
	file := filepath.Join(root, "url")
	link := filepath.Join(root, "link")
	_ = os.WriteFile(file, []byte("postgres://role@127.0.0.1/lmm_store_test"), 0600)
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if _, err := storageURL(StorageConfig{"store", link, "lmm_store_test"}); err == nil {
		t.Fatal("symlink accepted")
	}
}
