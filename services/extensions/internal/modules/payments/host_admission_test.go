package payments

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
)

func TestBoundedAdmissionPreservesPaymentJobAndCredit(t *testing.T) {
	fixture := setup(t)
	o := fixture.create("create_fixture_0001")
	host, e := modules.NewHostWithLimit(nil, []byte(strings.Repeat("x", 32)), 1)
	if e != nil {
		t.Fatal(e)
	}
	_ = host.MarkReady()
	held, e := host.TryAcquire()
	if e != nil {
		t.Fatal(e)
	}
	run := func() error {
		release, e := host.TryAcquire()
		if e != nil {
			return e
		}
		defer release()
		return fixture.s.Work(context.Background(), 100)
	}
	if e = run(); !errors.Is(e, modules.ErrSaturated) {
		t.Fatal(e)
	}
	if fixture.a.checkoutCalls.Load() != 0 || fixture.c.count("credit") != 0 {
		t.Fatal("rejected admission changed funds")
	}
	held()
	if e = run(); e != nil {
		t.Fatal(e)
	}
	if fixture.a.checkoutCalls.Load() != 1 {
		t.Fatal("checkout job lost")
	}
	event := fixture.event(o, "evt_host_admission", Paid, 1000)
	if e = fixture.s.receiveEvent(context.Background(), fixture.a, event); e != nil {
		t.Fatal(e)
	}
	if e = run(); e != nil {
		t.Fatal(e)
	}
	if e = run(); e != nil {
		t.Fatal(e)
	}
	if !fixture.get(o.ID).Credited() || fixture.c.count("credit") != 1 {
		t.Fatal("credit recovery", fixture.c.count("credit"))
	}
	host.BeginDrain()
	if e = run(); !errors.Is(e, modules.ErrNotAccepting) {
		t.Fatal(e)
	}
	if fixture.c.count("credit") != 1 {
		t.Fatal("drain duplicated credit")
	}
}
