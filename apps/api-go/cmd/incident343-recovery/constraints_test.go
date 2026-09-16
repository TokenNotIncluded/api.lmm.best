//go:build linux

package main

import (
	"context"
	"strings"
	"testing"
)

func TestIncident343PostgresRejectsForeignIndexWithMatchingName(t *testing.T) {
	db, dsn, schema := incidentTestDB(t)
	for _, query := range []string{
		"CREATE TABLE " + schema + ".untouched (item_type text, source_id bigint)",
		"CREATE UNIQUE INDEX idx_red_packet_source ON " + schema + ".untouched (item_type, source_id)",
		"INSERT INTO " + schema + ".untouched VALUES ('sentinel', 1)",
	} {
		if err := db.Exec(query).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := createAbsentRedPacketSchema(context.Background(), dsn, schema); err == nil {
		t.Fatal("an unrelated index cannot satisfy a new table's uniqueness contract")
	}
	if n := targetRelationCount(t, db, schema); n != 0 {
		t.Fatalf("partial recovery escaped transaction: %d tables", n)
	}
	var value string
	if err := db.Raw("SELECT item_type FROM " + schema + ".untouched WHERE source_id=1").Scan(&value).Error; err != nil || value != "sentinel" {
		t.Fatal("unrelated table changed", err)
	}
}

func TestIncident343PostgresNewConstraintsRejectDuplicateClaims(t *testing.T) {
	db, dsn, schema := incidentTestDB(t)
	if err := createAbsentRedPacketSchema(context.Background(), dsn, schema); err != nil {
		t.Fatal(err)
	}
	// Each Exec runs in its own transaction. Distinct item IDs isolate the
	// per-user uniqueness constraint from the separate item ID constraint.
	query := "INSERT INTO " + schema + ".red_packet_claims (packet_id,user_id,claim_index,item_id,item_type,source_id,created_at) VALUES (1,2,1,3,'token',4,0)"
	if err := db.Exec(query).Error; err != nil {
		t.Fatal(err)
	}
	for _, duplicate := range []string{
		"(1,2,1,5,'token',6,0)",
		"(7,8,1,3,'token',9,0)",
	} {
		query = "INSERT INTO " + schema + ".red_packet_claims (packet_id,user_id,claim_index,item_id,item_type,source_id,created_at) VALUES " + duplicate
		if err := db.Exec(query).Error; err == nil || !strings.Contains(err.Error(), "23505") {
			t.Fatal("duplicate claim was not rejected by uniqueness", err)
		}
	}
}
