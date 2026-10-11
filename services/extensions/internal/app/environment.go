package app

import "fmt"

// validateEnvironment rejects configuration for the retired monolith or the
// Rust database before opening files, sockets, or listeners. Extension modules
// must use their own explicitly named storage configuration, never core access.
// Report names only: connection strings and file paths can contain secrets.
func validateEnvironment(getenv func(string) string) error {
	if err := validateDatabaseEnvironment(getenv); err != nil {
		return err
	}
	for _, name := range [...]string{
		"LMM_EXTENSION_TOKEN_FILE", "LMM_EXTENSION_LISTEN", "LMM_EXTENSION_MODULES", "LMM_EXTENSION_WEB_COMPAT",
		"LMM_CORE_RPC_SOCKET", "LMM_CORE_RPC_TOKEN_FILE", "LMM_CORE_COMMERCE_RPC_TOKEN_FILE",
		"LMM_STORE_DATABASE_URL_FILE", "LMM_STORE_DATABASE_NAME", "LMM_SUPPORT_DATABASE_URL_FILE", "LMM_SUPPORT_DATABASE_NAME", "LMM_LEGAL_CONFIG_FILE",
	} {
		if getenv(name) != "" {
			return fmt.Errorf("%s is not supported by lmm-extensions; remove retired environment configuration and use the typed TOML file", name)
		}
	}
	return nil
}

func validateDatabaseEnvironment(getenv func(string) string) error {
	for _, name := range []string{"SQL_DSN", "LOG_SQL_DSN", "DATABASE_URL", "LMM_CORE_DATABASE_URL", "LMM_CORE_DATABASE_URL_FILE", "LMM_DB_MIGRATION_MODE"} {
		if getenv(name) != "" {
			return fmt.Errorf("%s is not supported by lmm-extensions; remove retired and core database configuration", name)
		}
	}
	return nil
}
