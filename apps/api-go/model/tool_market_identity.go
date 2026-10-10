package model

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Marketplace client identities are exact strings. MySQL's usual _ci/_ai
// collations must not let a personal client inherit another client's access.
// Only the parameter is cast, so the existing column/index/schema stays intact.
func marketExactTextExpr(tx *gorm.DB, column, value string) clause.Expr {
	sql := "? = ?"
	if tx.Dialector.Name() == "mysql" {
		sql = "? = CAST(? AS BINARY)"
	}
	return clause.Expr{SQL: sql, Vars: []any{clause.Column{Name: column}, value}}
}

func marketExactTextScope(column, value string) func(*gorm.DB) *gorm.DB {
	return func(tx *gorm.DB) *gorm.DB {
		return tx.Where(marketExactTextExpr(tx, column, value))
	}
}
