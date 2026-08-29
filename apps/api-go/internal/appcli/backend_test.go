package appcli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeBackendOwner map[string]string

func (owners fakeBackendOwner) Owner(path string) (string, error) {
	owner, ok := owners[path]
	if !ok {
		return "", errors.New("unowned")
	}
	return owner, nil
}

func testBackendRuntime(t *testing.T) (*backendRuntime, string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "usr", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	paths := backendPaths{
		Canonical: filepath.Join(bin, backendCanonicalName),
		Go:        filepath.Join(bin, backendGoName),
		Rust:      filepath.Join(bin, backendRustName),
	}
	for _, path := range []string{paths.Go, paths.Rust} {
		if err := os.WriteFile(path, []byte("provider"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	owners := fakeBackendOwner{paths.Go: "lmm-api-go-bin", paths.Rust: "lmm-api-rs-git"}
	return &backendRuntime{
		paths:       paths,
		owner:       owners,
		effectiveID: func() int { return 0 },
		requiredUID: uint32(os.Getuid()),
	}, bin
}

func TestBackendSelectAtomicallyCreatesOneHopRelativeLink(t *testing.T) {
	runtime, _ := testBackendRuntime(t)
	var stdout bytes.Buffer
	if code := runtime.run([]string{"select", "go"}, &stdout, &bytes.Buffer{}); code != ExitOK {
		t.Fatalf("select exit=%d", code)
	}
	target, err := os.Readlink(runtime.paths.Canonical)
	if err != nil || target != backendGoName {
		t.Fatalf("canonical target=%q err=%v", target, err)
	}
	stdout.Reset()
	if code := runtime.run([]string{"select", "rust"}, &stdout, &bytes.Buffer{}); code != ExitOK {
		t.Fatalf("switch exit=%d", code)
	}
	target, err = os.Readlink(runtime.paths.Canonical)
	if err != nil || target != backendRustName {
		t.Fatalf("switched target=%q err=%v", target, err)
	}
	if !strings.Contains(stdout.String(), "provider=rust") || !strings.Contains(stdout.String(), "package=lmm-api-rs-git") {
		t.Fatalf("select output=%q", stdout.String())
	}
}

func TestBackendSelectRejectsNonRootAndUnsafeProviderEvidence(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *backendRuntime)
		want   string
	}{
		{name: "non-root", mutate: func(_ *testing.T, runtime *backendRuntime) { runtime.effectiveID = func() int { return 1000 } }, want: "must run as root"},
		{name: "symlink provider", mutate: func(t *testing.T, runtime *backendRuntime) {
			if err := os.Remove(runtime.paths.Go); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(backendRustName, runtime.paths.Go); err != nil {
				t.Fatal(err)
			}
		}, want: "safe"},
		{name: "writable provider", mutate: func(t *testing.T, runtime *backendRuntime) {
			if err := os.Chmod(runtime.paths.Go, 0o775); err != nil {
				t.Fatal(err)
			}
		}, want: "non-writable"},
		{name: "unowned provider", mutate: func(_ *testing.T, runtime *backendRuntime) {
			runtime.owner = fakeBackendOwner{}
		}, want: "ownership"},
		{name: "wrong package", mutate: func(_ *testing.T, runtime *backendRuntime) {
			runtime.owner = fakeBackendOwner{runtime.paths.Go: "unrelated"}
		}, want: "unexpected package owner"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime, _ := testBackendRuntime(t)
			test.mutate(t, runtime)
			var stderr bytes.Buffer
			if code := runtime.run([]string{"select", "go"}, &bytes.Buffer{}, &stderr); code == ExitOK || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("exit=%d stderr=%q want %q", code, stderr.String(), test.want)
			}
		})
	}
}

func TestBackendStatusRejectsWrongAbsoluteAndChainedLinks(t *testing.T) {
	for _, target := range []string{"/usr/bin/lmm-api-go", "nested/lmm-api-go", backendGoName} {
		t.Run(strings.ReplaceAll(target, "/", "_"), func(t *testing.T) {
			runtime, _ := testBackendRuntime(t)
			if target == backendGoName {
				if err := os.Remove(runtime.paths.Go); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(backendRustName, runtime.paths.Go); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(target, runtime.paths.Canonical); err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.status(); err == nil {
				t.Fatalf("status accepted unsafe target %q", target)
			}
		})
	}
}

func TestBackendSelectRefusesUnsafeExistingCanonicalPath(t *testing.T) {
	runtime, _ := testBackendRuntime(t)
	if err := os.WriteFile(runtime.paths.Canonical, []byte("legacy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.selectProvider("go"); err == nil || !strings.Contains(err.Error(), "not a symlink") {
		t.Fatalf("unsafe canonical error=%v", err)
	}
}
