package appcli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionDeploymentDefaultsUseIndependentOperatorRoot(t *testing.T) {
	paths := defaultProductionPaths()
	if paths.WorkRoot != "/var/lib/lmm-api-go-deploy/work" || paths.BackupRoot != "/var/lib/lmm-api-go-deploy/backups" {
		t.Fatalf("deployment roots=(%q, %q)", paths.WorkRoot, paths.BackupRoot)
	}
	if paths.ReleasePackages != "/var/lib/lmm-api-go-deploy/release-packages" || paths.LegacyReleasePackages != "/var/lib/lmm-api-go/release-packages" {
		t.Fatalf("release package roots=(%q, %q)", paths.ReleasePackages, paths.LegacyReleasePackages)
	}
	if productionMemoryFileName != "20-memory.conf" || filepath.Join(paths.PackagedDropInDir, productionMemoryFileName) != "/usr/lib/systemd/system/lmm-api.service.d/20-memory.conf" {
		t.Fatalf("package-owned memory path=%q", filepath.Join(paths.PackagedDropInDir, productionMemoryFileName))
	}
	if paths.OperatorBinary != "/usr/bin/lmm-api-deploy" || paths.InstalledBinary != "/usr/bin/lmm-api" || paths.RunuserBinary != "/usr/bin/runuser" || paths.ParuBinary != "/usr/bin/paru" {
		t.Fatalf("production binaries=%#v", paths)
	}
}

func TestProductionReleaseSourceBuildPathIsHardDisabled(t *testing.T) {
	var stderr bytes.Buffer
	code := runProductionRelease([]string{"--confirm", productionExpectedHost}, &bytes.Buffer{}, &stderr)
	if code != ExitUsage || !strings.Contains(stderr.String(), "split lmm-api-go-bin and lmm-api-web-bin") || !strings.Contains(stderr.String(), "deploy production apply") {
		t.Fatalf("release exit=%d stderr=%q", code, stderr.String())
	}
}

func TestProductionDatabaseCommandKeepsPasswordOutOfArguments(t *testing.T) {
	databaseURL, environment, err := productionDatabaseCommand(map[string]string{
		"SQL_DSN": "postgres://app:p%40ssword@database.example/lmm?sslmode=require",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(databaseURL, "p%40ssword") || databaseURL != "postgres://app@database.example/lmm?sslmode=require" {
		t.Fatalf("database command URL contains credentials: %q", databaseURL)
	}
	if !containsString(environment, "PGPASSWORD=p@ssword") {
		t.Fatal("database password is not available to libpq")
	}
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func TestNativeProductionHardenAtomicallyPinsSecurityAndMemoryGuards(t *testing.T) {
	root := t.TempDir()
	envFile := filepath.Join(root, "etc", "lmm-api-go.env")
	dropInDir := filepath.Join(root, "usr", "lib", "systemd", "lmm-api.service.d")
	overrideDir := filepath.Join(root, "etc", "systemd", "lmm-api.service.d")
	if err := os.MkdirAll(filepath.Dir(envFile), 0o700); err != nil {
		t.Fatal(err)
	}
	original := "SQL_DSN=postgres://private\nPORT=3000\nPORT=3001\nSESSION_COOKIE_SECURE=false\nTRUSTED_PROXIES=0.0.0.0/0\n"
	if err := os.WriteFile(envFile, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyMemoryPath := filepath.Join(overrideDir, legacyEmergencyMemoryFile)
	legacyGuardPath := filepath.Join(overrideDir, legacyMemoryGuardFile)
	legacyProductionPath := filepath.Join(overrideDir, legacyProductionMemoryFile)
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	memoryPath := filepath.Join(dropInDir, productionMemoryFileName)
	if err := os.WriteFile(memoryPath, productionMemoryConfig(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyMemoryPath, []byte("[Service]\nMemoryHigh=256M\nMemoryMax=288M\nMemorySwapMax=64M\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{legacyGuardPath, legacyProductionPath} {
		if err := os.WriteFile(path, productionMemoryConfig(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	code := RunDeploy([]string{
		"production", "harden", "--env-file", envFile, "--drop-in-dir", dropInDir, "--override-drop-in-dir", overrideDir,
	}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("harden exit=%d stderr=%q", code, stderr.String())
	}
	environment, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"SQL_DSN=postgres://private",
		"SESSION_COOKIE_SECURE=true",
		"SESSION_COOKIE_TRUSTED_URL=https://api.lmm.best,https://lmm.best",
		"TRUSTED_PROXIES=127.0.0.1/32,::1/128",
	} {
		if !strings.Contains(string(environment), expected) {
			t.Errorf("environment missing %q: %q", expected, environment)
		}
	}
	if strings.Contains(string(environment), "SESSION_COOKIE_SECURE=false") || strings.Contains(string(environment), "0.0.0.0/0") {
		t.Fatalf("unsafe values survived hardening: %q", environment)
	}
	if strings.Count(string(environment), "PORT=") != 1 || !strings.Contains(string(environment), "PORT=3001") {
		t.Fatalf("duplicate environment assignments were not normalized: %q", environment)
	}
	envInfo, err := os.Stat(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if envInfo.Mode().Perm() != 0o600 {
		t.Fatalf("environment mode=%v", envInfo.Mode().Perm())
	}

	memory, err := os.ReadFile(memoryPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"MemoryHigh=" + productionMemoryHigh,
		"MemoryMax=" + productionMemoryMax,
		"MemorySwapMax=" + productionMemorySwapMax,
		"Environment=GOMEMLIMIT=" + productionGoMemoryLimit,
	} {
		if !strings.Contains(string(memory), expected) {
			t.Errorf("memory guard missing %q: %q", expected, memory)
		}
	}
	memoryInfo, err := os.Stat(memoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if memoryInfo.Mode().Perm() != 0o644 {
		t.Fatalf("memory drop-in mode=%v", memoryInfo.Mode().Perm())
	}
	for _, path := range []string{legacyMemoryPath, legacyGuardPath, legacyProductionPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("recognized legacy memory override remains at %s: %v", path, err)
		}
	}
	if _, err := os.Stat(legacyMemoryPath + ".disabled"); !os.IsNotExist(err) {
		t.Fatalf("legacy memory override was retained outside systemd loading: %v", err)
	}
	if stdout.String() != "configuration=hardened\nsystemd_reload_required=true\n" {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestNativeProductionHardenRejectsUnknownMemoryOverride(t *testing.T) {
	for _, test := range []struct{ name, filename, content string }{
		{name: "unknown filename", filename: "91-local-memory.conf", content: "[Service]\nMemoryMax=1G\n"},
		{name: "known filename with changed content", filename: legacyMemoryGuardFile, content: "[Service]\nMemoryHigh=320M\nMemoryMax=1G\nMemorySwapMax=256M\nEnvironment=GOMEMLIMIT=256MiB\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			envFile := filepath.Join(root, "lmm-api-go.env")
			packaged := filepath.Join(root, "usr", "lib", "systemd", "lmm-api.service.d")
			overrides := filepath.Join(root, "etc", "systemd", "lmm-api.service.d")
			for _, directory := range []string{packaged, overrides} {
				if err := os.MkdirAll(directory, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(envFile, []byte("SQL_DSN=private\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(packaged, productionMemoryFileName), productionMemoryConfig(), 0o644); err != nil {
				t.Fatal(err)
			}
			recognized := filepath.Join(overrides, legacyProductionMemoryFile)
			if err := os.WriteFile(recognized, productionMemoryConfig(), 0o644); err != nil {
				t.Fatal(err)
			}
			unknown := filepath.Join(overrides, test.filename)
			if err := os.WriteFile(unknown, []byte(test.content), 0o644); err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			code := RunDeploy([]string{"production", "harden", "--env-file", envFile, "--drop-in-dir", packaged, "--override-drop-in-dir", overrides}, &bytes.Buffer{}, &stderr)
			if code == ExitOK || !strings.Contains(stderr.String(), "unknown memory override") {
				t.Fatalf("unknown override exit=%d stderr=%q", code, stderr.String())
			}
			if _, err := os.Stat(unknown); err != nil {
				t.Fatalf("unknown override was removed: %v", err)
			}
			if _, err := os.Stat(recognized); err != nil {
				t.Fatalf("known override was partially removed before STOP: %v", err)
			}
		})
	}
}

func TestNativeProductionHardenRejectsSymlinkedSensitiveTargets(t *testing.T) {
	root := t.TempDir()
	realEnv := filepath.Join(root, "real.env")
	linkEnv := filepath.Join(root, "link.env")
	if err := os.WriteFile(realEnv, []byte("SQL_DSN=private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realEnv, linkEnv); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	code := RunDeploy([]string{
		"production", "harden", "--env-file", linkEnv, "--drop-in-dir", filepath.Join(root, "dropins"),
	}, &bytes.Buffer{}, &stderr)
	if code == ExitOK || !strings.Contains(stderr.String(), "real regular file") {
		t.Fatalf("symlink harden exit=%d stderr=%q", code, stderr.String())
	}
	content, err := os.ReadFile(realEnv)
	if err != nil || string(content) != "SQL_DSN=private\n" {
		t.Fatalf("real env changed: %q err=%v", content, err)
	}
}
