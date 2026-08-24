package appcli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	defaultNginxRoot      = "/etc/nginx"
	defaultEdgeAssetRoot  = "/usr/share/lmm-api-go/edge-policy"
	edgeBackupFormat      = 1
	edgePolicyBackupLimit = 4 << 20
)

type edgePolicyAsset struct {
	Key    string
	Source string
	Target string
	Mode   os.FileMode
}

type edgePolicyBackupEntry struct {
	Key    string      `json:"key"`
	State  string      `json:"state"`
	Mode   os.FileMode `json:"mode,omitempty"`
	SHA256 string      `json:"sha256,omitempty"`
}

type edgePolicyBackupManifest struct {
	Format  int                     `json:"format"`
	Entries []edgePolicyBackupEntry `json:"entries"`
}

type edgePolicyOptions struct {
	Action    string
	AssetRoot string
	BackupDir string
}

func (runtime *productionRuntime) edgePolicyAssets() []edgePolicyAsset {
	root := runtime.paths.NginxRoot
	unitRoot := runtime.paths.SystemdUnitRoot
	return []edgePolicyAsset{
		{Key: "http-map", Source: "nginx/http-map.conf", Target: filepath.Join(root, "lmm-api-http-map.conf"), Mode: 0o644},
		{Key: "locations", Source: "nginx/lmm-api-locations.conf", Target: filepath.Join(root, "lmm-api-locations.conf"), Mode: 0o644},
		{Key: "mime", Source: "nginx/mime.types", Target: filepath.Join(root, "lmm-api-mime.types"), Mode: 0o644},
		{Key: "server", Source: "nginx/new-api.conf", Target: filepath.Join(root, "conf.d", "new-api.conf"), Mode: 0o644},
		{Key: "region-policy", Source: "nginx/lmm-api-region-policy.conf", Target: filepath.Join(root, "lmm-api-region-policy.conf"), Mode: 0o644},
		{Key: "geoip-service", Source: "geoip2-country-update.service", Target: filepath.Join(unitRoot, "geoip2-country-update.service"), Mode: 0o644},
		{Key: "geoip-timer", Source: "geoip2-country-update.timer", Target: filepath.Join(unitRoot, "geoip2-country-update.timer"), Mode: 0o644},
	}
}

func (runtime *productionRuntime) edgePolicyLegacyAssets() []edgePolicyAsset {
	root := runtime.paths.NginxRoot
	// Derive the test/restore filesystem prefix from /etc/nginx. Production
	// uses /etc/nginx and therefore receives the real absolute paths; isolated
	// tests can point NginxRoot at <root>/etc/nginx without touching the host.
	prefix := filepath.Dir(filepath.Dir(root))
	if prefix == "." {
		prefix = string(filepath.Separator)
	}
	under := func(relative string) string {
		if prefix == string(filepath.Separator) {
			return filepath.Join(string(filepath.Separator), relative)
		}
		return filepath.Join(prefix, relative)
	}
	return []edgePolicyAsset{
		{Key: "legacy-http-region", Target: filepath.Join(root, "site-policy", "http", "cn-region.conf")},
		{Key: "legacy-api-region", Target: filepath.Join(root, "site-policy", "api.lmm.best", "cn-region-notice.conf")},
		{Key: "legacy-prefix-map", Target: filepath.Join(root, "lmm-api-cn-prefixes.conf")},
		{Key: "legacy-nft", Target: under("etc/nftables.d/cn-443-block.nft")},
		{Key: "legacy-geoip-script", Target: under("usr/local/sbin/update-geoip2-country")},
		{Key: "legacy-prefix-script", Target: under("usr/local/sbin/update-cn-443-block")},
		{Key: "legacy-prefix-service", Target: under("etc/systemd/system/cn-443-block.service")},
		{Key: "legacy-prefix-update-service", Target: under("etc/systemd/system/cn-443-block-update.service")},
		{Key: "legacy-prefix-update-timer", Target: under("etc/systemd/system/cn-443-block-update.timer")},
	}
}

func (runtime *productionRuntime) allEdgePolicyAssets() []edgePolicyAsset {
	assets := append([]edgePolicyAsset(nil), runtime.edgePolicyAssets()...)
	return append(assets, runtime.edgePolicyLegacyAssets()...)
}

func (runtime *productionRuntime) captureEdgePolicyBackup(root string) (string, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) == string(filepath.Separator) {
		return "", errors.New("edge-policy backup path is invalid")
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return "", errors.New("edge-policy backup path already exists")
		}
		return "", fmt.Errorf("inspect edge-policy backup path: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create edge-policy backup: %w", err)
	}
	entries := make([]edgePolicyBackupEntry, 0)
	for _, asset := range runtime.allEdgePolicyAssets() {
		info, err := os.Lstat(asset.Target)
		switch {
		case errors.Is(err, os.ErrNotExist):
			entries = append(entries, edgePolicyBackupEntry{Key: asset.Key, State: "absent"})
		case err != nil:
			return "", fmt.Errorf("inspect edge-policy target %s: %w", asset.Target, err)
		default:
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return "", fmt.Errorf("edge-policy target is not a regular file: %s", asset.Target)
			}
			backupFile := filepath.Join(root, asset.Key)
			if err := copyRegularFile(asset.Target, backupFile, info.Mode().Perm(), true); err != nil {
				return "", fmt.Errorf("backup edge-policy target %s: %w", asset.Target, err)
			}
			digest, err := sha256File(backupFile)
			if err != nil {
				return "", err
			}
			entries = append(entries, edgePolicyBackupEntry{Key: asset.Key, State: "present", Mode: info.Mode().Perm(), SHA256: digest})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	manifest := edgePolicyBackupManifest{Format: edgeBackupFormat, Entries: entries}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	if err := writeAtomicRegularFile(filepath.Join(root, "manifest.json"), append(encoded, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("write edge-policy backup manifest: %w", err)
	}
	digest, err := sha256File(filepath.Join(root, "manifest.json"))
	if err != nil {
		return "", err
	}
	return digest, nil
}

func (runtime *productionRuntime) validateEdgePolicyAssets(assetRoot string) error {
	if assetRoot == "" || !filepath.IsAbs(assetRoot) {
		return errors.New("edge-policy asset root must be absolute")
	}
	info, err := os.Lstat(assetRoot)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("edge-policy asset root is missing or unsafe")
	}
	for _, asset := range runtime.edgePolicyAssets() {
		source := filepath.Join(assetRoot, asset.Source)
		info, err := os.Lstat(source)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("edge-policy asset is missing or unsafe: %s", asset.Source)
		}
	}
	for _, check := range []struct {
		name string
		need string
	}{
		{name: "nginx/http-map.conf", need: "geoip2 /var/lib/geoip2/DBIP-Country-Lite.mmdb {"},
		{name: "nginx/new-api.conf", need: "include /etc/nginx/lmm-api-region-policy.conf;"},
		{name: "nginx/lmm-api-locations.conf", need: "error_page 418 = @lmm_api_cors_preflight;"},
		{name: "nginx/lmm-api-locations.conf", need: "location @lmm_api_cors_preflight {"},
		{name: "nginx/lmm-api-locations.conf", need: "auth_request off;"},
		{name: "nginx/lmm-api-locations.conf", need: "set $lmm_access_policy_original_uri $uri;"},
		{name: "nginx/lmm-api-locations.conf", need: "if ($request_method = OPTIONS) { return 418; }"},
		{name: "nginx/lmm-api-locations.conf", need: "add_header Access-Control-Allow-Methods $http_access_control_request_method always;"},
		{name: "nginx/lmm-api-locations.conf", need: "add_header Access-Control-Allow-Headers $http_access_control_request_headers always;"},
		{name: "nginx/lmm-api-locations.conf", need: "add_header Vary \"Origin, Access-Control-Request-Method, Access-Control-Request-Headers\" always;"},
		{name: "nginx/lmm-api-region-policy.conf", need: "auth_request /internal/access-ip-policy;"},
		{name: "nginx/lmm-api-region-policy.conf", need: "proxy_set_header X-LMM-Original-URI $lmm_access_policy_original_uri;"},
		{name: "nginx/lmm-api-region-policy.conf", need: "proxy_set_header X-LMM-Original-Accept $http_accept;"},
	} {
		content, err := os.ReadFile(filepath.Join(assetRoot, check.name))
		if err != nil || !strings.Contains(string(content), check.need) {
			return fmt.Errorf("edge-policy asset %s is missing required directive", check.name)
		}
	}
	return nil
}

func (runtime *productionRuntime) applyEdgePolicyAssets(ctx context.Context, assetRoot, backupDir string, backupAlreadyCaptured bool) (returnErr error) {
	backupCaptured := backupAlreadyCaptured
	defer func() {
		if returnErr == nil || !backupCaptured {
			return
		}
		if restoreErr := runtime.restoreEdgePolicyBackup(ctx, backupDir, ""); restoreErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("restore edge-policy after failed install: %w", restoreErr))
		}
	}()
	if err := runtime.validateEdgePolicyAssets(assetRoot); err != nil {
		return err
	}
	if backupDir == "" {
		backupDir = filepath.Join(runtime.paths.BackupRoot, "edge-policy", runtime.now().UTC().Format("20060102T150405Z"))
	}
	if !backupAlreadyCaptured {
		if _, err := runtime.captureEdgePolicyBackup(backupDir); err != nil {
			return err
		}
		backupCaptured = true
	}
	if err := runtime.rejectActiveLegacyPolicy(ctx); err != nil {
		return err
	}
	for _, asset := range runtime.edgePolicyAssets() {
		source := filepath.Join(assetRoot, asset.Source)
		if err := ensureRealDirectory(filepath.Dir(asset.Target), 0o755); err != nil {
			return fmt.Errorf("prepare edge-policy target directory: %w", err)
		}
		if err := atomicInstallRegularFile(source, asset.Target, asset.Mode); err != nil {
			return fmt.Errorf("install edge-policy asset %s: %w", asset.Key, err)
		}
	}
	for _, legacy := range runtime.edgePolicyLegacyAssets() {
		if info, err := os.Lstat(legacy.Target); err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return fmt.Errorf("legacy edge-policy target is unsafe: %s", legacy.Target)
			}
			if err := os.Remove(legacy.Target); err != nil {
				return fmt.Errorf("remove legacy edge-policy target %s: %w", legacy.Target, err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect legacy edge-policy target %s: %w", legacy.Target, err)
		}
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandNginx, Args: []string{"-t"}}); err != nil {
		return fmt.Errorf("validate managed nginx policy: %w", err)
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"reload", "nginx"}}); err != nil {
		return fmt.Errorf("reload nginx with managed policy: %w", err)
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"daemon-reload"}}); err != nil {
		return fmt.Errorf("reload systemd after managed policy: %w", err)
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"enable", "--now", "geoip2-country-update.timer"}}); err != nil {
		return fmt.Errorf("enable GeoIP update timer: %w", err)
	}
	return nil
}

func (runtime *productionRuntime) rejectActiveLegacyPolicy(ctx context.Context) error {
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"is-active", "--quiet", "geoip2-country-update.service"}}); err == nil {
		return errors.New("GeoIP update service is active; wait for it to finish before migration")
	}
	for _, unit := range []string{"cn-443-block.service", "cn-443-block-update.service", "cn-443-block-update.timer"} {
		if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"is-active", "--quiet", unit}}); err == nil {
			return fmt.Errorf("legacy policy unit is active; stop it before migration: %s", unit)
		}
		output, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"is-enabled", unit}})
		if err == nil && legacyPolicyUnitIsEnabled(output) {
			return fmt.Errorf("legacy policy unit is enabled; disable it before migration: %s", unit)
		}
	}
	return nil
}

func legacyPolicyUnitIsEnabled(output []byte) bool {
	switch strings.TrimSpace(string(output)) {
	case "enabled", "enabled-runtime", "linked", "linked-runtime", "alias", "generated":
		return true
	default:
		// systemctl reports inactive static units as "static" with a successful
		// exit status. Static means the unit can be started by another unit; it
		// does not mean that this legacy policy is enabled.
		return false
	}
}

func (runtime *productionRuntime) restoreEdgePolicyBackup(ctx context.Context, root, expectedDigest string) error {
	manifestPath := filepath.Join(root, "manifest.json")
	manifestBytes, err := readPrivateRegularFile(manifestPath, edgePolicyBackupLimit)
	if err != nil {
		return fmt.Errorf("read edge-policy restore manifest: %w", err)
	}
	if expectedDigest != "" && fmt.Sprintf("%x", sha256Bytes(manifestBytes)) != expectedDigest {
		return errors.New("edge-policy restore manifest changed after deployment was armed")
	}
	var manifest edgePolicyBackupManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil || manifest.Format != edgeBackupFormat {
		return errors.New("edge-policy restore manifest is invalid")
	}
	assets := make(map[string]edgePolicyAsset)
	for _, asset := range runtime.allEdgePolicyAssets() {
		assets[asset.Key] = asset
	}
	for _, entry := range manifest.Entries {
		asset, ok := assets[entry.Key]
		if !ok || entry.State != "present" && entry.State != "absent" {
			return errors.New("edge-policy restore manifest contains an unknown entry")
		}
		info, err := os.Lstat(asset.Target)
		if entry.State == "absent" {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return fmt.Errorf("unsafe edge-policy file while restoring absence: %s", asset.Target)
			}
			if err := os.Remove(asset.Target); err != nil {
				return fmt.Errorf("remove edge-policy file %s: %w", asset.Target, err)
			}
			continue
		}
		backupFile := filepath.Join(root, entry.Key)
		backupInfo, err := os.Lstat(backupFile)
		if err != nil || backupInfo.Mode()&os.ModeSymlink != 0 || !backupInfo.Mode().IsRegular() || backupInfo.Size() == 0 {
			return fmt.Errorf("edge-policy restore file is missing or unsafe: %s", entry.Key)
		}
		actual, err := sha256File(backupFile)
		if err != nil || actual != entry.SHA256 {
			return fmt.Errorf("edge-policy restore checksum mismatch: %s", entry.Key)
		}
		if err := ensureRealDirectory(filepath.Dir(asset.Target), 0o755); err != nil {
			return err
		}
		if err := atomicInstallRegularFile(backupFile, asset.Target, entry.Mode); err != nil {
			return fmt.Errorf("restore edge-policy file %s: %w", entry.Key, err)
		}
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandNginx, Args: []string{"-t"}}); err != nil {
		return fmt.Errorf("validate restored nginx policy: %w", err)
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"reload", "nginx"}}); err != nil {
		return fmt.Errorf("reload nginx after edge-policy restore: %w", err)
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"daemon-reload"}}); err != nil {
		return fmt.Errorf("reload systemd after edge-policy restore: %w", err)
	}
	return nil
}

func (runtime *productionRuntime) verifyEdgePolicy(ctx context.Context, assetRoot string) error {
	if err := runtime.validateEdgePolicyAssets(assetRoot); err != nil {
		return err
	}
	for _, asset := range runtime.edgePolicyAssets() {
		sourceDigest, err := sha256File(filepath.Join(assetRoot, asset.Source))
		if err != nil {
			return err
		}
		targetDigest, err := sha256File(asset.Target)
		if err != nil || sourceDigest != targetDigest {
			return fmt.Errorf("managed edge-policy asset drifted: %s", asset.Key)
		}
	}
	for _, legacy := range runtime.edgePolicyLegacyAssets() {
		if _, err := os.Lstat(legacy.Target); err == nil || !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("legacy edge-policy target remains: %s", legacy.Target)
		}
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandNginx, Args: []string{"-t"}}); err != nil {
		return fmt.Errorf("managed nginx policy validation failed: %w", err)
	}
	return nil
}

func atomicInstallRegularFile(source, target string, mode os.FileMode) (returnErr error) {
	if info, err := os.Lstat(source); err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("source is missing or unsafe")
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".*.new")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		if returnErr != nil {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		_ = temporary.Close()
		return err
	}
	_, copyErr := io.Copy(temporary, input)
	closeInputErr := input.Close()
	if copyErr != nil {
		_ = temporary.Close()
		return copyErr
	}
	if closeInputErr != nil {
		_ = temporary.Close()
		return closeInputErr
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(target))
}

func runProductionEdgePolicy(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "install" && args[0] != "verify") {
		_, _ = fmt.Fprintf(stderr, "%s deploy production edge-policy: choose install or verify\n", ProgramName)
		return ExitUsage
	}
	action := args[0]
	options := edgePolicyOptions{Action: action, AssetRoot: defaultEdgeAssetRoot}
	flags := flag.NewFlagSet("deploy production edge-policy "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.AssetRoot, "asset-root", options.AssetRoot, "package-managed edge-policy asset root")
	flags.StringVar(&options.BackupDir, "backup-dir", "", "private backup directory (install only)")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitUsage
	}
	if flags.NArg() != 0 {
		return ExitUsage
	}
	assetRoot, err := cleanAbsoluteNonRoot(options.AssetRoot)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production edge-policy: invalid asset root: %v\n", ProgramName, err)
		return ExitUsage
	}
	options.AssetRoot = assetRoot
	if options.BackupDir != "" {
		options.BackupDir, err = cleanAbsoluteNonRoot(options.BackupDir)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%s deploy production edge-policy: invalid backup dir: %v\n", ProgramName, err)
			return ExitUsage
		}
	}
	runtime := defaultProductionRuntime()
	if err := runtime.assertProductionMutation(); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production edge-policy: %v\n", ProgramName, err)
		return ExitError
	}
	status := "verified"
	err = runtime.withGlobalLock(context.Background(), func() error {
		if action == "verify" {
			if options.BackupDir != "" {
				return errors.New("--backup-dir is only valid with install")
			}
			return runtime.verifyEdgePolicy(context.Background(), options.AssetRoot)
		}
		backupDir := options.BackupDir
		if backupDir == "" {
			backupDir = filepath.Join(runtime.paths.BackupRoot, "edge-policy", runtime.now().UTC().Format("20060102T150405Z"))
		}
		if err := runtime.applyEdgePolicyAssets(context.Background(), options.AssetRoot, backupDir, false); err != nil {
			return err
		}
		options.BackupDir = backupDir
		return nil
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production edge-policy %s: %v\n", ProgramName, action, err)
		return ExitError
	}
	if options.BackupDir != "" {
		status = "installed"
		_, _ = fmt.Fprintf(stdout, "backup_dir=%s\n", options.BackupDir)
	}
	_, _ = fmt.Fprintf(stdout, "status=%s\n", status)
	return ExitOK
}
