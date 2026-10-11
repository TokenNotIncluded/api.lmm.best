package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigArguments(t *testing.T) {
	path, err := configPath(nil)
	if err != nil || path != defaultConfigPath {
		t.Fatal(path, err)
	}
	for _, args := range [][]string{{"serve"}, {"--config"}, {"--config", "relative.toml"}, {"--config", "/tmp/../other"}, {"--config", "/tmp/a", "extra"}, {"--unknown", "/tmp/a"}} {
		if _, err := configPath(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if path, err = configPath([]string{"--config", "/tmp/extensions.toml"}); err != nil || path != "/tmp/extensions.toml" {
		t.Fatal(path, err)
	}
}
func TestStrictTypedConfig(t *testing.T) {
	base := "version=1\n[host]\ntoken_file='/tmp/host-token'\nmodules=['none']\nweb_compat=false\n"
	write := func(raw string) string {
		p := filepath.Join(t.TempDir(), "extensions.toml")
		if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cfg, err := LoadConfig(write(base))
	if err != nil || cfg.Host.TokenFile != "/tmp/host-token" || cfg.Host.Listen != "0.0.0.0:8081" {
		t.Fatal(cfg, err)
	}
	for _, raw := range []string{strings.Replace(base, "version=1", "version=2", 1), base + "unknown='private-value'\n", base + "modules=['identity']\n", strings.Replace(base, "[host]", "[Host]", 1), strings.Replace(base, "token_file=", "TokenFile=", 1), strings.Replace(base, "'/tmp/host-token'", "'relative'", 1), base + "[core]\nsocket='/tmp/core.sock'\n", base + "[store]\ndatabase_name='lmm_store_test'\n", base + "listen='bad-address'\n", `{"version":1}`} {
		_, err := LoadConfig(write(raw))
		if err == nil {
			t.Fatalf("accepted malformed config %q", raw)
		}
		if strings.Contains(err.Error(), "private-value") {
			t.Fatal("config value leaked")
		}
	}
	link := filepath.Join(t.TempDir(), "link.toml")
	if err := os.Symlink(write(base), link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(link); err == nil {
		t.Fatal("symlink config accepted")
	}
	writable := write(base)
	if err := os.Chmod(writable, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(writable); err == nil {
		t.Fatal("group/world writable configuration accepted")
	}
	if _, err := configPath([]string{"--legacy-env", "--config", "/tmp/file"}); err == nil {
		t.Fatal("mixed configuration modes accepted")
	}
	if p, err := configPath([]string{"--legacy-env"}); err != nil || p != "" {
		t.Fatal("explicit legacy mode rejected", err)
	}
}
