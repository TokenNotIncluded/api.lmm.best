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
func TestOperationsTypedConfig(t *testing.T) {
	base := "version=1\n[host]\ntoken_file='/tmp/host-token'\nmodules=['operations']\nweb_compat=true\n"
	releases := "[operations]\ndatabase_name='lmm_operations_test'\ndatabase_url_file='/tmp/operations.url'\n"
	scripts := "[scripts]\nroot='/tmp/reviewed-scripts'\n[[scripts.manifest]]\nname='install.sh'\nsha256='" + strings.Repeat("a", 64) + "'\n"
	write := func(raw string) string {
		path := filepath.Join(t.TempDir(), "extensions.toml")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	cfg, err := LoadConfig(write(base + releases + scripts))
	if err != nil || cfg.Operations.DatabaseName != "lmm_operations_test" || len(cfg.Scripts.Manifest) != 1 || cfg.Scripts.Manifest[0].Name != "install.sh" {
		t.Fatal(cfg, err)
	}
	cfg, err = LoadConfig(write(base + "[scripts]\nroot='/tmp/reviewed-scripts'\nmanifest=[]\n"))
	if err != nil || cfg.Scripts.Manifest == nil {
		t.Fatal("explicitly reviewed empty catalogue was lost", cfg, err)
	}
	for _, raw := range []string{
		base + "[operations]\ndatabase_name='lmm_operations_test'\n",
		base + "[operations]\ndatabase_url_file='/tmp/operations.url'\n",
		base + "[scripts]\nroot='/tmp/reviewed-scripts'\n",
		base + "[scripts]\nmanifest=[]\n",
		base + strings.Replace(scripts, "/tmp/reviewed-scripts", "relative", 1),
		base + strings.Replace(scripts, "sha256=", "SHA256=", 1),
		base + strings.Replace(releases, "[operations]", "[Operations]", 1),
		base + scripts + "execute=true\n",
	} {
		if _, err := LoadConfig(write(raw)); err == nil {
			t.Fatalf("accepted invalid operations contract %q", raw)
		}
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

func TestHostAdmissionConfig(t *testing.T) {
	base := "version=1\n[host]\ntoken_file='/tmp/host-token'\nmodules=['none']\n"
	for _, tc := range []struct {
		value string
		want  int
	}{{"", 16}, {"max_in_flight=1\n", 1}, {"max_in_flight=64\n", 64}, {"max_in_flight=0\n", 0}, {"max_in_flight=-1\n", 0}, {"max_in_flight=65\n", 0}, {"max_in_flight='16'\n", 0}, {"Max_In_Flight=16\n", 0}} {
		p := filepath.Join(t.TempDir(), "extensions.toml")
		if e := os.WriteFile(p, []byte(base+tc.value), 0600); e != nil {
			t.Fatal(e)
		}
		cfg, e := LoadConfig(p)
		if tc.want == 0 {
			if e == nil {
				t.Fatal("accepted", tc.value)
			}
		} else if e != nil || cfg.Host.MaxInFlight != tc.want {
			t.Fatal(cfg, e)
		}
	}
	cfg, e := legacyConfig(func(string) string { return "" })
	if e != nil || cfg.Host.MaxInFlight != 16 {
		t.Fatal(cfg, e)
	}
}
