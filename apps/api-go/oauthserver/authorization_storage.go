package oauthserver

import "gorm.io/gorm"

// MaxAuthorizationRows bounds persisted browser state per issuer, including
// abandoned and consumed rows awaiting expiry cleanup. At capacity, new login
// flows fail closed; existing live transactions are never evicted to make room.
const MaxAuthorizationRows = 4096

// Use a separate advisory-lock namespace from one-key application locks.
const authorizationAdmissionLockNamespace = 0x4c4d4f41

func (s *Server) lockAuthorizationAdmission(tx *gorm.DB) (bool, error) {
	if tx.Dialector.Name() == "postgres" {
		var acquired bool
		err := tx.Raw("SELECT pg_catalog.pg_try_advisory_xact_lock(?, pg_catalog.hashtext(?))", authorizationAdmissionLockNamespace, s.issuer).Scan(&acquired).Error
		return acquired, err
	}
	// New accepts only PostgreSQL and SQLite. A first write, even with no
	// matching rows, takes SQLite's writer lock before cleanup/count/insert.
	// Do not replace this with a process-local mutex or a deferred read.
	err := tx.Exec("UPDATE oauth_server_authorizations SET lock_version = lock_version WHERE 1 = 0").Error
	return err == nil, err
}
