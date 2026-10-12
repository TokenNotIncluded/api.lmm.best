package pgtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	p "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/payments"
)

// These are real Go processes and PostgreSQL commits. replayCore is still a
// simulated Core: this does NOT prove Rust verification, ledger or OS recovery.
const processOrderID = "top_process_recovery"

func processDB(t *testing.T, ctx context.Context) *sql.DB {
	t.Helper()
	dsn := os.Getenv("PAYMENT_TEST_DSN")
	if dsn == "" {
		t.Skip("PAYMENT_TEST_DSN is not configured; Go/PG process test NOT RUN")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal("cannot open disposable payment test database")
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(4)
	var name string
	if err := db.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil || name != "lmm_payments_test" {
		t.Fatal("refuse database outside the disposable payment test fixture")
	}
	return db
}

type processCheckpoint struct {
	PID     int    `json:"pid"`
	Stage   string `json:"stage"`
	Credits int    `json:"simulated_core_credits"`
	Receipt string `json:"receipt"`
}

func checkpoint(path string, value processCheckpoint) {
	raw, err := json.Marshal(value)
	if err != nil || os.WriteFile(path+".tmp", raw, 0600) != nil || os.Rename(path+".tmp", path) != nil {
		panic("cannot persist synthetic process checkpoint")
	}
}

type interruptInboxStore struct {
	p.Store
	stage, marker string
}

func (s *interruptInboxStore) pause(stage string) {
	if s.stage == stage {
		checkpoint(s.marker, processCheckpoint{PID: os.Getpid(), Stage: stage})
		// Only the parent that spawned this child may kill it. Do not emulate a
		// restart by returning an error or constructing another Service here.
		select {}
	}
}
func (s *interruptInboxStore) PutEvent(ctx context.Context, e p.Event) error {
	if err := s.Store.PutEvent(ctx, e); err != nil {
		return err
	}
	s.pause("after_inbox")
	return nil
}
func (s *interruptInboxStore) FinishEvent(ctx context.Context, key, status, code string, next time.Time) error {
	s.pause("after_job")
	return s.Store.FinishEvent(ctx, key, status, code, next)
}

func TestPaymentProcessHelper(t *testing.T) {
	stage := os.Getenv("LMM_PAYMENT_PROCESS_STAGE")
	if stage == "" {
		t.Skip("subprocess helper is launched by TestPostgresInboxProcessRecovery")
	}
	if stage != "after_inbox" && stage != "after_job" && stage != "recover" && stage != "replay" {
		t.Fatal("unknown process fixture stage")
	}
	marker := os.Getenv("LMM_PAYMENT_PROCESS_MARKER")
	if marker == "" {
		t.Fatal("missing private checkpoint path")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db := processDB(t, ctx)
	store, err := p.NewPostgres(ctx, db, "lmm_payments_test")
	if err != nil {
		t.Fatal("process storage isolation check failed")
	}
	core := &replayCore{}
	if stage == "after_inbox" || stage == "after_job" {
		o := p.Order{ID: processOrderID, ChannelID: 1, Namespace: "stripe/acct_test/test", Intent: p.Intent{ID: 701, AccountID: 42, CreatedBy: 7, Provider: "stripe", Merchant: "acct_test", Environment: "test", Currency: "USD", AmountMinor: 1000, Key: "process_fixture_0001", RequestHash: make([]byte, 32)}, CreditState: "pending", Refunds: map[string]p.Refund{}, Jobs: map[string]p.Job{}}
		if err := store.Insert(ctx, o); err != nil {
			t.Fatal("cannot persist process fixture order")
		}
		adapter := &replayAdapter{channel: p.Channel{ID: 1, Provider: "stripe", Merchant: "acct_test", Environment: "test", Currency: "USD", Enabled: true}, event: p.Event{OrderID: o.ID, Type: p.Paid, Currency: "USD", AmountMinor: 1000, Evidence: p.Evidence{Provider: "stripe", Merchant: "acct_test", Environment: "test", Transaction: "pi_process", EventID: "evt_process", Payload: []byte("synthetic_original_payload"), Signature: []byte("synthetic_original_signature"), Source: "webhook"}}}
		service, err := p.New(&interruptInboxStore{Store: store, stage: stage, marker: marker}, core, adapter)
		if err != nil || service.Receive(ctx, 1, httptest.NewRequest("POST", "/callbacks/1", nil)) != nil || service.Drain(ctx, 100) != nil {
			t.Fatal("process fixture did not reach its crash boundary")
		}
		t.Fatal("parent did not kill the interrupted process")
	}
	service, err := p.New(store, core) // No Go provider adapter after restart.
	if err != nil || service.Work(ctx, 100) != nil || service.Work(ctx, 100) != nil {
		t.Fatal("restarted process could not drain its durable inbox")
	}
	o, err := store.Get(ctx, processOrderID)
	if err != nil || !o.Credited() || len(o.Jobs) != 1 {
		t.Fatal("restarted process did not recover exactly one credit job")
	}
	want := 1
	if stage == "replay" {
		want = 0
	}
	if core.credits != want {
		t.Fatal("restarted process lost or repeated the simulated Core call")
	}
	checkpoint(marker, processCheckpoint{PID: os.Getpid(), Stage: stage, Credits: core.credits, Receipt: o.CreditReceipt})
}

func TestPostgresInboxProcessRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	db := processDB(t, ctx)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal("cannot locate current test executable")
	}
	for _, fault := range []string{"after_inbox", "after_job"} {
		t.Run(fault, func(t *testing.T) {
			// Same explicitly disposable database as the existing PG tests. No
			// parallel tests, application database, private dump or automatic DDL.
			if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS payment_events,payment_orders,payment_storage_guard; DROP SCHEMA IF EXISTS core_probe CASCADE`); err != nil {
				t.Fatal("cannot reset the disposable process fixture")
			}
			if _, err := db.ExecContext(ctx, p.Schema); err != nil {
				t.Fatal("cannot install the disposable process fixture")
			}
			store, err := p.NewPostgres(ctx, db, "lmm_payments_test")
			if err != nil {
				t.Fatal("process fixture storage guard failed")
			}
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal("cannot restrict process fixture directory")
			}
			var pids []int
			run := func(stage string, kill bool) processCheckpoint {
				t.Helper()
				marker := filepath.Join(dir, stage+".json")
				cmd := exec.CommandContext(ctx, executable, "-test.run=^TestPaymentProcessHelper$", "-test.timeout=20s")
				cmd.Env = append(os.Environ(), "LMM_PAYMENT_PROCESS_STAGE="+stage, "LMM_PAYMENT_PROCESS_MARKER="+marker)
				// Child output is not needed; a private, typed checkpoint is the
				// only result. In particular do not print DSNs or environments.
				if err := cmd.Start(); err != nil {
					t.Fatal("cannot start payment worker process")
				}
				t.Cleanup(func() {
					if cmd.ProcessState == nil {
						_ = cmd.Process.Kill()
						_ = cmd.Wait()
					}
				})
				deadline := time.Now().Add(8 * time.Second)
				var result processCheckpoint
				for {
					raw, err := os.ReadFile(marker)
					if err == nil && json.Unmarshal(raw, &result) == nil {
						break
					}
					if time.Now().After(deadline) || ctx.Err() != nil {
						t.Fatal("payment worker did not reach the required checkpoint")
					}
					time.Sleep(10 * time.Millisecond)
				}
				if result.PID != cmd.Process.Pid || result.Stage != stage {
					t.Fatal("checkpoint does not belong to the spawned process")
				}
				for _, pid := range pids {
					if result.PID == pid {
						t.Fatal("process restart reused a previous PID")
					}
				}
				pids = append(pids, result.PID)
				if kill {
					if err := cmd.Process.Kill(); err != nil {
						t.Fatal("cannot kill owned payment worker process")
					}
					if err := cmd.Wait(); err == nil || cmd.ProcessState.Success() {
						t.Fatal("crash fixture exited successfully instead of being killed")
					}
				} else if err := cmd.Wait(); err != nil {
					t.Fatal("restarted payment worker failed")
				}
				t.Logf("stage=%s pid=%d killed=%t simulated_core_calls=%d", stage, result.PID, kill, result.Credits)
				return result
			}
			run(fault, true)
			before, err := store.Get(ctx, processOrderID)
			if err != nil || before.Credited() {
				t.Fatal("killed process falsely finalized the payment")
			}
			jobs := 0
			if fault == "after_job" {
				jobs = 1
			}
			if len(before.Jobs) != jobs {
				t.Fatal("process was not killed at the requested persistence boundary")
			}
			pending, err := store.Events(ctx, time.Now(), 100)
			if err != nil || len(pending) != 1 || pending[0].Evidence.AcceptedProofID == "" {
				t.Fatal("killed process lost its accepted durable inbox")
			}
			recovered := run("recover", false)
			replayed := run("replay", false)
			if recovered.Credits != 1 || replayed.Credits != 0 || recovered.Receipt == "" || recovered.Receipt != replayed.Receipt {
				t.Fatal("repeated process restart changed the durable receipt")
			}
			var raw []byte
			var status string
			if err := db.QueryRowContext(ctx, `SELECT snapshot,status FROM payment_events WHERE event_key=$1`, pending[0].Key).Scan(&raw, &status); err != nil {
				t.Fatal("cannot read recovered inbox state")
			}
			var final p.Event
			if json.Unmarshal(raw, &final) != nil || status != "done" || final.Evidence.AcceptedProofID != pending[0].Evidence.AcceptedProofID || string(final.Evidence.Payload) != string(pending[0].Evidence.Payload) || string(final.Evidence.Signature) != string(pending[0].Evidence.Signature) {
				t.Fatal("process restart changed the original accepted evidence")
			}
		})
	}
}
