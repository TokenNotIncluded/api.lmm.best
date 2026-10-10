package app

import "fmt"

// validateEnvironment rejects configuration for the retired monolith or the
// Rust database before opening files, sockets, or listeners. Extension modules
// must use their own explicitly named storage configuration, never core access.
// Report names only: connection strings and file paths can contain secrets.
func validateEnvironment(getenv func(string) string) error {
	for _, name := range [...]string{
		"SQL_DSN",
		"LOG_SQL_DSN",
		"DATABASE_URL",
		"LMM_CORE_DATABASE_URL",
		"LMM_CORE_DATABASE_URL_FILE",
		"LMM_DB_MIGRATION_MODE",
	} {
		if getenv(name) != "" {
			return fmt.Errorf("%s is not supported by lmm-extensions; remove legacy and core database configuration from this process", name)
		}
	}
	return nil
}
