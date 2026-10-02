package model

import (
	"database/sql"

	"gorm.io/gorm"
)

// Market mutations serialize dynamic state through service and account locks.
// MySQL's default repeatable-read can retain a snapshot created before waiting
// for those locks. Read-committed makes subsequent authorization and counter
// reads see the committed state, without introducing a different lock order.
// The option stays local to market transactions; other dialects keep defaults.
func marketTransaction(db *gorm.DB, fn func(tx *gorm.DB) error) error {
	if db.Dialector.Name() == "mysql" {
		return db.Transaction(fn, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	}
	return db.Transaction(fn)
}
