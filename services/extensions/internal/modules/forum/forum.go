// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package forum

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// @mark forum.authority role=implementation
// Service never accepts an actor/role from HTTP or caches an authorization.
type Service struct {
	repo      Repository
	authority Authority
}

func New(repo Repository, authority Authority) (*Service, error) {
	if missing(repo) || missing(authority) {
		return nil, ErrUnavailable
	}
	return &Service{repo: repo, authority: authority}, nil
}
func missing(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Func, reflect.Interface, reflect.Chan, reflect.Slice:
		return r.IsNil()
	}
	return false
}
func identifier(s string, min, max int) bool {
	if len(s) < min || len(s) > max {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// Text stays plain text, byte-for-byte (no trimming, HTML rendering or markup
// interpretation). Reject blank strings and controls other than body LF/TAB.
func text(s string, max int, multiline bool) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > max || strings.TrimSpace(s) == "" {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) && !(multiline && (c == '\n' || c == '\t')) {
			return false
		}
	}
	return true
}
func authorityError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrUnauthorized) {
		return ErrUnauthorized
	}
	if errors.Is(err, ErrForbidden) {
		return ErrForbidden
	}
	return ErrUnavailable
}
func (s *Service) begin(ctx context.Context, credential string) (context.Context, context.CancelFunc, Actor, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	if !identifier(credential, 32, 256) {
		return ctx, cancel, Actor{}, ErrUnauthorized
	}
	actor, err := s.authority.Authenticate(ctx, credential)
	if err != nil {
		return ctx, cancel, Actor{}, authorityError(err)
	}
	if actor.UserID <= 0 {
		return ctx, cancel, Actor{}, ErrUnavailable
	}
	return ctx, cancel, actor, nil
}
func (s *Service) gate(ctx context.Context, token string, actor Actor, action Action, resource Resource) error {
	var valid bool
	switch action {
	case ListBoards, CreateBoard:
		valid = resource == (Resource{Kind: ForumResource})
	case ListTopics, CreateTopic:
		valid = resource.Kind == BoardResource && resource.ID > 0 && resource.BoardID == resource.ID && resource.TopicID == 0
	case ReadTopic, ListReplies, CreateReply, LockTopic, PinTopic, HideTopic:
		valid = resource.Kind == TopicResource && resource.ID > 0 && resource.TopicID == resource.ID && resource.BoardID > 0
	case HideReply:
		valid = resource.Kind == ReplyResource && resource.ID > 0 && resource.TopicID > 0 && resource.BoardID > 0
	}
	if !valid {
		return ErrForbidden
	}
	return authorityError(s.authority.Authorize(ctx, token, actor, action, resource))
}
func boardResource(id int64) Resource { return Resource{Kind: BoardResource, ID: id, BoardID: id} }
func topicResource(t Topic) Resource {
	return Resource{Kind: TopicResource, ID: t.ID, BoardID: t.BoardID, TopicID: t.ID}
}

func (s *Service) Boards(ctx context.Context, token string, page PageRequest) (Page[Board], error) {
	ctx, cancel, actor, err := s.begin(ctx, token)
	defer cancel()
	if err != nil {
		return Page[Board]{}, err
	}
	if !page.Valid() {
		return Page[Board]{}, ErrInvalid
	}
	if err = s.gate(ctx, token, actor, ListBoards, Resource{Kind: ForumResource}); err != nil {
		return Page[Board]{}, err
	}
	return s.repo.Boards(ctx, page)
}
func (s *Service) AddBoard(ctx context.Context, token, key, name string) (Board, error) {
	ctx, cancel, actor, err := s.begin(ctx, token)
	defer cancel()
	if err != nil {
		return Board{}, err
	}
	if !identifier(key, 1, 128) || !text(name, MaxNameRunes, false) {
		return Board{}, ErrInvalid
	}
	if err = s.gate(ctx, token, actor, CreateBoard, Resource{Kind: ForumResource}); err != nil {
		return Board{}, err
	}
	return s.repo.CreateBoard(ctx, BoardCreation{Creation: Creation{ActorID: actor.UserID, Key: key}, Name: name})
}
func (s *Service) Topics(ctx context.Context, token string, boardID int64, page PageRequest) (Page[Topic], error) {
	ctx, cancel, actor, err := s.begin(ctx, token)
	defer cancel()
	if err != nil {
		return Page[Topic]{}, err
	}
	if boardID <= 0 || !page.Valid() {
		return Page[Topic]{}, ErrInvalid
	}
	if err = s.gate(ctx, token, actor, ListTopics, boardResource(boardID)); err != nil {
		return Page[Topic]{}, err
	}
	return s.repo.Topics(ctx, boardID, page)
}
func (s *Service) AddTopic(ctx context.Context, token, key string, boardID int64, title, body string) (Topic, error) {
	ctx, cancel, actor, err := s.begin(ctx, token)
	defer cancel()
	if err != nil {
		return Topic{}, err
	}
	if boardID <= 0 || !identifier(key, 1, 128) || !text(title, MaxTitleRunes, false) || !text(body, MaxBodyRunes, true) {
		return Topic{}, ErrInvalid
	}
	if err = s.gate(ctx, token, actor, CreateTopic, boardResource(boardID)); err != nil {
		return Topic{}, err
	}
	return s.repo.CreateTopic(ctx, TopicCreation{Creation: Creation{ActorID: actor.UserID, Key: key}, BoardID: boardID, Title: title, Body: body})
}

// @mark forum.review.visibility role=implementation
func (s *Service) Topic(ctx context.Context, token string, id int64) (Topic, error) {
	ctx, cancel, actor, err := s.begin(ctx, token)
	defer cancel()
	if err != nil {
		return Topic{}, err
	}
	if id <= 0 {
		return Topic{}, ErrInvalid
	}
	topic, err := s.repo.GetTopic(ctx, id)
	if err != nil {
		return Topic{}, err
	}
	if topic.Hidden {
		return Topic{}, ErrNotFound
	}
	// The preflight snapshot identifies the grant; its body is never returned.
	resource := topicResource(topic)
	if err = s.gate(ctx, token, actor, ReadTopic, resource); err != nil {
		return Topic{}, err
	}
	// Visibility linearizes at this post-authorization snapshot, not preflight.
	topic, err = s.repo.GetTopic(ctx, id)
	if err != nil {
		return Topic{}, err
	}
	if topic.Hidden {
		return Topic{}, ErrNotFound
	}
	if topicResource(topic) != resource {
		return Topic{}, ErrForbidden
	}
	return topic, nil
}
func (s *Service) Replies(ctx context.Context, token string, topicID int64, page PageRequest) (Page[Reply], error) {
	ctx, cancel, actor, err := s.begin(ctx, token)
	defer cancel()
	if err != nil {
		return Page[Reply]{}, err
	}
	if topicID <= 0 || !page.Valid() {
		return Page[Reply]{}, ErrInvalid
	}
	topic, err := s.repo.GetTopic(ctx, topicID)
	if err != nil {
		return Page[Reply]{}, err
	}
	if topic.Hidden {
		return Page[Reply]{}, ErrNotFound
	}
	if err = s.gate(ctx, token, actor, ListReplies, topicResource(topic)); err != nil {
		return Page[Reply]{}, err
	}
	return s.repo.Replies(ctx, topicID, page)
}
func (s *Service) AddReply(ctx context.Context, token, key string, topicID int64, body string) (Reply, error) {
	ctx, cancel, actor, err := s.begin(ctx, token)
	defer cancel()
	if err != nil {
		return Reply{}, err
	}
	if topicID <= 0 || !identifier(key, 1, 128) || !text(body, MaxBodyRunes, true) {
		return Reply{}, ErrInvalid
	}
	topic, err := s.repo.GetTopic(ctx, topicID)
	if err != nil {
		return Reply{}, err
	}
	if topic.Hidden {
		return Reply{}, ErrNotFound
	}
	if err = s.gate(ctx, token, actor, CreateReply, topicResource(topic)); err != nil {
		return Reply{}, err
	}
	// Do not check Locked here: the transactional repository arbitrates lock vs
	// new reply and can reconcile an already-committed create after lock.
	return s.repo.CreateReply(ctx, ReplyCreation{Creation: Creation{ActorID: actor.UserID, Key: key}, TopicID: topicID, Body: body})
}

// @mark forum.moderation role=implementation
// Resource ancestry and ActorID are always derived, not accepted as claims.
func (s *Service) Moderate(ctx context.Context, token string, action Action, id, expectedVersion int64, value bool, reason string) (Receipt, error) {
	ctx, cancel, actor, err := s.begin(ctx, token)
	defer cancel()
	if err != nil {
		return Receipt{}, err
	}
	switch action {
	case LockTopic, PinTopic, HideTopic, HideReply:
	default:
		return Receipt{}, ErrForbidden
	}
	if id <= 0 || expectedVersion <= 0 || expectedVersion == math.MaxInt64 || !text(reason, MaxReasonRunes, false) || ((action == HideTopic || action == HideReply) && !value) {
		return Receipt{}, ErrInvalid
	}
	var resource Resource
	if action == HideReply {
		reply, e := s.repo.GetReply(ctx, id)
		if e != nil {
			return Receipt{}, e
		}
		topic, e := s.repo.GetTopic(ctx, reply.TopicID)
		if e != nil {
			return Receipt{}, e
		}
		resource = Resource{Kind: ReplyResource, ID: reply.ID, BoardID: topic.BoardID, TopicID: topic.ID}
	} else {
		topic, e := s.repo.GetTopic(ctx, id)
		if e != nil {
			return Receipt{}, e
		}
		resource = topicResource(topic)
	}
	if err = s.gate(ctx, token, actor, action, resource); err != nil {
		return Receipt{}, err
	}
	return s.repo.Moderate(ctx, Moderation{ActorID: actor.UserID, Action: action, Resource: resource, ExpectedVersion: expectedVersion, Value: value, Reason: reason})
}
