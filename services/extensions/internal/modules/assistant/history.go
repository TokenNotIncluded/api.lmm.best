// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
// Legacy DTOs and privacy contract adapted from api.lmm.best main 6efcb6fb.
package assistant

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
)

const HistoryPrivacyNotice = "你与助手的对话不是私密通信，请勿发送个人信息、密码、API Key、Cookie 或其他凭证。敏感内容会被自动脱敏。"
const maxSafeHistoryID int64 = 9007199254740991

type Conversation struct {
	ID                 int64  `json:"id"`
	Title              string `json:"title"`
	LastMessagePreview string `json:"last_message_preview"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`
	ArchivedAt         int64  `json:"archived_at"`
	RestrictedAt       int64  `json:"restricted_at"`
	Owner              string `json:"owner"`
	PrivacyNotice      string `json:"privacy_notice"`
}
type HistoryMessage struct {
	ID        int64  `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt int64  `json:"created_at"`
}
type HistoryPage struct {
	Conversations []Conversation `json:"conversations"`
	NextCursor    string         `json:"next_cursor,omitempty"`
	PrivacyNotice string         `json:"privacy_notice"`
}
type HistoryDetail struct {
	Conversation  Conversation     `json:"conversation"`
	Messages      []HistoryMessage `json:"messages"`
	PrivacyNotice string           `json:"privacy_notice"`
}
type ArchiveResult struct {
	ID         int64 `json:"id"`
	Archived   bool  `json:"archived"`
	ArchivedAt int64 `json:"archived_at"`
}
type HistoryPosition struct{ UpdatedAt, ID int64 }
type CompletedTurn struct {
	ConversationID int64
	ClientTurnID   string
	UserContent    string
	AnswerContent  string
	InputHash      [32]byte
	AnswerHash     [32]byte
	Now            int64
}

// HistoryRepository owns only the separate assistant DB. RecordTurn is atomic:
// a successful pair and its replay receipt commit together, or nothing does.
// It is not a model executor, payment receipt or caller-supplied identity store.
type HistoryRepository interface {
	RecordTurn(context.Context, int64, CompletedTurn) (int64, error)
	ListHistory(context.Context, int64, bool, HistoryPosition, int) ([]Conversation, error)
	ReadHistory(context.Context, int64, int64, int) (Conversation, []HistoryMessage, error)
	ArchiveHistory(context.Context, int64, int64, bool, int64) (Conversation, error)
	ClickPreset(context.Context, string) error
}

type HistoryService struct {
	repo      HistoryRepository
	auth      access.Authority
	cursorKey []byte
	now       func() time.Time
}

func NewHistory(repo HistoryRepository, auth access.Authority, cursorKey []byte) (*HistoryService, error) {
	if access.Missing(repo) || access.Missing(auth) || len(cursorKey) < 32 || len(cursorKey) > 4096 {
		return nil, access.ErrInvalid
	}
	return &HistoryService{repo: repo, auth: auth, cursorKey: append([]byte(nil), cursorKey...), now: time.Now}, nil
}

func (s *HistoryService) actor(ctx context.Context, token string) (access.Principal, error) {
	p, err := s.auth.Check(ctx, token, access.Account{}, access.Read)
	if err != nil {
		return access.Principal{}, err
	}
	if p.UserID <= 0 || p.Level < 0 || p.Level > 6 || p.Account != (access.Account{Kind: "personal", ID: p.UserID}) {
		return access.Principal{}, access.ErrUnavailable
	}
	return p, nil
}

// RecordCompletedTurn is an internal trusted-adapter hook, not a Web mutation.
// Call only after the billed Core model result is authoritative. Passing these
// persistence tests does not prove that such a model adapter is installed.
func (s *HistoryService) RecordCompletedTurn(ctx context.Context, token string, conversationID int64, turnID, input, answer string) (int64, error) {
	p, err := s.actor(ctx, token)
	if err != nil {
		return 0, err
	}
	if conversationID < 0 || conversationID > maxSafeHistoryID || !access.ID(turnID) || len(turnID) > 64 ||
		!utf8.ValidString(input) || !utf8.ValidString(answer) || len(input) > 32<<10 || len(answer) > 32<<10 {
		return 0, access.ErrInvalid
	}
	input, answer = redactAssistantHistoryBounded(input), redactAssistantHistoryBounded(answer)
	if strings.TrimSpace(input) == "" || strings.TrimSpace(answer) == "" {
		return 0, access.ErrInvalid
	}
	return s.repo.RecordTurn(ctx, p.UserID, CompletedTurn{ConversationID: conversationID, ClientTurnID: turnID,
		UserContent: input, AnswerContent: answer, InputHash: sha256.Sum256([]byte(input)),
		AnswerHash: sha256.Sum256([]byte(answer)), Now: s.now().Unix()})
}

type historyCursor struct {
	Version   int   `json:"v"`
	Owner     int64 `json:"owner"`
	Archived  bool  `json:"archived"`
	UpdatedAt int64 `json:"updated_at"`
	ID        int64 `json:"id"`
	ExpiresAt int64 `json:"expires_at"`
}

func (s *HistoryService) encodeCursor(userID int64, archived bool, item Conversation) string {
	data, _ := json.Marshal(historyCursor{1, userID, archived, item.UpdatedAt, item.ID, s.now().Add(24 * time.Hour).Unix()})
	value := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, s.cursorKey)
	_, _ = mac.Write([]byte(value))
	return value + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *HistoryService) decodeCursor(value string, userID int64, archived bool) (HistoryPosition, error) {
	if value == "" {
		return HistoryPosition{}, nil
	}
	if len(value) > 1024 {
		return HistoryPosition{}, access.ErrInvalid
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return HistoryPosition{}, access.ErrInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	mac := hmac.New(sha256.New, s.cursorKey)
	_, _ = mac.Write([]byte(parts[0]))
	if err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		return HistoryPosition{}, access.ErrInvalid
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	var cursor historyCursor
	if err != nil || access.Decode(data, &cursor) != nil || cursor.Version != 1 || cursor.Owner != userID ||
		cursor.Archived != archived || cursor.ID <= 0 || cursor.ID > maxSafeHistoryID || cursor.UpdatedAt <= 0 || cursor.ExpiresAt <= s.now().Unix() {
		return HistoryPosition{}, access.ErrInvalid
	}
	return HistoryPosition{cursor.UpdatedAt, cursor.ID}, nil
}

func (s *HistoryService) List(ctx context.Context, token string, ownerID int64, archived bool, limit int, cursor string) (HistoryPage, error) {
	p, err := s.actor(ctx, token)
	if err != nil {
		return HistoryPage{}, err
	}
	if ownerID != 0 && ownerID != p.UserID {
		// No authoritative target-level lookup is available in the current RPC.
		// Never infer main's strict-lower administrator permission from a query.
		return HistoryPage{}, access.ErrNotFound
	}
	if limit < 1 || limit > 100 {
		return HistoryPage{}, access.ErrInvalid
	}
	position, err := s.decodeCursor(cursor, p.UserID, archived)
	if err != nil {
		return HistoryPage{}, err
	}
	items, err := s.repo.ListHistory(ctx, p.UserID, archived, position, limit+1)
	if err != nil {
		return HistoryPage{}, err
	}
	page := HistoryPage{Conversations: items, PrivacyNotice: HistoryPrivacyNotice}
	if len(items) > limit {
		page.Conversations = items[:limit]
		page.NextCursor = s.encodeCursor(p.UserID, archived, items[limit-1])
	}
	if page.Conversations == nil {
		page.Conversations = []Conversation{}
	}
	return page, nil
}

func (s *HistoryService) Detail(ctx context.Context, token string, id int64, limit int) (HistoryDetail, error) {
	p, err := s.actor(ctx, token)
	if err != nil {
		return HistoryDetail{}, err
	}
	if id <= 0 || id > maxSafeHistoryID {
		return HistoryDetail{}, access.ErrNotFound
	}
	if limit < 1 || limit > 100 {
		return HistoryDetail{}, access.ErrInvalid
	}
	item, messages, err := s.repo.ReadHistory(ctx, p.UserID, id, limit)
	if err != nil {
		return HistoryDetail{}, err
	}
	if messages == nil {
		messages = []HistoryMessage{}
	}
	return HistoryDetail{item, messages, HistoryPrivacyNotice}, nil
}

func (s *HistoryService) Archive(ctx context.Context, token string, id int64, archived bool) (ArchiveResult, error) {
	p, err := s.actor(ctx, token)
	if err != nil {
		return ArchiveResult{}, err
	}
	if id <= 0 || id > maxSafeHistoryID {
		return ArchiveResult{}, access.ErrNotFound
	}
	item, err := s.repo.ArchiveHistory(ctx, p.UserID, id, archived, s.now().Unix())
	if err != nil {
		return ArchiveResult{}, err
	}
	return ArchiveResult{item.ID, item.ArchivedAt != 0, item.ArchivedAt}, nil
}

func (s *HistoryService) PresetClick(ctx context.Context, id string) error {
	if _, ok := requiredPromptPresetCopy[id]; !ok {
		return access.ErrNotFound
	}
	return s.repo.ClickPreset(ctx, id)
}

func (s *HistoryService) Status(ctx context.Context, token string) (map[string]any, error) {
	_, err := s.actor(ctx, token)
	if err != nil {
		return nil, err
	}
	return map[string]any{"enabled": false, "route_available": false, "model": "", "group": "",
		"funding":                  map[string]any{"mode": "super_administrator", "configured": false},
		"developer_access_granted": false, "permission_profile_available": false,
		"agent": map[string]any{"enabled": false, "stream_enabled": false},
		"capabilities": map[string]bool{"public_assistant": false, "history": true, "presets": true, "admin_history": false,
			"new_user_gift":   false,
			"developer_tools": false, "admin_config": false, "admin_pricing": false, "admin_model_inventory": false, "admin_model_sync": false},
		"unavailable_reason": "billed_assistant_delegation_not_configured"}, nil
}
