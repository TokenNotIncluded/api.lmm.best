package model

import (
	"errors"
	"fmt"

	"github.com/LIghtJUNction/api.lmm.best/common"
)

// EnsureRedPacketSchemaAtStartup extends the normal startup migration contract
// without doing schema writes from request handlers. In apply mode only the
// master node mutates the schema. Verify mode is read-only and fails closed when
// a required table/column is absent.
func EnsureRedPacketSchemaAtStartup() error {
	if DB == nil {
		return nil
	}
	mode, err := databaseMigrationModeFromEnv()
	if err != nil {
		return err
	}
	models := []interface{}{&RedPacket{}, &RedPacketItem{}, &RedPacketClaim{}}
	if mode == DBMigrationModeApply {
		if !common.IsMasterNode {
			return nil
		}
		if err := DB.AutoMigrate(models...); err != nil {
			return fmt.Errorf("migrate red packet schema: %w", err)
		}
		return nil
	}
	if mode != DBMigrationModeVerify {
		return errors.New("unsupported red packet migration mode")
	}

	required := []struct {
		model   interface{}
		name    string
		columns []string
	}{
		{&RedPacket{}, "red_packets", []string{"id", "slug", "title", "description", "cover_image", "cover_prompt", "draw_mode", "per_user_limit", "start_at", "end_at", "enabled", "created_by", "created_at", "updated_at"}},
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
