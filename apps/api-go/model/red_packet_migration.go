package model

import "fmt"

// EnsureRedPacketSchemaAtStartup verifies the route's schema without changing it.
// mainMigrationModels owns table creation and verification before routes are
// registered. DDL here would escape the startup advisory lock and
// hide omissions from the standalone migrate --apply / --verify contract.
func EnsureRedPacketSchemaAtStartup() error {
	if DB == nil {
		return nil
	}
	if _, err := databaseMigrationModeFromEnv(); err != nil {
		return err
	}

	required := []struct {
		model   interface{}
		name    string
		columns []string
	}{
		{&RedPacket{}, "red_packets", []string{"id", "slug", "title", "description", "cover_image", "cover_prompt", "draw_mode", "per_user_limit", "start_at", "end_at", "enabled", "created_by", "created_at", "updated_at", "deleted_at"}},
		{&RedPacketItem{}, "red_packet_items", []string{"id", "packet_id", "item_type", "source_id", "weight", "claimed_by", "claimed_at", "created_at"}},
		{&RedPacketClaim{}, "red_packet_claims", []string{"id", "packet_id", "user_id", "claim_index", "item_id", "item_type", "source_id", "created_at"}},
	}
	for _, table := range required {
		if !DB.Migrator().HasTable(table.model) {
			return fmt.Errorf("red packet schema verification failed: missing table %s", table.name)
		}
		for _, column := range table.columns {
			if !DB.Migrator().HasColumn(table.model, column) {
				return fmt.Errorf("red packet schema verification failed: missing %s.%s", table.name, column)
			}
		}
	}
	return nil
}
