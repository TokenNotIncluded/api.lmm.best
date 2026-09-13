package controller

import (
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/gin-gonic/gin"
)

const (
	piRemoteSessionTTL         = 2 * time.Minute
	piRemoteMaxMetadataBytes   = 16 << 10
	piRemoteMaxMessageBytes    = 64 << 10
	piRemoteMaxMessages        = 128
	piRemoteMaxSessionsPerUser = 32
	piRemoteMaxSessionsGlobal  = 512
)

var piRemoteIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{7,63}$`)

type piRemoteCiphertext struct {
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type piRemoteSession struct {
	SessionID string             `json:"session_id"`
	DeviceID  string             `json:"device_id"`
	Metadata  piRemoteCiphertext `json:"metadata"`
	ExpiresAt int64              `json:"expires_at"`
	UpdatedAt int64              `json:"updated_at"`
	Messages  []piRemoteMessage  `json:"-"`
	nextSeq   uint64
}

type piRemoteMessage struct {
	Sequence   uint64 `json:"sequence"`
	Sender     string `json:"sender"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
	CreatedAt  int64  `json:"created_at"`
}

type piRemoteUpsertRequest struct {
	DeviceID string             `json:"device_id"`
	Metadata piRemoteCiphertext `json:"metadata"`
}

type piRemoteAppendRequest struct {
	Sender     string `json:"sender"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type piRemoteStore struct {
	mu       sync.Mutex
	sessions map[int]map[string]*piRemoteSession
	now      func() time.Time
}

func newPiRemoteStore() *piRemoteStore {
	return &piRemoteStore{sessions: make(map[int]map[string]*piRemoteSession), now: time.Now}
}

var activePiRemoteStore = newPiRemoteStore()

func validatePiRemoteCiphertext(value piRemoteCiphertext, maxBytes int) error {
	if len(value.Nonce) < 16 || len(value.Nonce) > 256 || len(value.Ciphertext) == 0 || len(value.Ciphertext) > maxBytes*2 {
		return errors.New("invalid encrypted payload size")
	}
	nonce, err := base64.RawURLEncoding.DecodeString(value.Nonce)
	if err != nil || len(nonce) != 12 {
		return errors.New("nonce must be a 12-byte unpadded base64url value")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value.Ciphertext)
	if err != nil || len(decoded) < 16 || len(decoded) > maxBytes {
		return errors.New("ciphertext must be bounded unpadded base64url")
	}
	return nil
}

func (store *piRemoteStore) cleanupLocked(userID int, now time.Time) map[string]*piRemoteSession {
	owned := store.sessions[userID]
	for id, session := range owned {
		if !time.Unix(session.ExpiresAt, 0).After(now) {
			delete(owned, id)
		}
	}
	return owned
}

func (store *piRemoteStore) cleanupAllLocked(now time.Time) {
	for userID := range store.sessions {
		store.cleanupLocked(userID, now)
		if len(store.sessions[userID]) == 0 {
			delete(store.sessions, userID)
		}
	}
}

func (store *piRemoteStore) sessionCountLocked() int {
	count := 0
	for _, owned := range store.sessions {
		count += len(owned)
	}
	return count
}

func (store *piRemoteStore) upsert(userID int, sessionID string, request piRemoteUpsertRequest) (*piRemoteSession, error) {
	if userID <= 0 || !piRemoteIDPattern.MatchString(sessionID) || !piRemoteIDPattern.MatchString(request.DeviceID) {
		return nil, errors.New("invalid session or device id")
	}
	if err := validatePiRemoteCiphertext(request.Metadata, piRemoteMaxMetadataBytes); err != nil {
		return nil, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	now := store.now().UTC()
	store.cleanupAllLocked(now)
	owned := store.cleanupLocked(userID, now)
	if owned == nil {
		owned = make(map[string]*piRemoteSession)
		store.sessions[userID] = owned
	}
	session := owned[sessionID]
	if session == nil {
		if len(owned) >= piRemoteMaxSessionsPerUser {
			return nil, errors.New("too many active Pi sessions")
		}
		if total := store.sessionCountLocked(); total >= piRemoteMaxSessionsGlobal {
			return nil, errors.New("too many active Pi sessions")
		}
		session = &piRemoteSession{SessionID: sessionID, nextSeq: 1}
		owned[sessionID] = session
	}
	session.DeviceID = request.DeviceID
	session.Metadata = request.Metadata
	session.UpdatedAt = now.Unix()
	session.ExpiresAt = now.Add(piRemoteSessionTTL).Unix()
	copy := *session
	copy.Messages = nil
	return &copy, nil
}

func (store *piRemoteStore) list(userID int) []piRemoteSession {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := store.now()
	store.cleanupAllLocked(now)
	owned := store.cleanupLocked(userID, now)
	result := make([]piRemoteSession, 0, len(owned))
	for _, session := range owned {
		copy := *session
		copy.Messages = nil
		result = append(result, copy)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UpdatedAt > result[j].UpdatedAt })
	return result
}

func (store *piRemoteStore) append(userID int, sessionID string, request piRemoteAppendRequest) (*piRemoteMessage, error) {
	if !piRemoteIDPattern.MatchString(sessionID) || (request.Sender != "plugin" && request.Sender != "controller") {
		return nil, errors.New("invalid message metadata")
	}
	if err := validatePiRemoteCiphertext(piRemoteCiphertext{Nonce: request.Nonce, Ciphertext: request.Ciphertext}, piRemoteMaxMessageBytes); err != nil {
		return nil, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	now := store.now().UTC()
	store.cleanupAllLocked(now)
	owned := store.cleanupLocked(userID, now)
	session := owned[sessionID]
	if session == nil {
		return nil, errors.New("session not found or expired")
	}
	message := piRemoteMessage{Sequence: session.nextSeq, Sender: request.Sender, Nonce: request.Nonce, Ciphertext: request.Ciphertext, CreatedAt: now.Unix()}
	session.nextSeq++
	session.Messages = append(session.Messages, message)
	if len(session.Messages) > piRemoteMaxMessages {
		session.Messages = append([]piRemoteMessage(nil), session.Messages[len(session.Messages)-piRemoteMaxMessages:]...)
	}
	return &message, nil
}

func (store *piRemoteStore) messages(userID int, sessionID string, after uint64) ([]piRemoteMessage, error) {
	if !piRemoteIDPattern.MatchString(sessionID) {
		return nil, errors.New("invalid session id")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	now := store.now()
	store.cleanupAllLocked(now)
	owned := store.cleanupLocked(userID, now)
	session := owned[sessionID]
	if session == nil {
		return nil, errors.New("session not found or expired")
	}
	result := make([]piRemoteMessage, 0, len(session.Messages))
	for _, message := range session.Messages {
		if message.Sequence > after {
			result = append(result, message)
		}
	}
	return result, nil
}

func PiRemoteUpsertSession(c *gin.Context) {
	var request piRemoteUpsertRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid request"})
		return
	}
	session, err := activePiRemoteStore.upsert(c.GetInt("id"), c.Param("session_id"), request)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	common.ApiSuccess(c, session)
}

func PiRemoteListSessions(c *gin.Context) {
	common.ApiSuccess(c, activePiRemoteStore.list(c.GetInt("id")))
}

func PiRemoteAppendMessage(c *gin.Context) {
	var request piRemoteAppendRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid request"})
		return
	}
	message, err := activePiRemoteStore.append(c.GetInt("id"), c.Param("session_id"), request)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	common.ApiSuccess(c, message)
}

func PiRemoteGetMessages(c *gin.Context) {
	after := uint64(0)
	if raw := strings.TrimSpace(c.Query("after")); raw != "" {
		for _, char := range raw {
			if char < '0' || char > '9' {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "after must be an integer"})
				return
			}
			after = after*10 + uint64(char-'0')
		}
	}
	messages, err := activePiRemoteStore.messages(c.GetInt("id"), c.Param("session_id"), after)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": err.Error()})
		return
	}
	common.ApiSuccess(c, gin.H{"messages": messages})
}
