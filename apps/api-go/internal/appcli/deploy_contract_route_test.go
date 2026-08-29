package appcli

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRouteContractPrintGenerateVerify(t *testing.T) {
	root := t.TempDir()
	version := filepath.Join(root, "contracts", "api-route", "VERSION")
	if err := os.MkdirAll(filepath.Dir(version), 0o755); err != nil { t.Fatal(err) }
	if err := os.WriteFile(version, []byte("1.2.3\n"), 0o644); err != nil { t.Fatal(err) }
	runtime := routeContractRuntime{versionPath: version}
	expected := fmt.Sprintf("%x", sha256.Sum256([]byte("1.2.3\n")))
	var output bytes.Buffer
	if err := runtime.run([]string{"print"}, &output); err != nil { t.Fatal(err) }
	if output.String() != expected+"\n" { t.Fatalf("print=%q", output.String()) }
	revision := filepath.Join(root, "out", "API_ROUTE_CONTRACT_REVISION")
	if err := runtime.run([]string{"generate", revision}, &bytes.Buffer{}); err != nil { t.Fatal(err) }
	if err := runtime.run([]string{"verify", revision}, &bytes.Buffer{}); err != nil { t.Fatal(err) }
}

func TestRouteContractRejectsUnsafeVersionAndRevision(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, "VERSION.real")
	if err := os.WriteFile(valid, []byte("1.0.0\n"), 0o644); err != nil { t.Fatal(err) }
	link := filepath.Join(root, "VERSION")
	if err := os.Symlink(filepath.Base(valid), link); err != nil { t.Fatal(err) }
	if _, err := (routeContractRuntime{versionPath: link}).revision(); err == nil { t.Fatal("accepted symlink VERSION") }
	for _, content := range []string{"1.0.0", "01.0.0\n", "1.0.0\nextra\n", "1.0.0-rc.1\n"} {
		if err := os.Remove(link); err != nil { t.Fatal(err) }
		if err := os.WriteFile(link, []byte(content), 0o644); err != nil { t.Fatal(err) }
		if _, err := (routeContractRuntime{versionPath: link}).revision(); err == nil { t.Fatalf("accepted %q", content) }
	}
}
