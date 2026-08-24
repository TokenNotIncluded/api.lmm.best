package appcli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type edgePolicyTestRunner struct{}

func (edgePolicyTestRunner) Run(_ context.Context, command productionCommand) ([]byte, error) {
	if command.Name == commandSystemctl && len(command.Args) >= 2 &&
		(command.Args[0] == "is-active" || command.Args[0] == "is-enabled") {
		return nil, errors.New("unit inactive in test")
	}
	return nil, nil
}

type legacyPolicyStateRunner struct{}

func (legacyPolicyStateRunner) Run(_ context.Context, command productionCommand) ([]byte, error) {
	if command.Name == commandSystemctl && len(command.Args) >= 2 {
		switch command.Args[0] {
		case "is-active":
			return nil, errors.New("unit inactive in test")
		case "is-enabled":
			return []byte("static\n"), nil
		}
	}
	return nil, nil
}

func TestRejectActiveLegacyPolicyIgnoresInactiveStaticUnits(t *testing.T) {
	runtime := &productionRuntime{runner: legacyPolicyStateRunner{}}
	if err := runtime.rejectActiveLegacyPolicy(context.Background()); err != nil {
		t.Fatalf("rejectActiveLegacyPolicy() returned an error for inactive static units: %v", err)
	}
}

func TestEdgePolicyInstallBacksUpRemovesLegacyAndRestores(t *testing.T) {
	root := t.TempDir()
	assets := filepath.Join(root, "assets")
	nginxRoot := filepath.Join(root, "etc", "nginx")
	unitRoot := filepath.Join(root, "etc", "systemd", "system")
	for _, directory := range []string{
		filepath.Join(assets, "nginx"), nginxRoot, filepath.Join(nginxRoot, "conf.d"),
		filepath.Join(unitRoot), filepath.Join(root, "etc", "nftables.d"),
		filepath.Join(root, "usr", "local", "sbin"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runtime := &productionRuntime{
		paths: productionPaths{
			NginxRoot: nginxRoot, SystemdUnitRoot: unitRoot,
			EdgeAssetRoot: assets, BackupRoot: filepath.Join(root, "backups"),
		},
		runner: edgePolicyTestRunner{},
		now:    func() time.Time { return time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC) },
	}
	for _, asset := range runtime.edgePolicyAssets() {
		assetPath := filepath.Join(assets, asset.Source)
		if err := os.MkdirAll(filepath.Dir(assetPath), 0o755); err != nil {
			t.Fatal(err)
		}
		candidate := "candidate-" + asset.Key + "\n"
		switch asset.Key {
		case "http-map":
			candidate += "geoip2 /var/lib/geoip2/DBIP-Country-Lite.mmdb {\n"
		case "server":
			candidate += "include /etc/nginx/lmm-api-region-policy.conf;\n"
		case "locations":
			candidate += "error_page 418 = @lmm_api_cors_preflight;\n"
			candidate += "location @lmm_api_cors_preflight {\n"
			candidate += "auth_request off;\n"
			candidate += "set $lmm_access_policy_original_uri $uri;\n"
			candidate += "if ($request_method = OPTIONS) { return 418; }\n"
			candidate += "add_header Access-Control-Allow-Methods $http_access_control_request_method always;\n"
			candidate += "add_header Access-Control-Allow-Headers $http_access_control_request_headers always;\n"
			candidate += "add_header Vary \"Origin, Access-Control-Request-Method, Access-Control-Request-Headers\" always;\n"
		case "region-policy":
			candidate += "auth_request /internal/access-ip-policy;\n"
			candidate += "proxy_set_header X-LMM-Original-URI $lmm_access_policy_original_uri;\n"
			candidate += "proxy_set_header X-LMM-Original-Accept $http_accept;\n"
		}
		if err := os.WriteFile(assetPath, []byte(candidate), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(asset.Target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(asset.Target, []byte("old-"+asset.Key+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, legacy := range runtime.edgePolicyLegacyAssets() {
		if err := os.MkdirAll(filepath.Dir(legacy.Target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(legacy.Target, []byte("legacy\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	backup := filepath.Join(root, "backup")
	digest, err := runtime.captureEdgePolicyBackup(backup)
	if err != nil || digest == "" {
		t.Fatalf("captureEdgePolicyBackup() digest=%q err=%v", digest, err)
	}
	if err := runtime.applyEdgePolicyAssets(context.Background(), assets, backup, true); err != nil {
		t.Fatal(err)
	}
	for _, asset := range runtime.edgePolicyAssets() {
		candidate, _ := os.ReadFile(filepath.Join(assets, asset.Source))
		actual, _ := os.ReadFile(asset.Target)
		if string(actual) != string(candidate) {
			t.Fatalf("target %s was not installed", asset.Key)
		}
	}
	for _, legacy := range runtime.edgePolicyLegacyAssets() {
		if _, err := os.Lstat(legacy.Target); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("legacy target %s remains: %v", legacy.Key, err)
		}
	}
	if err := runtime.restoreEdgePolicyBackup(context.Background(), backup, digest); err != nil {
		t.Fatal(err)
	}
	for _, asset := range runtime.edgePolicyAssets() {
		actual, _ := os.ReadFile(asset.Target)
		if !strings.HasPrefix(string(actual), "old-") {
			t.Fatalf("target %s was not restored: %q", asset.Key, actual)
		}
	}
	for _, legacy := range runtime.edgePolicyLegacyAssets() {
		if _, err := os.Stat(legacy.Target); err != nil {
			t.Fatalf("legacy target %s was not restored: %v", legacy.Key, err)
		}
	}
}
