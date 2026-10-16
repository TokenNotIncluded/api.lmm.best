// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
// Package forum implements a compiled extension's domain and HTTP boundary.
// Production authority and durable storage adapters are not implemented here.
package forum

import (
	"context"
	"errors"
)

var (
	ErrInvalid      = errors.New("invalid_request")
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not_found")
	ErrConflict     = errors.New("state_conflict")
	ErrLocked       = errors.New("topic_locked")
	ErrUnavailable  = errors.New("dependency_unavailable")
	// ErrUnknownCommit means storage cannot establish whether a write committed.
	// Callers must reconcile using the original key/version; never blindly retry.
	ErrUnknownCommit = errors.New("commit_outcome_unknown")
)

const (
	MaxNameRunes    = 80
	MaxTitleRunes   = 200
	MaxBodyRunes    = 10000
	MaxReasonRunes  = 500
	MaxPageSize     = 100
	DefaultPageSize = 20
	MaxBodyBytes    = 64 << 10
)

type Action string

const (
	ListBoards  Action = "forum.boards.list"
	CreateBoard Action = "forum.boards.create"
	ListTopics  Action = "forum.topics.list"
	CreateTopic Action = "forum.topics.create"
	ReadTopic   Action = "forum.topics.read"
	ListReplies Action = "forum.replies.list"
	CreateReply Action = "forum.replies.create"
	LockTopic   Action = "forum.topics.lock"
	PinTopic    Action = "forum.topics.pin"
	HideTopic   Action = "forum.topics.hide"
	HideReply   Action = "forum.replies.hide"
)

type ResourceKind string

const (
	ForumResource ResourceKind = "forum"
	BoardResource ResourceKind = "board"
	TopicResource ResourceKind = "topic"
	ReplyResource ResourceKind = "reply"
)

// @mark forum.authority role=contract
// IDs and ancestry come from storage, never from client-supplied authority claims.
type Resource struct {
	Kind    ResourceKind
	ID      int64
	BoardID int64
	TopicID int64
}

type Actor struct{ UserID int64 }

// Authority is the only identity/permission source. Authenticate must validate a
// LIVE Core personal session on every operation (no host tokens or API keys).
// Authorize must check that same session/actor against the exact action/resource,
// honoring explicit deny and revocation. nil error is an authoritative grant,
// NOT a local role/PlatformLevel check. Unknown/unmapped gates must deny.
// There is deliberately no production implementation until A01 freezes its gate.
type Authority interface {
	Authenticate(context.Context, string) (Actor, error)
	Authorize(context.Context, string, Actor, Action, Resource) error
}

type Board struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	AuthorID int64  `json:"author_id"`
}
type Topic struct {
	ID       int64  `json:"id"`
	BoardID  int64  `json:"board_id"`
	AuthorID int64  `json:"author_id"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Locked   bool   `json:"locked"`
	Pinned   bool   `json:"pinned"`
	Hidden   bool   `json:"-"`
	Version  int64  `json:"version"`
}
type Reply struct {
	ID       int64  `json:"id"`
	TopicID  int64  `json:"topic_id"`
	AuthorID int64  `json:"author_id"`
	Body     string `json:"body"`
	Hidden   bool   `json:"-"`
	Version  int64  `json:"version"`
}

// Ascending immutable IDs, exclusive cursor. Pinning is metadata, not sorting.
// Repositories return at most Limit items and NextAfterID=last returned ID only
// when another visible item exists. Hidden rows never affect public page counts.
type PageRequest struct {
	AfterID int64
	Limit   int
}

func (p PageRequest) Valid() bool { return p.AfterID >= 0 && p.Limit >= 1 && p.Limit <= MaxPageSize }

type Page[T any] struct {
	Items       []T   `json:"items"`
	NextAfterID int64 `json:"next_after_id"`
}

// Key is unique per actor across ALL create operations. Reuse must compare the
// action, parent and exact validated content; changed intent returns ErrConflict.
type Creation struct {
	ActorID int64
	Key     string
}
type BoardCreation struct {
	Creation
	Name string
}
type TopicCreation struct {
	Creation
	BoardID     int64
	Title, Body string
}
type ReplyCreation struct {
	Creation
	TopicID int64
	Body    string
}

type Moderation struct {
	ActorID         int64
	Action          Action
	Resource        Resource
	ExpectedVersion int64
	Value           bool // hide only accepts true; lock/pin can be cleared
	Reason          string
}
type Receipt struct {
	ID      int64 `json:"id"`
	Version int64 `json:"version"`
}
type Audit struct {
	ActorID                     int64
	Action                      Action
	Resource                    Resource
	BeforeVersion, AfterVersion int64
	Value                       bool
	Reason                      string
}

// @mark forum.repository role=contract
// Repository owns forum data ONLY, in an isolated forum database, never Core's
// DSN. Writes must be single transactions, not read/check/write in the service:
//   - create + actor/key intent binding + result are atomic and durable;
//   - reply creation serializes with topic lock/hide and rechecks the parent;
//   - moderation CAS + immutable audit commit together (including no-op changes);
//   - hidden topics suppress their detail, replies, pagination and create replays;
//   - reply replays recheck current visibility; they never return cached bodies;
//   - cancellations/errors before commit leave no partial state; uncertain commit
//     returns ErrUnknownCommit, never a successful DTO or an automatic retry.
//
// GetTopic/GetReply are INTERNAL lookups, including hidden state for moderation.
// IDs and parent ancestry are immutable. GetTopic must read a fresh snapshot on
// each call: Service.Topic checks visibility and the granted resource again AFTER
// ReadTopic authorization. The pre-authorization body must never be a response.
// All public lists and create results must enforce visibility in their snapshot.
// Future PG adapter must follow operations' explicit Initialize/Open-check split;
// ordinary startup must never install or upgrade schema. This slice has NO PG
// adapter; the reference in-memory implementation exists only in *_test.go.
type Repository interface {
	Boards(context.Context, PageRequest) (Page[Board], error)
	GetBoard(context.Context, int64) (Board, error)
	Topics(context.Context, int64, PageRequest) (Page[Topic], error)
	GetTopic(context.Context, int64) (Topic, error)
	Replies(context.Context, int64, PageRequest) (Page[Reply], error)
	GetReply(context.Context, int64) (Reply, error)
	CreateBoard(context.Context, BoardCreation) (Board, error)
	CreateTopic(context.Context, TopicCreation) (Topic, error)
	CreateReply(context.Context, ReplyCreation) (Reply, error)
	Moderate(context.Context, Moderation) (Receipt, error)
}
