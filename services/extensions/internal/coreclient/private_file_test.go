//go:build unix

package coreclient

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestPrivateCredentialFileBoundaries(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private-secret-path")
	token := strings.Repeat("x", 32)
	for _, mode := range []os.FileMode{0400, 0600, 0644, 0640, 0666, 0700} {
		if err := os.WriteFile(path, []byte(token), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		got, err := ReadTokenFile(path)
		if (err == nil) != (mode == 0400 || mode == 0600) {
			t.Fatalf("mode %o accepted=%v", mode, err == nil)
		}
		if err == nil && string(got) != token {
			t.Fatal("secret changed")
		}
		if err != nil && strings.Contains(err.Error(), path) {
			t.Fatal("private path leaked")
		}
		_ = os.Chmod(path, 0600)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTokenFile(link); err == nil {
		t.Fatal("symlink accepted")
	}
	fifo := filepath.Join(root, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTokenFile(fifo); err == nil {
		t.Fatal("FIFO accepted")
	}
	if _, err := ReadTokenFile(root); err == nil {
		t.Fatal("directory accepted")
	}
	if _, err := ReadTokenFile(path + "-missing"); err == nil || strings.Contains(err.Error(), root) {
		t.Fatal("missing file error unsafe", err)
	}
}
