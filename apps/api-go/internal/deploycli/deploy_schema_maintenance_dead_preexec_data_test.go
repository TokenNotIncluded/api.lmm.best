//go:build !windows

package deploycli

import (
	"context"
	"strings"
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
	for _, deferred := range []bool{false, true} {
		name, trigger := "immediate", "CREATE TRIGGER change_money_after_owner_delete AFTER DELETE ON dead_pre.options FOR EACH ROW EXECUTE FUNCTION public.dead_pre_change_money()"
		if deferred {
			name, trigger = "deferred", "CREATE CONSTRAINT TRIGGER change_money_after_owner_delete AFTER DELETE ON dead_pre.options DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.dead_pre_change_money()"
		}
		t.Run(name+" delete trigger is refused before money or sequence effects", func(t *testing.T) {
			f.t = t
			f.reset()
			f.mutate(`GRANT UPDATE ON dead_pre.credits TO lmm_dead_pre_business;
CREATE SEQUENCE public.dead_pre_delete_audit;
GRANT USAGE,SELECT ON SEQUENCE public.dead_pre_delete_audit TO lmm_dead_pre_business;
CREATE FUNCTION public.dead_pre_change_money() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM nextval('public.dead_pre_delete_audit'); UPDATE dead_pre.credits SET quota=quota-1 WHERE id=1; RETURN OLD; END$$;
` + trigger)
			t.Cleanup(func() {
				f.mutate(`DROP TRIGGER change_money_after_owner_delete ON dead_pre.options; DROP FUNCTION public.dead_pre_change_money(); DROP SEQUENCE public.dead_pre_delete_audit`)
			})
			expected := f.snapshot()
			before := schemaMaintenanceDeadPreExecDataFixture(t, f)
			owner, raw := f.owner()
			f.seed(owner, raw)
			options, money := f.options(), f.money()
			data := productionSchemaMaintenanceDeadPreExecData{Before: before, OriginalOwnerRaw: raw}
			if err := f.lease.recoverSchemaMaintenanceDeadPreWorkerOwner(f.ctx, expected, raw, schemaMaintenanceDeadPreWorkerExactOwner, func(context.Context) error { return nil }, data); err == nil || !strings.Contains(err.Error(), "active user DELETE triggers or rules") {
				t.Fatalf("typed DELETE effects were not refused before execution: %v", err)
			}
			f.unchanged(options, money)
			var called bool
			if err := f.lease.connection.QueryRow(f.ctx, `SELECT is_called FROM public.dead_pre_delete_audit`).Scan(&called); err != nil || called {
				t.Fatalf("refused DELETE executed a non-transactional nextval: called=%t err=%v", called, err)
			}
		})
	}
	t.Run("delete rule is refused before owner delete", func(t *testing.T) {
		f.t = t
		f.reset()
		f.mutate(`CREATE RULE block_dead_pre_owner_delete AS ON DELETE TO dead_pre.options DO INSTEAD NOTHING`)
		t.Cleanup(func() { f.mutate(`DROP RULE block_dead_pre_owner_delete ON dead_pre.options`) })
		expected := f.snapshot()
		before := schemaMaintenanceDeadPreExecDataFixture(t, f)
		owner, raw := f.owner()
		f.seed(owner, raw)
		options, money := f.options(), f.money()
		data := productionSchemaMaintenanceDeadPreExecData{Before: before, OriginalOwnerRaw: raw}
		if err := f.lease.recoverSchemaMaintenanceDeadPreWorkerOwner(f.ctx, expected, raw, schemaMaintenanceDeadPreWorkerExactOwner, func(context.Context) error { return nil }, data); err == nil || !strings.Contains(err.Error(), "active user DELETE triggers or rules") {
			t.Fatalf("typed DELETE rule was not refused before execution: %v", err)
		}
		f.unchanged(options, money)
	})
}
