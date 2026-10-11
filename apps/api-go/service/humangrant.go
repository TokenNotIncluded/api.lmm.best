package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/humangrant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// HumanGrant uses the existing database and login sessions. There is no second
// human account database and no new credential accepted by UserAuth or TokenAuth.
type HumanGrantStore struct{ DB *gorm.DB }

// Use the same table definitions as the controlled migration phase.
type humanGrantRecord = model.HumanGrantRecord
type humanGrantGate = model.HumanGrantGate

type humanGrantTx struct{ db *gorm.DB }

func NewHumanGrantStore(db *gorm.DB) (*HumanGrantStore, error) {
	if db == nil {
		return nil, errors.New("HumanGrant database unavailable")
	}
	// HG payloads include private consent and local mail content. Do not send
	// SQL statement arguments to the application logger.
	db = db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	// All schema writes run under the startup migration session. Read-only
	// replicas must never attempt a migration or insert during router setup.
	return &HumanGrantStore{DB: db}, nil
}
func (s *HumanGrantStore) Transaction(ctx context.Context, fn func(humangrant.Tx) error) error {
	return s.DB.WithContext(ctx).Transaction(func(db *gorm.DB) error {
		// A real UPDATE obtains the SQLite write lock or a PostgreSQL/MySQL row lock.
		// Every HG process uses this same gate, including revoke and the site switch.
		locked := db.Model(&humanGrantGate{}).Where("id = ?", 1).UpdateColumn("revision", gorm.Expr("revision + 1"))
		if locked.Error != nil {
			return locked.Error
		}
		if locked.RowsAffected != 1 {
			return errors.New("HumanGrant gate missing")
		}
		// Bounded expiry cleanup. Permanent settings and recipient blocks have zero
		// expiry. Do not log Payload: it can contain a private invitation address.
		var expired []humanGrantRecord
		if err := db.Select("kind", "id").Where("expires_at > 0 AND expires_at <= ?", time.Now().Unix()).Order("expires_at").Limit(64).Find(&expired).Error; err != nil {
			return err
		}
		for _, row := range expired {
			if err := db.Where("kind = ? AND id = ?", row.Kind, row.ID).Delete(&humanGrantRecord{}).Error; err != nil {
				return err
			}
		}
		return fn(&humanGrantTx{db: db})
	})
}
func (t *humanGrantTx) Get(kind, id string, value any) (bool, error) {
	var row humanGrantRecord
	err := t.db.Where("kind = ? AND id = ?", kind, id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if row.ExpiresAt > 0 && row.ExpiresAt <= time.Now().Unix() {
		return false, nil
	}
	return true, json.Unmarshal([]byte(row.Payload), value)
}
func (t *humanGrantTx) Put(kind, id, owner string, expires int64, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	row := humanGrantRecord{Kind: kind, ID: id, Owner: owner, ExpiresAt: expires, Payload: string(raw)}
	return t.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "kind"}, {Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"owner", "expires_at", "payload"})}).Create(&row).Error
}
func (t *humanGrantTx) Delete(kind, id string) error {
	return t.db.Where("kind = ? AND id = ?", kind, id).Delete(&humanGrantRecord{}).Error
}
func (t *humanGrantTx) List(kind, owner string, limit int) ([]json.RawMessage, error) {
	if limit < 1 || limit > 200 {
		return nil, errors.New("invalid list limit")
	}
	var rows []humanGrantRecord
	if err := t.db.Where("kind = ? AND owner = ? AND (expires_at = 0 OR expires_at > ?)", kind, owner, time.Now().Unix()).Order("id").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, 0, len(rows))
	for _, row := range rows {
		out = append(out, json.RawMessage(row.Payload))
	}
	return out, nil
}
func (t *humanGrantTx) Authenticate(r *http.Request) (humangrant.Human, humangrant.Account, bool, error) {
	// The shared verifier checks the real HttpOnly login cookie. In this
	// transaction, expired sessions fail without starting a nested global-DB write.
	identity, _, err := browserIdentity(r.Context(), t.db, r, false)
	if err != nil {
		return humangrant.Human{}, humangrant.Account{}, false, nil
	}
	h := humangrant.Human{ID: strconv.FormatInt(identity.UserID, 10), SessionID: identity.SessionID, SessionVersion: identity.SessionVersion, AuthVersion: identity.AuthVersion}
	a, ok, err := t.CurrentHuman(h)
	return h, a, ok, err
}
func (t *humanGrantTx) CurrentHuman(h humangrant.Human) (humangrant.Account, bool, error) {
	var empty humangrant.Account
	id, err := strconv.ParseInt(h.ID, 10, 64)
	if err != nil || id <= 0 {
		return empty, false, nil
	}
	var user model.User
	// Lock current account/session rows too: account disable, security-version
	// changes and session revoke outside HG must be ordered with the profile read.
	err = t.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, false, nil
	}
	if err != nil {
		return empty, false, err
	}
	var session model.UserSession
	err = t.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&session, "sid = ?", h.SessionID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, false, nil
	}
	if err != nil {
		return empty, false, err
	}
	now := time.Now().Unix()
	if user.Status != common.UserStatusEnabled || user.AuthVersion != h.AuthVersion || strconv.Itoa(session.UserID) != h.ID || session.Status != model.UserSessionStatusActive || session.RevokedAt != 0 || session.ExpiresAt <= now || session.Version != h.SessionVersion || session.UserAuthVersion != h.AuthVersion {
		return empty, false, nil
	}
	if user.GetSetting().IsSessionAutoLogoutEnabled() && session.CreatedAt < now-int64(model.UserSessionAutoLogoutAge/time.Second) {
		return empty, false, nil
	}
	return humangrant.Account{ID: strconv.Itoa(user.Id), Username: user.Username, DisplayName: user.DisplayName, Root: user.Role >= common.RoleRootUser}, true, nil
}
