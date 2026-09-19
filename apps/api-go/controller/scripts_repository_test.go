package controller

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSyncRepositoryScriptsIgnoresReadme(t *testing.T) {
	repository := t.TempDir()
	destination := t.TempDir()
	t.Setenv("SCRIPTS_DIR", destination)
	names := []string{"dsh.ps1", "dsh.sh", "pi.ps1", "pi.sh"}
	for _, name := range append(append([]string{}, names...), "README.md") {
		if err := os.WriteFile(filepath.Join(repository, name), []byte("contents of "+name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(repository, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := syncRepositoryScripts(repository); err != nil {
		t.Fatal(err)
	}
	if got := listScriptNames(); !reflect.DeepEqual(got, names) {
		t.Fatalf("scripts = %v, want %v", got, names)
	}
	for _, name := range names {
		body, _, err := readScript(name)
		if err != nil || string(body) != "contents of "+name {
			t.Fatalf("read %s: body=%q, err=%v", name, body, err)
		}
	}
	if _, err := os.Stat(filepath.Join(destination, "README.md")); !os.IsNotExist(err) {
		t.Fatalf("README must not be published: %v", err)
	}
}
