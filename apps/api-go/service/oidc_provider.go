package service

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oidcprovider"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type oidcRecord struct {
	Key       string `gorm:"primaryKey;size:160"`
	Value     string `gorm:"type:text;not null"`
	Owner     string `gorm:"size:128;index:idx_oidc_owner_expiry"`
	ExpiresAt int64  `gorm:"index;index:idx_oidc_owner_expiry"`
}

func (oidcRecord) TableName() string { return "lmm_oidc_records" }

type oidcStore struct{ db *gorm.DB }

func (s oidcStore) Set(ctx context.Context, key string, value []byte, expires int64, owner string) error {
	row := oidcRecord{key, string(value), owner, expires}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value", "owner", "expires_at"})}).Create(&row).Error
}
func (s oidcStore) Get(ctx context.Context, key string) ([]byte, error) {
	var row oidcRecord
	e := s.db.WithContext(ctx).Where("key = ? AND expires_at > ?", key, time.Now().Unix()).First(&row).Error
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, oidcprovider.ErrMissing
	}
	return []byte(row.Value), e
}
func (s oidcStore) Take(ctx context.Context, key string) ([]byte, error) {
	var value []byte
	e := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row oidcRecord
		e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("key = ? AND expires_at > ?", key, time.Now().Unix()).First(&row).Error
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return oidcprovider.ErrMissing
		}
		if e != nil {
			return e
		}
		deleted := tx.Where("key = ? AND value = ?", key, row.Value).Delete(&oidcRecord{})
		if deleted.Error != nil {
			return deleted.Error
		}
		if deleted.RowsAffected != 1 {
			return oidcprovider.ErrMissing
		}
		// Publish the rotation tombstone in the SAME transaction as consumption.
		// Otherwise a simultaneous replay could arrive before the handler records it.
		if strings.HasPrefix(key, "refresh:") {
			marker := oidcRecord{Key: "used-refresh:" + strings.TrimPrefix(key, "refresh:"), Value: row.Value, Owner: row.Owner, ExpiresAt: row.ExpiresAt}
			if e := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&marker).Error; e != nil {
				return e
			}
		}
		value = []byte(row.Value)
		return nil
	})
	return value, e
}
func (s oidcStore) Delete(ctx context.Context, key string) error {
	return s.db.WithContext(ctx).Where("key = ?", key).Delete(&oidcRecord{}).Error
}
func (s oidcStore) Families(ctx context.Context, owner string) ([][]byte, error) {
	var rows []oidcRecord
	e := s.db.WithContext(ctx).Where("owner = ? AND key LIKE ? AND expires_at > ?", owner, "family:%", time.Now().Unix()).Order("expires_at DESC").Limit(100).Find(&rows).Error
	values := make([][]byte, 0, len(rows))
	for _, row := range rows {
		values = append(values, []byte(row.Value))
	}
	return values, e
}

// ConfigureSubprojectOIDC uses the SAME LMM user/session tables and verified
// refresh cookie as native OAuth. No group, account level or paid status is
// imported into a subproject's community identity or governance permissions.
func ConfigureSubprojectOIDC(db *gorm.DB) (*oidcprovider.Provider, error) {
	if os.Getenv("LMM_OIDC_ENABLED") != "true" {
		return nil, nil
	}
	if db == nil {
		return nil, fmt.Errorf("OIDC database is unavailable")
	}
	path := os.Getenv("LMM_OIDC_SIGNING_KEY_FILE")
	if path == "" {
		return nil, fmt.Errorf("LMM_OIDC_SIGNING_KEY_FILE is required")
	}
	encoded, e := os.ReadFile(path)
	if e != nil {
		return nil, fmt.Errorf("read OIDC signing key: %w", e)
	}
	block, rest := pem.Decode(encoded)
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, fmt.Errorf("expected exactly one PEM private key")
	}
	var key *rsa.PrivateKey
	if block.Type == "RSA PRIVATE KEY" {
		key, e = x509.ParsePKCS1PrivateKey(block.Bytes)
	} else {
		var parsed any
		parsed, e = x509.ParsePKCS8PrivateKey(block.Bytes)
		if e == nil {
			var ok bool
			key, ok = parsed.(*rsa.PrivateKey)
			if !ok {
				return nil, fmt.Errorf("OIDC key must be RSA")
			}
		}
	}
	if e != nil {
		return nil, e
	}
	var clients []oidcprovider.Client
	var resources []oidcprovider.Resource
	if e = json.Unmarshal([]byte(os.Getenv("LMM_OIDC_CLIENTS")), &clients); e != nil {
		return nil, fmt.Errorf("LMM_OIDC_CLIENTS: %w", e)
	}
	if e = json.Unmarshal([]byte(os.Getenv("LMM_OIDC_RESOURCES")), &resources); e != nil {
		return nil, fmt.Errorf("LMM_OIDC_RESOURCES: %w", e)
	}
	for i := range resources {
		if resources[i].SecretEnv == "" {
			return nil, fmt.Errorf("resource secret_env is required")
		}
		resources[i].Secret = os.Getenv(resources[i].SecretEnv)
	}
	var networks []*net.IPNet
	for _, raw := range strings.Split(os.Getenv("LMM_OIDC_TRUSTED_PROXY_CIDRS"), ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		_, network, e := net.ParseCIDR(raw)
		if e != nil {
			return nil, e
		}
		ones, _ := network.Mask.Size()
		if ones == 0 {
			return nil, fmt.Errorf("do not trust every address as an OIDC proxy")
		}
		networks = append(networks, network)
	}
	if e = db.AutoMigrate(&oidcRecord{}); e != nil {
		return nil, e
	}
	if e = db.Where("expires_at <= ?", time.Now().Unix()).Delete(&oidcRecord{}).Error; e != nil {
		return nil, e
	}
	browser := &OAuthIntegration{DB: db}
	issuer := os.Getenv("LMM_OIDC_ISSUER")
	if issuer == "" {
		issuer = "https://api.lmm.best/oidc"
	}
	return oidcprovider.New(oidcprovider.Config{Issuer: issuer, Clients: clients, Resources: resources, Key: key, Store: oidcStore{db}, TrustedProxies: networks,
		BrowserIdentity: func(ctx context.Context, r *http.Request) (oidcprovider.Identity, error) {
			current, user, e := browser.BrowserIdentity(ctx, r)
			if e != nil {
				return oidcprovider.Identity{}, e
			}
			return oidcprovider.Identity{Subject: "lmm:" + strconv.FormatInt(current.UserID, 10), Name: user.Username, SessionID: current.SessionID, SessionVersion: current.SessionVersion, AuthVersion: current.AuthVersion}, nil
		},
		ValidateIdentity: func(ctx context.Context, identity oidcprovider.Identity) error {
			if !strings.HasPrefix(identity.Subject, "lmm:") {
				return oidcprovider.ErrDenied
			}
			id, e := strconv.ParseInt(strings.TrimPrefix(identity.Subject, "lmm:"), 10, 64)
			if e != nil || id <= 0 {
				return oidcprovider.ErrDenied
			}
			var session model.UserSession
			if e = db.WithContext(ctx).Where("sid = ?", identity.SessionID).First(&session).Error; e != nil {
				return oidcprovider.ErrDenied
			}
			now := time.Now().Unix()
			if int64(session.UserID) != id || session.Status != model.UserSessionStatusActive || session.RevokedAt != 0 || session.ExpiresAt <= now || session.Version != identity.SessionVersion || session.UserAuthVersion != identity.AuthVersion {
				return oidcprovider.ErrDenied
			}
			var user model.User
			if e = db.WithContext(ctx).Where("id = ?", id).First(&user).Error; e != nil || user.Status != common.UserStatusEnabled || user.AuthVersion != identity.AuthVersion {
				return oidcprovider.ErrDenied
			}
			return enforceSessionAutoLogout(&session, user.GetSetting().IsSessionAutoLogoutEnabled(), now)
		},
	})
}
