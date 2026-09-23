package model

const legacyPasskeyUserUniqueIndex = "idx_passkey_credentials_user_id"

// The old one-credential-per-user index must be removed before AutoMigrate
// creates the separate non-unique owner index.
func migratePasskeyCredentialUserIndex() error {
	if !DB.Migrator().HasTable(&PasskeyCredential{}) ||
		!DB.Migrator().HasIndex(&PasskeyCredential{}, legacyPasskeyUserUniqueIndex) {
		return nil
	}
	return DB.Migrator().DropIndex(&PasskeyCredential{}, legacyPasskeyUserUniqueIndex)
}
