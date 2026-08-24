package appcli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func writeFrontendFixture(t *testing.T, root, asset, body string) string {
	t.Helper()
	source := filepath.Join(root, strings.ReplaceAll(asset, "/", "-"))
	assetPath := filepath.Join(source, "static", "js", asset)
	if err := os.MkdirAll(filepath.Dir(assetPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte(`<script src="/static/js/`+asset+`"></script>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assetPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return source
}

func deployFrontendForTest(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := RunDeploy(append([]string{"frontend"}, args...), &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

func TestNativeFrontendDeployPublishesWithPublicPermissionsUnderPrivateUmask(t *testing.T) {
	oldMask := unix.Umask(0o077)
	defer unix.Umask(oldMask)

	workspace := t.TempDir()
	root := filepath.Join(workspace, "public")
	first := writeFrontendFixture(t, workspace, "old.111.js", "old")
	second := writeFrontendFixture(t, workspace, "new.222.js", "new")

	if _, stderr, code := deployFrontendForTest(t, "publish", "--root", root, "--source", first, "--release", "first", "--keep", "2"); code != ExitOK {
		t.Fatalf("first publish exit=%d stderr=%q", code, stderr)
	}
	stdout, stderr, code := deployFrontendForTest(t, "publish", "--root", root, "--source", second, "--release", "second", "--keep", "2")
	if code != ExitOK || stdout != "current=second\n" {
		t.Fatalf("second publish exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	for path, expected := range map[string]os.FileMode{
		filepath.Join(root, "assets"):                           0o755,
		filepath.Join(root, "assets", "js"):                     0o755,
		filepath.Join(root, "assets", "js", "new.222.js"):       0o444,
		filepath.Join(root, "releases", "second"):               0o755,
		filepath.Join(root, "releases", "second", "static"):     0o755,
		filepath.Join(root, "releases", "second", "index.html"): 0o444,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != expected {
			t.Errorf("%s mode=%o want=%o", path, info.Mode().Perm(), expected)
		}
	}
	if body, err := os.ReadFile(filepath.Join(root, "assets", "js", "old.111.js")); err != nil || string(body) != "old" {
		t.Fatalf("old immutable chunk unavailable: body=%q err=%v", body, err)
	}

	stdout, stderr, code = deployFrontendForTest(t, "rollback", "--root", root, "--release", "first", "--keep", "2")
	if code != ExitOK || stdout != "current=first\n" {
		t.Fatalf("rollback exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestNativeFrontendDeployRejectsImmutableCollisionAndSymlinkedSource(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "public")
	first := writeFrontendFixture(t, workspace, "same.111.js", "original")
	if _, stderr, code := deployFrontendForTest(t, "publish", "--root", root, "--source", first, "--release", "first"); code != ExitOK {
		t.Fatalf("first publish exit=%d stderr=%q", code, stderr)
	}

	collision := filepath.Join(workspace, "collision")
	asset := filepath.Join(collision, "static", "js", "same.111.js")
	if err := os.MkdirAll(filepath.Dir(asset), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collision, "index.html"), []byte(`<script src="/static/js/same.111.js"></script>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(asset, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := deployFrontendForTest(t, "publish", "--root", root, "--source", collision, "--release", "collision")
	if code == ExitOK || !strings.Contains(stderr, "immutable asset collision") {
		t.Fatalf("collision exit=%d stderr=%q", code, stderr)
	}

	symlinked := filepath.Join(workspace, "symlinked")
	if err := os.MkdirAll(symlinked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(symlinked, "index.html"), []byte("index"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(first, "static"), filepath.Join(symlinked, "static")); err != nil {
		t.Fatal(err)
	}
	_, stderr, code = deployFrontendForTest(t, "publish", "--root", root, "--source", symlinked, "--release", "symlinked")
	if code == ExitOK || !strings.Contains(stderr, "symlink") {
		t.Fatalf("symlink exit=%d stderr=%q", code, stderr)
	}
}

func TestNativeFrontendDeployNeverPrunesTheImmediateRollbackRelease(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "public")
	first := writeFrontendFixture(t, workspace, "first.111.js", "first")
	second := writeFrontendFixture(t, workspace, "second.222.js", "second")
	third := writeFrontendFixture(t, workspace, "third.333.js", "third")

	if _, stderr, code := deployFrontendForTest(t, "publish", "--root", root, "--source", first, "--release", "first", "--keep", "3"); code != ExitOK {
		t.Fatalf("first publish exit=%d stderr=%q", code, stderr)
	}
	if _, stderr, code := deployFrontendForTest(t, "publish", "--root", root, "--source", second, "--release", "second", "--keep", "3"); code != ExitOK {
		t.Fatalf("second publish exit=%d stderr=%q", code, stderr)
	}
	// Make the first release look newer than the currently active second
	// release. Pruning by mtime alone would otherwise remove the only safe
	// rollback target when third is published with keep=2.
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(root, "releases", "first"), future, future); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := deployFrontendForTest(t, "publish", "--root", root, "--source", third, "--release", "third", "--keep", "2"); code != ExitOK {
		t.Fatalf("third publish exit=%d stderr=%q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "releases", "second", "index.html")); err != nil {
		t.Fatalf("immediate rollback release was pruned: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "releases", "first")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("older non-protected release was not pruned: %v", err)
	}
}

func TestDeployUsageExposesNativeFrontendLifecycle(t *testing.T) {
	var output bytes.Buffer
	result := Dispatch([]string{"deploy", "help"}, "test", &output, &output)
	if result.ExitCode != ExitOK || !strings.Contains(output.String(), "lmm-api deploy frontend publish") {
		t.Fatalf("deploy help result=%#v output=%q", result, output.String())
	}
}
