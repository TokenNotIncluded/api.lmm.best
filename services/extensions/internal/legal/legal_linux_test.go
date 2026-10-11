//go:build linux

package legal

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestOperatorFilesRejectFIFOWithoutOpening(t *testing.T) {
	for _, target := range []string{"configuration", "template"} {
		t.Run(target, func(t *testing.T) {
			root, c := fixture(t)
			path := writeConfig(t, root, c)
			file := path
			if target == "template" {
				file = filepath.Join(root, "en.md")
			}
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(file, 0600); err != nil {
				t.Fatal(err)
			}
			if h, err := Load(path); h != nil || err == nil || strings.Contains(err.Error(), root) {
				t.Fatalf("FIFO accepted or exposed path: %v %v", h, err)
			}
		})
	}
}
