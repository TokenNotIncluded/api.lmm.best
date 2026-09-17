package deployslots

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func artifact(version, marker string) Artifact {
	return Artifact{Version: version, Revision: strings.Repeat(marker, 40), SHA256: strings.Repeat(marker, 64)}
}
func baseline(t *testing.T) State {
	t.Helper()
	s, err := New(Receipt{"baseline", strings.Repeat("a", 64), "CONFIRMED", Pair{artifact("1", "a"), artifact("1", "b")}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func stage(t *testing.T, s State, pair Pair) State {
	t.Helper()
	n, err := s.Stage(s.Generation, "update", strings.Repeat("c", 64), pair)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func begin(t *testing.T, s State) State {
	t.Helper()
	n, err := s.Begin(s.Generation, s.Pending.DeploymentID, s.Pending.PlanSHA256)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func finish(t *testing.T, s State, phase string, pair Pair) State {
	t.Helper()
	n, err := s.Finish(s.Generation, Receipt{s.Pending.DeploymentID, s.Pending.PlanSHA256, phase, pair})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPairUpdateAndRollback(t *testing.T) {
	initial := baseline(t)
	old := initial.Slots["A"]
	pair := Pair{artifact("2", "c"), artifact("2", "d")}
	staged := stage(t, initial, pair)
	if staged.Active != "A" || len(staged.Slots) != 1 {
		t.Fatal("staging changed confirmed slots")
	}
	confirmed := finish(t, begin(t, staged), "CONFIRMED", pair)
	if confirmed.Active != "B" || confirmed.Slots["A"] != old || confirmed.Slots["B"] != pair {
		t.Fatal("pair not preserved")
	}
	rollback, err := confirmed.StageRollback(confirmed.Generation, "rollback", strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	restored := finish(t, begin(t, rollback), "CONFIRMED", old)
	if restored.Active != "A" || restored.Slots["B"] != pair {
		t.Fatal("rollback lost a confirmed pair")
	}
	if initial.Pending != nil || len(initial.Slots) != 1 || staged.Pending.Started {
		t.Fatal("transition mutated its input")
	}
}

func TestSingleComponentStillRequiresPairConfirmation(t *testing.T) {
	for _, component := range []string{"go", "web"} {
		t.Run(component, func(t *testing.T) {
			s := baseline(t)
			pair := s.Slots["A"]
			if component == "go" {
				pair.Go = artifact("2", "c")
			} else {
				pair.Web = artifact("2", "d")
			}
			started := begin(t, stage(t, s, pair))
			wrong := pair
			if component == "go" {
				wrong.Web = artifact("3", "e")
			} else {
				wrong.Go = artifact("3", "e")
			}
			if _, err := started.Finish(started.Generation, Receipt{"update", strings.Repeat("c", 64), "CONFIRMED", wrong}); err == nil {
				t.Fatal("mixed pair accepted")
			}
			finish(t, started, "CONFIRMED", pair)
		})
	}
}

func TestFailurePreservesActiveAndBlocksAnotherWriter(t *testing.T) {
	s := baseline(t)
	old := s.Slots["A"]
	started := begin(t, stage(t, s, Pair{artifact("2", "c"), artifact("2", "d")}))
	for _, phase := range []string{"", "STAGED", "OBSERVING", "AWAITING_CONFIRMATION", "ROLLBACK_REQUIRED", "HTTP_200", "failure", "cancelled"} {
		if _, err := started.Finish(started.Generation, Receipt{"update", strings.Repeat("c", 64), phase, old}); err == nil {
			t.Fatal("nonterminal accepted", phase)
		}
	}
	if _, err := started.Stage(started.Generation, "another", strings.Repeat("e", 64), old); err == nil {
		t.Fatal("parallel plan accepted")
	}
	if _, err := started.AbortStage(started.Generation, "update", strings.Repeat("c", 64)); err == nil {
		t.Fatal("uncertain activation was erased")
	}
	recovered := finish(t, started, "ROLLED_BACK", old)
	if recovered.Active != "A" || len(recovered.Slots) != 1 || recovered.Pending != nil {
		t.Fatal("failed release promoted")
	}
}

func TestEvidenceIsBoundToPlanAndGeneration(t *testing.T) {
	s := baseline(t)
	pair := Pair{artifact("2", "c"), artifact("2", "d")}
	staged := stage(t, s, pair)
	started := begin(t, staged)
	for _, r := range []Receipt{
		{"different", strings.Repeat("c", 64), "CONFIRMED", pair},
		{"update", strings.Repeat("e", 64), "CONFIRMED", pair},
		{"update", strings.Repeat("c", 64), "ROLLED_BACK", pair},
	} {
		if _, err := started.Finish(started.Generation, r); err == nil {
			t.Fatal("unrelated evidence accepted")
		}
	}
	if _, err := staged.Begin(s.Generation, "update", strings.Repeat("c", 64)); err == nil {
		t.Fatal("stale generation accepted")
	}
	if _, err := staged.Finish(staged.Generation, Receipt{"update", strings.Repeat("c", 64), "CONFIRMED", pair}); err == nil {
		t.Fatal("confirmation before begin")
	}
	confirmed := finish(t, started, "CONFIRMED", pair)
	if _, err := confirmed.Finish(confirmed.Generation, Receipt{"update", strings.Repeat("c", 64), "CONFIRMED", pair}); err == nil {
		t.Fatal("replay accepted")
	}
}

func TestAbortOnlyBeforeActivationAndNoPhantomRollback(t *testing.T) {
	s := baseline(t)
	if _, err := s.StageRollback(s.Generation, "rollback", strings.Repeat("c", 64)); err == nil {
		t.Fatal("unconfirmed rollback slot")
	}
	staged := stage(t, s, Pair{artifact("2", "c"), artifact("2", "d")})
	aborted, err := staged.AbortStage(staged.Generation, "update", strings.Repeat("c", 64))
	if err != nil || aborted.Pending != nil || !reflect.DeepEqual(s.Slots, aborted.Slots) {
		t.Fatal("unsafe abort", err)
	}
}

func TestInvalidStateOrIdentityFailsClosed(t *testing.T) {
	s := baseline(t)
	for _, version := range []string{"", "../x", "1;restart", "1\nx"} {
		p := s.Slots["A"]
		p.Go.Version = version
		if _, err := s.Stage(s.Generation, "update", strings.Repeat("c", 64), p); err == nil {
			t.Fatal("invalid version accepted")
		}
	}
	for _, id := range []string{"", "../x", "a/b", "run; echo", strings.Repeat("x", 81)} {
		if _, err := s.Stage(s.Generation, id, strings.Repeat("c", 64), Pair{artifact("2", "c"), artifact("2", "d")}); err == nil {
			t.Fatal("invalid deployment accepted")
		}
	}
	if _, err := s.Stage(s.Generation, "update", strings.Repeat("c", 64), s.Slots["A"]); err == nil {
		t.Fatal("no-op update accepted")
	}
	s.Generation = ^uint64(0)
	if _, err := s.Stage(s.Generation, "update", strings.Repeat("c", 64), Pair{artifact("2", "c"), artifact("2", "d")}); err == nil {
		t.Fatal("generation overflow")
	}
	for _, mutate := range []func(*State){
		func(s *State) { s.Active = "C" }, func(s *State) { delete(s.Slots, "A") },
		func(s *State) { s.Format = 2 }, func(s *State) { s.Slots["C"] = s.Slots["A"] },
	} {
		broken := baseline(t)
		mutate(&broken)
		if broken.Validate() == nil {
			t.Fatal("invalid persisted state accepted")
		}
	}
}

func TestCrashStateRoundTripsWithoutClaimingSuccess(t *testing.T) {
	s := baseline(t)
	started := begin(t, stage(t, s, Pair{artifact("2", "c"), artifact("2", "d")}))
	data, err := json.Marshal(started)
	if err != nil {
		t.Fatal(err)
	}
	var restored State
	if err = json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if err = restored.Validate(); err != nil {
		t.Fatal(err)
	}
	if !restored.Pending.Started || restored.Active != "A" {
		t.Fatal("uncertain writer relabelled healthy")
	}
	finish(t, restored, "ROLLED_BACK", s.Slots["A"])
}

func TestRepeatedUpdatesKeepExactlyTwoConfirmedPairs(t *testing.T) {
	s := baseline(t)
	for i, marker := range []string{"c", "d", "e", "f", "a", "b"} {
		old := s.Slots[s.Active]
		pair := Pair{artifact("2", marker), artifact("3", marker)}
		s = finish(t, begin(t, stage(t, s, pair)), "CONFIRMED", pair)
		if len(s.Slots) != 2 || s.Slots[other(s.Active)] != old {
			t.Fatalf("lost previous pair at %d", i)
		}
	}
}
