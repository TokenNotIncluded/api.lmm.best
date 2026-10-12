//go:build !windows

package deploycli

import (
	"context"
	"testing"
)

func schemaMaintenanceDeadPreExecDataFixture(t *testing.T, f *schemaMaintenanceDeadPreWorkerPGFixture) productionSchemaMaintenanceBackupSource {
	t.Helper()
	query, err := schemaMaintenanceBackupSourceQuery(f.contract.Schema)
	if err != nil {
		t.Fatal(err)
	}
	results, err := f.lease.connection.PgConn().Exec(f.ctx, query).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	for _, result := range results {
		if len(result.Rows) == 1 && len(result.Rows[0]) == 1 {
			if raw != nil {
				t.Fatal("backup fixture returned multiple data projections")
			}
			raw = result.Rows[0][0]
		}
	}
	source, err := decodeSchemaMaintenanceBackupSource(raw)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

// Uses only the existing opt-in owned Unix-socket PostgreSQL fixture. It
// never accepts application connection settings or a production database.
func TestSchemaMaintenanceDeadPreExecDataActualPostgres(t *testing.T) {
	f := newSchemaMaintenanceDeadPreWorkerPGFixture(t)
	t.Run("exact original owner removed with matching signed before data", func(t *testing.T) {
		f.t = t
		f.reset()
		expected := f.snapshot()
		before := schemaMaintenanceDeadPreExecDataFixture(t, f)
		owner, raw := f.owner()
		f.seed(owner, raw)
		money, normal := f.money(), f.normalOptions()
		data := productionSchemaMaintenanceDeadPreExecData{Before: before, OriginalOwnerRaw: raw}
		if err := f.lease.recoverSchemaMaintenanceDeadPreWorkerOwner(f.ctx, expected, raw, schemaMaintenanceDeadPreWorkerExactOwner, func(context.Context) error { return nil }, data); err != nil {
			t.Fatal(err)
		}
		if f.money() != money || f.normalOptions() != normal || f.snapshot() != expected || f.options() != normal {
			t.Fatal("typed data-qualified CAS changed old business rows or retained its owner")
		}
	})
	t.Run("changed old money rejects before delete", func(t *testing.T) {
		f.t = t
		f.reset()
		expected := f.snapshot()
		before := schemaMaintenanceDeadPreExecDataFixture(t, f)
		owner, raw := f.owner()
		f.seed(owner, raw)
		f.mutate(`UPDATE dead_pre.credits SET quota=quota-1 WHERE id=1`)
		options, money := f.options(), f.money()
		data := productionSchemaMaintenanceDeadPreExecData{Before: before, OriginalOwnerRaw: raw}
		if err := f.lease.recoverSchemaMaintenanceDeadPreWorkerOwner(f.ctx, expected, raw, schemaMaintenanceDeadPreWorkerExactOwner, func(context.Context) error { return nil }, data); err == nil {
			t.Fatal("typed data gate ignored changed old money")
		}
		f.unchanged(options, money)
	})
	t.Run("delete trigger changes old money and rolls owner and money back", func(t *testing.T) {
		f.t = t
		f.reset()
		f.mutate(`GRANT UPDATE ON dead_pre.credits TO lmm_dead_pre_business;
CREATE FUNCTION public.dead_pre_change_money() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN UPDATE dead_pre.credits SET quota=quota-1 WHERE id=1; RETURN OLD; END$$;
CREATE TRIGGER change_money_after_owner_delete AFTER DELETE ON dead_pre.options FOR EACH ROW EXECUTE FUNCTION public.dead_pre_change_money()`)
		t.Cleanup(func() {
			f.mutate(`DROP TRIGGER change_money_after_owner_delete ON dead_pre.options; DROP FUNCTION public.dead_pre_change_money()`)
		})
		expected := f.snapshot()
		before := schemaMaintenanceDeadPreExecDataFixture(t, f)
		owner, raw := f.owner()
		f.seed(owner, raw)
		options, money := f.options(), f.money()
		data := productionSchemaMaintenanceDeadPreExecData{Before: before, OriginalOwnerRaw: raw}
		if err := f.lease.recoverSchemaMaintenanceDeadPreWorkerOwner(f.ctx, expected, raw, schemaMaintenanceDeadPreWorkerExactOwner, func(context.Context) error { return nil }, data); err == nil {
			t.Fatal("typed data gate ignored money changed by the exact owner DELETE")
		}
		f.unchanged(options, money)
	})
}
