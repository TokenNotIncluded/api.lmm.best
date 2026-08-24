package appcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type fakeBuildRunner struct {
	t            *testing.T
	repo         string
	version      string
	goReleaseTag string
	dirty        bool
}

func (runner *fakeBuildRunner) Run(_ context.Context, command productionCommand) ([]byte, error) {
	base := filepath.Base(command.Name)
	if command.Name == commandRunuser && len(command.Args) >= 5 && command.Args[0] == "--user" && command.Args[1] == "root" && command.Args[2] == "--" && command.Args[len(command.Args)-1] == "version" {
		return []byte(runner.version + "\n"), nil
	}
	switch base {
	case "git":
		joined := strings.Join(command.Args, " ")
		switch {
		case strings.Contains(joined, "status --porcelain"):
			if runner.dirty {
				return []byte(" M apps/api-go/main.go\n"), nil
			}
			return nil, nil
		case strings.Contains(joined, "rev-list --count"):
			return []byte("300\n"), nil
		case strings.Contains(joined, "rev-parse --short=9"):
			return []byte("abcdef123\n"), nil
		case strings.Contains(joined, "rev-parse HEAD"):
			return []byte("abcdef1234567890abcdef1234567890abcdef12\n"), nil
		case strings.Contains(joined, "tag --merged HEAD --list go-v*"):
			return []byte(runner.goReleaseTag + "\n"), nil
		case strings.Contains(joined, "ls-remote"):
			return []byte("abcdef1234567890abcdef1234567890abcdef12\trefs/heads/main\n"), nil
		}
	case "bun":
		if len(command.Args) >= 2 && command.Args[0] == "run" && command.Args[1] == "build:web" {
			dist := filepath.Join(runner.repo, "apps", "web", "dist")
			if err := os.MkdirAll(filepath.Join(dist, "static", "js"), 0o755); err != nil {
				runner.t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte(`<script src="/static/js/app.js"></script>`), 0o644); err != nil {
				runner.t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dist, "static", "js", "app.js"), []byte("app"), 0o644); err != nil {
				runner.t.Fatal(err)
			}
		}
		return nil, nil
	case "go":
		for index, argument := range command.Args {
			if argument == "-o" && index+1 < len(command.Args) {
				if err := os.WriteFile(command.Args[index+1], []byte("static-go-binary"), 0o755); err != nil {
					runner.t.Fatal(err)
				}
				return nil, nil
			}
		}
	case "file":
		return []byte("ELF 64-bit LSB executable, statically linked, stripped\n"), nil
	case "makepkg":
		pkgdest := environmentValue(command.Env, "PKGDEST")
		version := environmentValue(command.Env, "LMM_API_PKGVER")
		architecture := "x86_64"
		if runtime.GOARCH == "arm64" {
			architecture = "aarch64"
		}
		packagePath := filepath.Join(pkgdest, fmt.Sprintf("%s-%s-1-%s.pkg.tar.zst", productionAURPackageName, version, architecture))
		if err := os.WriteFile(packagePath, []byte("package"), 0o644); err != nil {
			runner.t.Fatal(err)
		}
		return nil, nil
	case "pacman":
		return []byte(productionAURPackageName + " " + runner.version + "-1\n"), nil
	}
	if strings.HasPrefix(command.Name, filepath.Clean(filepath.Join(filepath.Dir(command.Name), ".lmm-api-go."))) && len(command.Args) == 1 && command.Args[0] == "version" {
		return []byte(runner.version + "\n"), nil
	}
	return nil, fmt.Errorf("unexpected build command: %s %v", command.Name, command.Args)
}

func environmentValue(environment []string, key string) string {
	prefix := key + "="
	for index := len(environment) - 1; index >= 0; index-- {
		if strings.HasPrefix(environment[index], prefix) {
			return strings.TrimPrefix(environment[index], prefix)
		}
	}
	return ""
}

func writeBuildSourceFixture(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"VERSION":                             "0.1.1\n",
		"LICENSE":                             "license\n",
		"NOTICE":                              "notice\n",
		"THIRD-PARTY-LICENSES.md":             "third party\n",
		"apps/api-go/go.mod":                  "module fixture\n",
		"apps/web/package.json":               "{}\n",
		"packaging/local/lmm-api-go/PKGBUILD": "pkgname=lmm-api-go\n",
		"packaging/common/lmm-api/lmm-api.service":               "[Service]\nExecStart=/usr/bin/lmm-api serve\n",
		"packaging/common/lmm-api/lmm-api-go.env":                "SQL_DSN=postgres://fixture\n",
		"packaging/common/lmm-api/lmm-api-operator.sysusers":     "u lmm-api-deploy - fixture\n",
		"packaging/common/lmm-api/lmm-api-operator.tmpfiles":     "d /var/lib/lmm-api-go-deploy 0710 root lmm-api-deploy -\n",
		"packaging/common/lmm-api/lmm-api-operator.sudoers":      "lmm-api-deploy ALL=(root) NOPASSWD: /usr/bin/pacman\n",
		"packaging/common/lmm-api/geoip2-country-update.service": "[Service]\nExecStart=/usr/bin/lmm-api geoip update\n",
		"packaging/common/lmm-api/geoip2-country-update.timer":   "[Timer]\nUnit=geoip2-country-update.service\n",
		"deploy/nginx/http-map.conf":                             "map $http_upgrade $connection_upgrade {}\n",
		"deploy/nginx/lmm-api-locations.conf":                    "location / {}\n",
		"deploy/nginx/mime.types":                                "text/plain txt;\n",
		"deploy/nginx/new-api.conf":                              "server {}\n",
		"deploy/nginx/lmm-api-region-policy.conf":                "auth_request /internal/access-ip-policy;\n",
	}
	for relative, content := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(relative, ".env") {
			mode = 0o600
		}
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeDeployBuildProducesVersionedBinaryFrontendAndPackage(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBuildSourceFixture(t, repo)
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, productionWorkspaceMarker), []byte("deployment_id=local-build-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	version := "0.1.1.r300.gabcdef123"
	runner := &fakeBuildRunner{t: t, repo: repo, version: version}
	buildRuntime := &buildDeployRuntime{runner: runner, now: func() time.Time {
		return time.Date(2026, 8, 10, 2, 0, 0, 0, time.UTC)
	}}
	result, err := buildRuntime.build(context.Background(), buildDeployOptions{Repo: repo, Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != version || result.Dirty || result.BinarySHA256 == "" || result.PackageSHA256 == "" || result.FrontendIndexSHA256 == "" {
		t.Fatalf("build result=%#v", result)
	}
	for _, path := range []string{result.Binary, result.Package, result.Package + ".sha256", filepath.Join(repo, "apps", "web", "dist", "index.html")} {
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("missing build output %s: info=%v err=%v", path, info, err)
		}
	}
}

func TestProductionBuildRejectsDirtySourceBeforeRunningBuildTools(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBuildSourceFixture(t, repo)
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, productionWorkspaceMarker), []byte("deployment_id=production-build-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeBuildRunner{t: t, repo: repo, dirty: true}
	buildRuntime := &buildDeployRuntime{runner: runner, now: time.Now}
	_, err := buildRuntime.build(context.Background(), buildDeployOptions{Repo: repo, Workspace: workspace, Production: true})
	if err == nil || !strings.Contains(err.Error(), "clean tracked and untracked") {
		t.Fatalf("production dirty error=%v", err)
	}
}

func TestProductionBuildIdentityUsesMergedGoReleaseVersion(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBuildSourceFixture(t, repo)
	runner := &fakeBuildRunner{
		t:            t,
		repo:         repo,
		goReleaseTag: "go-v0.1.34\ngo-v0.1.9",
	}
	buildRuntime := &buildDeployRuntime{runner: runner, now: time.Now}
	_, version, dirty, err := buildRuntime.resolveBuildIdentity(context.Background(), buildDeployOptions{
		Repo: repo, Production: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if dirty || version != "0.1.34.r300.gabcdef123" {
		t.Fatalf("version=%q dirty=%t", version, dirty)
	}
}

func TestBuildWorkspaceMustBeMarkerOwned(t *testing.T) {
	workspace := t.TempDir()
	err := validateBuildWorkspace(workspace)
	if err == nil || !errors.Is(err, os.ErrNotExist) && !strings.Contains(err.Error(), "marker") {
		t.Fatalf("workspace validation error=%v", err)
	}
}
