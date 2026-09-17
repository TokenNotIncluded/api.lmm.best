// Package deployslots plans paired Go/Web A/B transitions. It does not run a
// writer, switch traffic, migrate a database, or manufacture native receipts.
package deployslots

import (
	"errors"
	"fmt"
	"regexp"
)

var ErrConflict = errors.New("slot state changed or another transition is pending")

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$`)
var versionPattern = regexp.MustCompile(`^[0-9][0-9A-Za-z._+~-]{0,79}$`)

// Artifact identifies retained, signed package bytes, not a mutable URL or tag.
// Signature verification belongs to the native deployment controller.
type Artifact struct {
	Version  string `json:"version"`
	Revision string `json:"revision"`
	SHA256   string `json:"sha256"`
}

// Pair must always travel together, including for a single-component update.
type Pair struct {
	Go  Artifact `json:"go"`
	Web Artifact `json:"web"`
}

func (p Pair) validate() error {
	for _, a := range []Artifact{p.Go, p.Web} {
		if !versionPattern.MatchString(a.Version) || !revisionPattern.MatchString(a.Revision) || !digestPattern.MatchString(a.SHA256) {
			return errors.New("invalid immutable package identity")
		}
	}
	return nil
}

// Receipt is an adapter input, not an authorization token. Only the native
// controller, after its signature, DB compatibility, writer-drain and health
// checks, may supply it. Never accept receipt fields from an HTTP request.
type Receipt struct {
	DeploymentID string
	PlanSHA256   string
	Phase        string
	Pair         Pair
}

type Pending struct {
	DeploymentID string `json:"deployment_id"`
	PlanSHA256   string `json:"plan_sha256"`
	Target       string `json:"target"`
	Pair         Pair   `json:"pair"`
	Rollback     bool   `json:"rollback"`
	Started      bool   `json:"started"`
}

// Active denotes the last confirmed pair, NOT an assurance that it is serving
// while Pending.Started is true. During activation native status is authoritative.
// Slots contains only confirmed pairs; staging never destroys rollback evidence.
type State struct {
	Format     int             `json:"format"`
	Generation uint64          `json:"generation"`
	Active     string          `json:"active"`
	Slots      map[string]Pair `json:"slots"`
	Pending    *Pending        `json:"pending,omitempty"`
}

func New(baseline Receipt) (State, error) {
	if baseline.Phase != "CONFIRMED" || !validReceiptIdentity(baseline.DeploymentID, baseline.PlanSHA256) {
		return State{}, errors.New("initial slots require a confirmed native baseline")
	}
	if err := baseline.Pair.validate(); err != nil {
		return State{}, err
	}
	return State{Format: 1, Generation: 1, Active: "A", Slots: map[string]Pair{"A": baseline.Pair}}, nil
}

func validReceiptIdentity(id, digest string) bool {
	return idPattern.MatchString(id) && digestPattern.MatchString(digest)
}

func other(slot string) string {
	if slot == "A" {
		return "B"
	}
	return "A"
}

func (s State) Validate() error {
	if s.Format != 1 || s.Generation == 0 || (s.Active != "A" && s.Active != "B") || len(s.Slots) < 1 || len(s.Slots) > 2 {
		return errors.New("invalid slot state")
	}
	if _, found := s.Slots[s.Active]; !found {
		return errors.New("active slot has no confirmed pair")
	}
	for name, pair := range s.Slots {
		if name != "A" && name != "B" {
			return errors.New("unknown slot")
		}
		if err := pair.validate(); err != nil {
			return err
		}
	}
	if p := s.Pending; p != nil {
		if p.Target != other(s.Active) || !validReceiptIdentity(p.DeploymentID, p.PlanSHA256) {
			return errors.New("invalid pending slot identity")
		}
		if err := p.Pair.validate(); err != nil {
			return err
		}
		if p.Pair == s.Slots[s.Active] {
			return errors.New("pending pair is already active")
		}
		if p.Rollback {
			if pair, ok := s.Slots[p.Target]; !ok || pair != p.Pair {
				return errors.New("rollback target is not the retained confirmed pair")
			}
		}
	}
	return nil
}

func (s State) next(expected uint64) (State, error) {
	if err := s.Validate(); err != nil {
		return State{}, err
	}
	if expected != s.Generation || s.Generation == ^uint64(0) {
		return State{}, ErrConflict
	}
	next := s
	next.Generation++
	next.Slots = make(map[string]Pair, len(s.Slots))
	for key, pair := range s.Slots {
		next.Slots[key] = pair
	}
	if s.Pending != nil {
		pending := *s.Pending
		next.Pending = &pending
	}
	return next, nil
}

// Stage returns a copy. It reserves the inactive slot without changing either
// confirmed pair. Physical staging must also preserve the native rollback archive.
func (s State) Stage(expected uint64, id, planDigest string, pair Pair) (State, error) {
	if s.Pending != nil {
		return State{}, ErrConflict
	}
	next, err := s.next(expected)
	if err != nil {
		return State{}, err
	}
	next.Pending = &Pending{DeploymentID: id, PlanSHA256: planDigest, Target: other(s.Active), Pair: pair}
	return next, next.Validate()
}

// StageRollback selects a previously confirmed PAIR. It is not permission to
// downgrade a writer: the native controller must independently authorize that
// pair against the current database, and must never roll back business data.
func (s State) StageRollback(expected uint64, id, planDigest string) (State, error) {
	pair, found := s.Slots[other(s.Active)]
	if !found {
		return State{}, errors.New("no previously confirmed rollback pair")
	}
	next, err := s.Stage(expected, id, planDigest, pair)
	if err != nil {
		return State{}, err
	}
	next.Pending.Rollback = true
	return next, next.Validate()
}

// Begin records uncertainty BEFORE asking native code to switch. A crash or
// timeout after this point must retain Pending and be reconciled, not retried.
func (s State) Begin(expected uint64, id, planDigest string) (State, error) {
	next, err := s.next(expected)
	if err != nil {
		return State{}, err
	}
	if next.Pending == nil || next.Pending.Started || next.Pending.DeploymentID != id || next.Pending.PlanSHA256 != planDigest {
		return State{}, ErrConflict
	}
	next.Pending.Started = true
	return next, nil
}

// AbortStage is allowed only before any native activation is started.
func (s State) AbortStage(expected uint64, id, planDigest string) (State, error) {
	next, err := s.next(expected)
	if err != nil {
		return State{}, err
	}
	if next.Pending == nil || next.Pending.Started || next.Pending.DeploymentID != id || next.Pending.PlanSHA256 != planDigest {
		return State{}, ErrConflict
	}
	next.Pending = nil
	return next, nil
}

// Finish accepts only terminal native evidence for this exact plan. In-flight,
// failed, timed-out or merely HTTP-200 observations cannot advance the slots.
func (s State) Finish(expected uint64, receipt Receipt) (State, error) {
	next, err := s.next(expected)
	if err != nil {
		return State{}, err
	}
	p := next.Pending
	if p == nil || !p.Started || p.DeploymentID != receipt.DeploymentID || p.PlanSHA256 != receipt.PlanSHA256 {
		return State{}, ErrConflict
	}
	switch receipt.Phase {
	case "CONFIRMED":
		if receipt.Pair != p.Pair {
			return State{}, errors.New("native confirmation does not match both staged artifacts")
		}
		next.Slots[p.Target] = p.Pair
		next.Active = p.Target
	case "ROLLED_BACK":
		if receipt.Pair != s.Slots[s.Active] {
			return State{}, errors.New("native recovery did not restore the complete confirmed pair")
		}
	default:
		return State{}, fmt.Errorf("native deployment is not terminal: %s", receipt.Phase)
	}
	next.Pending = nil
	return next, next.Validate()
}
