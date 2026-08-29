package appcli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repositoryRoot(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := workingDirectory
	for range 8 {
		if info, err := os.Stat(filepath.Join(root, "package.json")); err == nil && info.Mode().IsRegular() {
			return root
		}
		parent := filepath.Dir(root)
		if parent == root {
			break
		}
		root = parent
	}
	t.Fatalf("repository root is not reachable from %s", workingDirectory)
	return ""
}

func TestRepositoryDeploymentBehaviorLivesInBackendCLIs(t *testing.T) {
	root := repositoryRoot(t)
	if _, err := os.Lstat(filepath.Join(root, "deploy")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retired root deploy directory exists or is unreadable: %v", err)
	}

	assertContains := func(relativePath string, required ...string) {
		t.Helper()
		contents, err := os.ReadFile(filepath.Join(root, relativePath))
		if err != nil {
			t.Fatal(err)
		}
		text := string(contents)
		for _, value := range required {
			if !strings.Contains(text, value) {
				t.Fatalf("%s is missing %q", relativePath, value)
			}
		}
	}

	assertContains(
		"packaging/common/lmm-api/lmm-api.service",
		"ExecStart=/usr/bin/lmm-api serve",
	)
	assertContains(
		"packaging/common/lmm-api/lmm-api-web.install",
		`/usr/bin/lmm-api deploy frontend package-activate --package-version "$1"`,
	)
	assertContains(
		"docs/backend-cli-deployment-contract.md",
		"/usr/bin/lmm-api-go",
		"/usr/bin/lmm-api-rs",
		"/usr/bin/lmm-api -> lmm-api-go",
		"Manual rollback",
	)
}
