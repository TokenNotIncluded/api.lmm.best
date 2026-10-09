package accountfunding

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestResolveOrder(t *testing.T) {
	personal := Account{Kind: Personal, ID: 7}
	otherPerson := Account{Kind: Personal, ID: 8}
	teamA := Account{Kind: Team, ID: 7} // IDs may overlap across kinds.
	teamB := Account{Kind: Team, ID: 8}
	grants := map[int64]bool{teamA.ID: true, teamB.ID: true}

	tests := []struct {
		name   string
		owner  Account
		order  []Account
		grants map[int64]bool
		want   []Account
		err    error
	}{
		{name: "personal default", owner: personal, want: []Account{personal}},
		{name: "joining teams does not change default", owner: personal, grants: grants, want: []Account{personal}},
		{name: "personal first", owner: personal, order: []Account{personal, teamA, teamB}, grants: grants, want: []Account{personal, teamA, teamB}},
		{name: "team first", owner: personal, order: []Account{teamB, personal, teamA}, grants: grants, want: []Account{teamB, personal, teamA}},
		{name: "explicit teams only", owner: personal, order: []Account{teamB, teamA}, grants: grants, want: []Account{teamB, teamA}},
		{name: "team default", owner: teamA, grants: grants, want: []Account{teamA}},
		{name: "team explicit self", owner: teamA, order: []Account{teamA}, grants: grants, want: []Account{teamA}},
		{name: "personal empty is not default", owner: personal, order: []Account{}, err: ErrInvalidOrder},
		{name: "team empty is not default", owner: teamA, order: []Account{}, grants: grants, err: ErrInvalidOrder},
		{name: "missing owner", err: ErrInvalidOwner},
		{name: "unknown owner kind", owner: Account{Kind: "admin", ID: 7}, err: ErrInvalidOwner},
		{name: "negative owner ID", owner: Account{Kind: Personal, ID: -7}, err: ErrInvalidOwner},
		{name: "zero owner ID", owner: Account{Kind: Team, ID: 0}, err: ErrInvalidOwner},
		{name: "invalid entry", owner: personal, order: []Account{{Kind: "organization", ID: 7}}, grants: grants, err: ErrInvalidOrder},
		{name: "zero entry ID", owner: personal, order: []Account{{Kind: Team, ID: 0}}, grants: grants, err: ErrInvalidOrder},
		{name: "negative entry ID", owner: personal, order: []Account{{Kind: Team, ID: -7}}, grants: grants, err: ErrInvalidOrder},
		{name: "no implicit normalization", owner: personal, order: []Account{{Kind: "Team", ID: 7}}, grants: grants, err: ErrInvalidOrder},
		{name: "duplicate personal", owner: personal, order: []Account{personal, personal}, err: ErrInvalidOrder},
		{name: "duplicate team", owner: personal, order: []Account{teamA, personal, teamA}, grants: grants, err: ErrInvalidOrder},
		{name: "other personal account", owner: personal, order: []Account{otherPerson}, grants: grants, err: ErrAccountNotAuthorized},
		{name: "unjoined team", owner: personal, order: []Account{teamA}, err: ErrAccountNotAuthorized},
		{name: "revoked team grant", owner: personal, order: []Account{teamA}, grants: map[int64]bool{7: false}, err: ErrAccountNotAuthorized},
		{name: "invalid fallback rejects entire plan", owner: personal, order: []Account{personal, teamA}, err: ErrAccountNotAuthorized},
		{name: "team missing grant", owner: teamA, err: ErrAccountNotAuthorized},
		{name: "team revoked grant", owner: teamA, grants: map[int64]bool{7: false}, err: ErrAccountNotAuthorized},
		{name: "team cannot use personal", owner: teamA, order: []Account{personal}, grants: grants, err: ErrInvalidOrder},
		{name: "team cannot use another team", owner: teamA, order: []Account{teamB}, grants: grants, err: ErrInvalidOrder},
		{name: "team cannot add personal fallback", owner: teamA, order: []Account{teamA, personal}, grants: grants, err: ErrInvalidOrder},
		{name: "team cannot add team fallback", owner: teamA, order: []Account{teamA, teamB}, grants: grants, err: ErrInvalidOrder},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveOrder(tt.owner, tt.order, tt.grants)
			if !errors.Is(err, tt.err) {
				t.Fatalf("error = %v, want %v", err, tt.err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("order = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveOrderDoesNotMutateInputs(t *testing.T) {
	personal, team := Account{Kind: Personal, ID: 7}, Account{Kind: Team, ID: 8}
	input := []Account{team, personal}
	grants := map[int64]bool{8: true}
	result, err := ResolveOrder(personal, input, grants)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input, []Account{team, personal}) || !grants[8] || len(grants) != 1 {
		t.Fatal("inputs were modified")
	}
	result[0] = personal
	if input[0] != team {
		t.Fatal("result aliases caller's input")
	}
	input[1] = team
	if result[1] != personal {
		t.Fatal("caller mutation changed resolved order")
	}
	// Re-resolving must use the new grant state, not cached permission.
	grants[8] = false
	if got, err := ResolveOrder(personal, []Account{team}, grants); got != nil || !errors.Is(err, ErrAccountNotAuthorized) {
		t.Fatalf("revoked grant: order=%v error=%v", got, err)
	}
}

func TestResolveOrderJSONDefaultAndEmpty(t *testing.T) {
	owner := Account{Kind: Personal, ID: 7}
	for _, raw := range []string{`{}`, `{"order":null}`, `{"order":[]}`} {
		var input struct {
			Order []Account `json:"order"`
		}
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			t.Fatal(err)
		}
		got, err := ResolveOrder(owner, input.Order, nil)
		if raw == `{"order":[]}` {
			if got != nil || !errors.Is(err, ErrInvalidOrder) {
				t.Fatalf("empty order silently became default: %v, %v", got, err)
			}
		} else if err != nil || !reflect.DeepEqual(got, []Account{owner}) {
			t.Fatalf("omitted/null order: %v, %v", got, err)
		}
	}
}

func FuzzResolveOrder(f *testing.F) {
	for _, seed := range []string{
		`{"owner":{"kind":"personal","id":7}}`,
		`{"owner":{"kind":"team","id":7},"grants":{"7":true}}`,
		`{"owner":{"kind":"personal","id":7},"order":[]}`,
		`{"owner":{"kind":"personal","id":7},"order":[{"kind":"team","id":8},{"kind":"personal","id":7}],"grants":{"8":true}}`,
		`{"owner":{"kind":"team","id":7},"order":[{"kind":"team","id":7},{"kind":"personal","id":7}],"grants":{"7":true}}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		var input struct {
			Owner  Account        `json:"owner"`
			Order  []Account      `json:"order"`
			Grants map[int64]bool `json:"grants"`
		}
		if json.Unmarshal([]byte(raw), &input) != nil {
			return
		}
		got, err := ResolveOrder(input.Owner, input.Order, input.Grants)
		if err != nil {
			if got != nil {
				t.Fatal("invalid order returned a partial plan")
			}
			return
		}
		want := input.Order
		if want == nil {
			want = []Account{input.Owner}
		}
		if len(got) == 0 || !reflect.DeepEqual(got, want) {
			t.Fatal("order was changed or an account was implicitly added")
		}
		if input.Owner.ID <= 0 || (input.Owner.Kind != Personal && input.Owner.Kind != Team) {
			t.Fatal("invalid owner accepted")
		}
		if input.Owner.Kind == Team && (len(got) != 1 || got[0] != input.Owner) {
			t.Fatal("team key escaped its owning account")
		}
		seen := map[Account]bool{}
		for _, a := range got {
			if a.ID <= 0 || (a.Kind != Personal && a.Kind != Team) || seen[a] {
				t.Fatal("invalid or duplicate payer accepted")
			}
			seen[a] = true
			if (a.Kind == Personal && a != input.Owner) || (a.Kind == Team && !input.Grants[a.ID]) {
				t.Fatal("unapproved payer accepted")
			}
		}
	})
}
