// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package forum

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"sort"
	"sync"
)

// @mark forum.repository role=test
// This transactional reference is TEST ONLY, not persistence or a production
// substitute. Its global mutex models a serial transaction, not PG row locking.
type memoryRepository struct {
	mu                              sync.Mutex
	boards                          map[int64]Board
	topics                          map[int64]Topic
	replies                         map[int64]Reply
	intents                         map[actorKey]intent
	audits                          []Audit
	nextBoard, nextTopic, nextReply int64
	failWrite                       error
	failAudit                       bool
	unknownCommit                   bool
	writeCalls                      int
}
type actorKey struct {
	actor int64
	key   string
}
type intent struct {
	action Action
	digest [32]byte
	id     int64
}

func memory() *memoryRepository {
	return &memoryRepository{boards: map[int64]Board{}, topics: map[int64]Topic{}, replies: map[int64]Reply{}, intents: map[actorKey]intent{}}
}

var _ Repository = (*memoryRepository)(nil)

func referencePage[T any](items []T, p PageRequest, id func(T) int64) Page[T] {
	sort.Slice(items, func(i, j int) bool { return id(items[i]) < id(items[j]) })
	out := Page[T]{Items: []T{}}
	for _, item := range items {
		if id(item) <= p.AfterID {
			continue
		}
		if len(out.Items) == p.Limit {
			out.NextAfterID = id(out.Items[len(out.Items)-1])
			break
		}
		out.Items = append(out.Items, item)
	}
	return out
}
func (m *memoryRepository) Boards(ctx context.Context, p PageRequest) (Page[Board], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Page[Board]{}, err
	}
	items := []Board{}
	for _, v := range m.boards {
		items = append(items, v)
	}
	return referencePage(items, p, func(b Board) int64 { return b.ID }), nil
}
func (m *memoryRepository) GetBoard(ctx context.Context, id int64) (Board, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Board{}, err
	}
	b, ok := m.boards[id]
	if !ok {
		return Board{}, ErrNotFound
	}
	return b, nil
}
func (m *memoryRepository) Topics(ctx context.Context, id int64, p PageRequest) (Page[Topic], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Page[Topic]{}, err
	}
	if _, ok := m.boards[id]; !ok {
		return Page[Topic]{}, ErrNotFound
	}
	items := []Topic{}
	for _, v := range m.topics {
		if v.BoardID == id && !v.Hidden {
			items = append(items, v)
		}
	}
	return referencePage(items, p, func(t Topic) int64 { return t.ID }), nil
}
func (m *memoryRepository) GetTopic(ctx context.Context, id int64) (Topic, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Topic{}, err
	}
	t, ok := m.topics[id]
	if !ok {
		return Topic{}, ErrNotFound
	}
	return t, nil
}
func (m *memoryRepository) Replies(ctx context.Context, id int64, p PageRequest) (Page[Reply], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Page[Reply]{}, err
	}
	t, ok := m.topics[id]
	if !ok || t.Hidden {
		return Page[Reply]{}, ErrNotFound
	}
	items := []Reply{}
	for _, v := range m.replies {
		if v.TopicID == id && !v.Hidden {
			items = append(items, v)
		}
	}
	return referencePage(items, p, func(r Reply) int64 { return r.ID }), nil
}
func (m *memoryRepository) GetReply(ctx context.Context, id int64) (Reply, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Reply{}, err
	}
	r, ok := m.replies[id]
	if !ok {
		return Reply{}, ErrNotFound
	}
	return r, nil
}
func (m *memoryRepository) prior(action Action, c Creation, payload any) (intent, bool, error) {
	data, _ := json.Marshal(payload)
	want := intent{action: action, digest: sha256.Sum256(data)}
	if old, ok := m.intents[actorKey{c.ActorID, c.Key}]; ok {
		if old.action != want.action || old.digest != want.digest {
			return want, false, ErrConflict
		}
		return old, true, nil
	}
	return want, false, nil
}
func (m *memoryRepository) startWrite(ctx context.Context) error {
	m.writeCalls++
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.failWrite
}
func (m *memoryRepository) outcome() error {
	if m.unknownCommit {
		return ErrUnknownCommit
	}
	return nil
}
func (m *memoryRepository) CreateBoard(ctx context.Context, c BoardCreation) (Board, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.startWrite(ctx); err != nil {
		return Board{}, err
	}
	in, found, err := m.prior(CreateBoard, c.Creation, c)
	if err != nil {
		return Board{}, err
	}
	if found {
		return m.boards[in.id], nil
	}
	m.nextBoard++
	b := Board{ID: m.nextBoard, Name: c.Name, AuthorID: c.ActorID}
	in.id = b.ID
	m.boards[b.ID] = b
	m.intents[actorKey{c.ActorID, c.Key}] = in
	if err = m.outcome(); err != nil {
		return Board{}, err
	}
	return b, nil
}
func (m *memoryRepository) CreateTopic(ctx context.Context, c TopicCreation) (Topic, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.startWrite(ctx); err != nil {
		return Topic{}, err
	}
	in, found, err := m.prior(CreateTopic, c.Creation, c)
	if err != nil {
		return Topic{}, err
	}
	if _, ok := m.boards[c.BoardID]; !ok {
		return Topic{}, ErrNotFound
	}
	if found {
		t := m.topics[in.id]
		if t.Hidden {
			return Topic{}, ErrNotFound
		}
		return t, nil
	}
	m.nextTopic++
	t := Topic{ID: m.nextTopic, BoardID: c.BoardID, AuthorID: c.ActorID, Title: c.Title, Body: c.Body, Version: 1}
	in.id = t.ID
	m.topics[t.ID] = t
	m.intents[actorKey{c.ActorID, c.Key}] = in
	if err = m.outcome(); err != nil {
		return Topic{}, err
	}
	return t, nil
}
func (m *memoryRepository) CreateReply(ctx context.Context, c ReplyCreation) (Reply, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.startWrite(ctx); err != nil {
		return Reply{}, err
	}
	in, found, err := m.prior(CreateReply, c.Creation, c)
	if err != nil {
		return Reply{}, err
	}
	t, ok := m.topics[c.TopicID]
	if !ok || t.Hidden {
		return Reply{}, ErrNotFound
	}
	if found {
		r := m.replies[in.id]
		if r.Hidden {
			return Reply{}, ErrNotFound
		}
		return r, nil
	}
	if t.Locked {
		return Reply{}, ErrLocked
	}
	m.nextReply++
	r := Reply{ID: m.nextReply, TopicID: c.TopicID, AuthorID: c.ActorID, Body: c.Body, Version: 1}
	in.id = r.ID
	m.replies[r.ID] = r
	m.intents[actorKey{c.ActorID, c.Key}] = in
	if err = m.outcome(); err != nil {
		return Reply{}, err
	}
	return r, nil
}
func (m *memoryRepository) Moderate(ctx context.Context, c Moderation) (Receipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.startWrite(ctx); err != nil {
		return Receipt{}, err
	}
	if m.failAudit {
		return Receipt{}, ErrUnavailable
	}
	var before int64
	if c.Action == HideReply {
		r, ok := m.replies[c.Resource.ID]
		if !ok {
			return Receipt{}, ErrNotFound
		}
		t := m.topics[r.TopicID]
		if c.Resource != (Resource{Kind: ReplyResource, ID: r.ID, BoardID: t.BoardID, TopicID: t.ID}) {
			return Receipt{}, ErrConflict
		}
		before = r.Version
		if before != c.ExpectedVersion {
			return Receipt{}, ErrConflict
		}
		r.Hidden = true
		r.Version++
		m.replies[r.ID] = r
	} else {
		t, ok := m.topics[c.Resource.ID]
		if !ok {
			return Receipt{}, ErrNotFound
		}
		if c.Resource != topicResource(t) {
			return Receipt{}, ErrConflict
		}
		before = t.Version
		if before != c.ExpectedVersion {
			return Receipt{}, ErrConflict
		}
		switch c.Action {
		case LockTopic:
			t.Locked = c.Value
		case PinTopic:
			t.Pinned = c.Value
		case HideTopic:
			t.Hidden = true
		default:
			return Receipt{}, ErrForbidden
		}
		t.Version++
		m.topics[t.ID] = t
	}
	m.audits = append(m.audits, Audit{ActorID: c.ActorID, Action: c.Action, Resource: c.Resource, BeforeVersion: before, AfterVersion: before + 1, Value: c.Value, Reason: c.Reason})
	if err := m.outcome(); err != nil {
		return Receipt{}, err
	}
	return Receipt{ID: c.Resource.ID, Version: before + 1}, nil
}
