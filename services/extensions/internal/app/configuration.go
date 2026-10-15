package app

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
)

const defaultConfigPath = "/etc/lmm.conf.d/extensions.toml"

type GoConfig struct {
	Version    int             `toml:"version"`
	Host       HostConfig      `toml:"host"`
	Core       CoreConfig      `toml:"core"`
	Store      DatabaseConfig  `toml:"store"`
	Support    DatabaseConfig  `toml:"support"`
	Legal      LegalConfig     `toml:"legal"`
	Assistant  AssistantConfig `toml:"assistant"`
	Operations DatabaseConfig  `toml:"operations"`
	Payments   PaymentsConfig  `toml:"payments"`
	Scripts    ScriptConfig    `toml:"scripts"`
}
type HostConfig struct {
	MaxInFlight int      `toml:"max_in_flight"`
	Listen      string   `toml:"listen"`
	TokenFile   string   `toml:"token_file"`
	Modules     []string `toml:"modules"`
	WebCompat   bool     `toml:"web_compat"`
}
type CoreConfig struct {
	Socket            string `toml:"socket"`
	IdentityTokenFile string `toml:"identity_token_file"`
	CommerceTokenFile string `toml:"commerce_token_file"`
	PaymentTokenFile  string `toml:"payment_token_file"`
}
type DatabaseConfig struct {
	DatabaseName    string `toml:"database_name"`
	DatabaseURLFile string `toml:"database_url_file"`
}
type LegalConfig struct {
	ConfigFile string `toml:"config_file"`
}
type AssistantConfig struct {
	DatabaseName    string `toml:"database_name"`
	DatabaseURLFile string `toml:"database_url_file"`
	CursorKeyFile   string `toml:"cursor_key_file"`
}

type ScriptConfig struct {
	Root     string           `toml:"root"`
	Manifest []ReviewedScript `toml:"manifest"`
}
type ReviewedScript struct {
	Name   string `toml:"name"`
	SHA256 string `toml:"sha256"`
}

func configPath(args []string) (string, error) {
	if len(args) == 1 && args[0] == "--legacy-env" {
		return "", nil
	}
	if len(args) == 0 {
		return defaultConfigPath, nil
	}
	if len(args) == 2 && args[0] == "--config" && absolutePath(args[1]) {
		return args[1], nil
	}
	return "", errors.New("lmm-extensions accepts no subcommands; use --config with one absolute TOML file path or explicit --legacy-env")
}

func legacyConfig(getenv func(string) string) (GoConfig, error) {
	if err := validateDatabaseEnvironment(getenv); err != nil {
		return GoConfig{}, err
	}
	web := getenv("LMM_EXTENSION_WEB_COMPAT")
	if web != "" && web != "true" && web != "false" {
		return GoConfig{}, errors.New("invalid legacy Web compatibility option")
	}
	var selected []string
	if raw := getenv("LMM_EXTENSION_MODULES"); raw != "" {
		selected = strings.Split(raw, ",")
	}
	return GoConfig{Version: 1, Host: HostConfig{MaxInFlight: modules.DefaultMaxInFlight, Listen: getenv("LMM_EXTENSION_LISTEN"), TokenFile: getenv("LMM_EXTENSION_TOKEN_FILE"), Modules: selected, WebCompat: web == "true"}, Core: CoreConfig{Socket: getenv("LMM_CORE_RPC_SOCKET"), IdentityTokenFile: getenv("LMM_CORE_RPC_TOKEN_FILE"), CommerceTokenFile: getenv("LMM_CORE_COMMERCE_RPC_TOKEN_FILE")}, Store: DatabaseConfig{DatabaseName: getenv("LMM_STORE_DATABASE_NAME"), DatabaseURLFile: getenv("LMM_STORE_DATABASE_URL_FILE")}, Support: DatabaseConfig{DatabaseName: getenv("LMM_SUPPORT_DATABASE_NAME"), DatabaseURLFile: getenv("LMM_SUPPORT_DATABASE_URL_FILE")}, Legal: LegalConfig{ConfigFile: getenv("LMM_LEGAL_CONFIG_FILE")}}, nil
}
func absolutePath(path string) bool {
	return path != "" && filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsAny(path, "\x00\r\n")
}

func LoadConfig(path string) (GoConfig, error) {
	var cfg GoConfig
	if !absolutePath(path) {
		return cfg, errors.New("extension configuration path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil || info == nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() > 64<<10 {
		return cfg, errors.New("cannot read bounded regular extension TOML configuration")
	}
	f, err := os.Open(path)
	if err != nil {
		return cfg, errors.New("cannot read extension TOML configuration")
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	closeErr := f.Close()
	if readErr != nil || closeErr != nil || len(raw) > 64<<10 || !utf8.Valid(raw) {
		return cfg, errors.New("invalid extension TOML configuration file")
	}
	meta, err := toml.NewDecoder(bytes.NewReader(raw)).Decode(&cfg)
	if err != nil || len(meta.Undecoded()) != 0 {
		return cfg, errors.New("invalid or unknown extension TOML configuration field")
	}
	// The decoder's struct matching also accepts case variants. Only canonical
	// names belong to this versioned operator contract.
	allowed := map[string]bool{"version": true, "host": true, "host.listen": true, "host.token_file": true, "host.modules": true, "host.web_compat": true, "host.max_in_flight": true, "core": true, "core.socket": true, "core.identity_token_file": true, "core.commerce_token_file": true, "store": true, "store.database_name": true, "store.database_url_file": true, "support": true, "support.database_name": true, "support.database_url_file": true, "legal": true, "legal.config_file": true}
	for _, key := range meta.Keys() {
		if paymentConfigurationKey(key.String()) {
			continue
		}
		switch key.String() {
		case "assistant", "assistant.database_name", "assistant.database_url_file", "assistant.cursor_key_file",
			"operations", "operations.database_name", "operations.database_url_file",
			"scripts", "scripts.root", "scripts.manifest", "scripts.manifest.name", "scripts.manifest.sha256":
			continue
		}
		if !allowed[key.String()] {
			return cfg, errors.New("unknown extension TOML configuration field")
		}
	}
	if cfg.Version != 1 || !absolutePath(cfg.Host.TokenFile) {
		return cfg, errors.New("extension TOML requires version 1 and an absolute host token_file")
	}
	if !meta.IsDefined("host", "max_in_flight") {
		cfg.Host.MaxInFlight = modules.DefaultMaxInFlight
	}
	if cfg.Host.MaxInFlight < 1 || cfg.Host.MaxInFlight > modules.MaximumMaxInFlight {
		return cfg, errors.New("extension max_in_flight must be between 1 and 64")
	}
	if cfg.Host.Listen == "" {
		cfg.Host.Listen = "0.0.0.0:8081"
	}
	if _, _, err = net.SplitHostPort(cfg.Host.Listen); err != nil {
		return cfg, errors.New("invalid extension host listen address")
	}
	for _, p := range []string{cfg.Core.Socket, cfg.Core.IdentityTokenFile, cfg.Core.CommerceTokenFile, cfg.Store.DatabaseURLFile, cfg.Support.DatabaseURLFile, cfg.Legal.ConfigFile, cfg.Assistant.DatabaseURLFile, cfg.Assistant.CursorKeyFile, cfg.Operations.DatabaseURLFile, cfg.Scripts.Root} {
		if p != "" && !absolutePath(p) {
			return cfg, errors.New("extension TOML file and socket paths must be absolute")
		}
	}
	if (cfg.Core.Socket == "") != (cfg.Core.IdentityTokenFile == "") {
		return cfg, errors.New("configure both core socket and identity_token_file")
	}
	for _, db := range []DatabaseConfig{cfg.Store, cfg.Support, cfg.Operations} {
		if (db.DatabaseName == "") != (db.DatabaseURLFile == "") {
			return cfg, errors.New("configure both extension database_name and database_url_file")
		}
	}
	if cfg.Scripts.Root != "" || cfg.Scripts.Manifest != nil {
		if cfg.Scripts.Root == "" || cfg.Scripts.Manifest == nil {
			return cfg, errors.New("configure both scripts root and an explicit reviewed manifest")
		}
	}
	if err := validatePaymentsConfiguration(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
