package model

import (
	"errors"
	"os"

	"github.com/LIghtJUNction/api.lmm.best/pkg/account"
	"gorm.io/gorm"
)

// NativeAccountRecord is resource ownership, not another login or wallet.
// Personal IDs retain user IDs. Team IDs are generated independently. Both
// fields participate in every resource reference and foreign key.
type NativeAccountRecord struct {
	Kind             account.Kind `json:"kind" gorm:"primaryKey;size:16;uniqueIndex:idx_accounts_owner,priority:1;check:ck_accounts_kind,kind IN ('personal','team')"`
	ID               int64        `json:"id" gorm:"primaryKey;autoIncrement:false;check:ck_accounts_identity,id > 0 AND (kind <> 'personal' OR id = owner_user_id)"`
	OwnerUserID      int          `json:"-" gorm:"not null;uniqueIndex:idx_accounts_owner,priority:2"`
	DisplayName      string       `json:"display_name" gorm:"size:80;not null"`
	Enabled          bool         `json:"enabled" gorm:"not null"`
	CreationKey      string       `json:"-" gorm:"size:64;not null"`
	OwnershipVersion int64        `json:"-" gorm:"not null;default:1;check:ck_accounts_owner_version,ownership_version > 0"`
	CreatedAt        int64        `json:"created_at" gorm:"autoCreateTime"`
	Owner            User         `json:"-" gorm:"foreignKey:OwnerUserID;references:Id;constraint:OnDelete:RESTRICT"`
}

func (NativeAccountRecord) TableName() string  { return "accounts" }
func (a NativeAccountRecord) Ref() account.Ref { return account.Ref{Kind: a.Kind, ID: a.ID} }

// The owner exists only in accounts.owner_user_id. Never store a second owner
// role here: exactly one owner is structural, not two fields to keep in sync.
// Removed members remain inactive so a future budget ledger retains its owner.
type NativeAccountMember struct {
	AccountKind account.Kind        `json:"-" gorm:"primaryKey;size:16;check:ck_account_members_kind,account_kind = 'team'"`
	AccountID   int64               `json:"-" gorm:"primaryKey;autoIncrement:false"`
	UserID      int                 `json:"user_id" gorm:"primaryKey;autoIncrement:false;index"`
	Role        account.TeamRole    `json:"role" gorm:"size:16;not null;check:ck_account_members_role,role IN ('admin','member')"`
	Active      bool                `json:"active" gorm:"not null"`
	Version     int64               `json:"-" gorm:"not null;check:ck_account_members_version,version > 0"`
	Account     NativeAccountRecord `json:"-" gorm:"foreignKey:AccountKind,AccountID;references:Kind,ID;constraint:OnDelete:RESTRICT"`
	User        User                `json:"-" gorm:"foreignKey:UserID;references:Id;constraint:OnDelete:RESTRICT"`
}

func (NativeAccountMember) TableName() string { return "account_members" }

type NativeAccountInvitation struct {
	ID                   string              `json:"id" gorm:"primaryKey;size:32"`
	AccountKind          account.Kind        `json:"-" gorm:"size:16;not null;uniqueIndex:idx_account_invite_request,priority:1;check:ck_account_invites_kind,account_kind = 'team'"`
	AccountID            int64               `json:"account_id" gorm:"not null;uniqueIndex:idx_account_invite_request,priority:2"`
	InviterUserID        int                 `json:"inviter_user_id" gorm:"not null;uniqueIndex:idx_account_invite_request,priority:3"`
	RequestKey           string              `json:"-" gorm:"size:64;not null;uniqueIndex:idx_account_invite_request,priority:4"`
	RecipientUserID      int                 `json:"recipient_user_id" gorm:"not null;index"`
	Role                 account.TeamRole    `json:"role" gorm:"size:16;not null;check:ck_account_invites_role,role IN ('admin','member')"`
	InviterRole          account.TeamRole    `json:"-" gorm:"size:16;not null"`
	InviterMemberVersion int64               `json:"-" gorm:"not null"`
	InviterAuthVersion   int64               `json:"-" gorm:"not null"`
	Status               string              `json:"status" gorm:"size:16;not null;check:ck_account_invites_status,status IN ('pending','accepted','revoked')"`
	CreatedAt            int64               `json:"created_at" gorm:"autoCreateTime"`
	ExpiresAt            int64               `json:"expires_at" gorm:"not null"`
	Account              NativeAccountRecord `json:"-" gorm:"foreignKey:AccountKind,AccountID;references:Kind,ID;constraint:OnDelete:RESTRICT"`
	Inviter              User                `json:"-" gorm:"foreignKey:InviterUserID;references:Id;constraint:OnDelete:RESTRICT"`
	Recipient            User                `json:"-" gorm:"foreignKey:RecipientUserID;references:Id;constraint:OnDelete:RESTRICT"`
}

func (NativeAccountInvitation) TableName() string { return "account_invitations" }

// Membership changes and their audit records commit or roll back together.
// Do not store credentials, email addresses or request bodies in this journal.
type NativeAccountEvent struct {
	ID           int64            `gorm:"primaryKey"`
	AccountKind  account.Kind     `gorm:"size:16;not null;index:idx_account_events_account,priority:1"`
	AccountID    int64            `gorm:"not null;index:idx_account_events_account,priority:2"`
	ActorUserID  int              `gorm:"not null"`
	TargetUserID int              `gorm:"not null"`
	Action       string           `gorm:"size:32;not null"`
	Role         account.TeamRole `gorm:"size:16"`
	CreatedAt    int64            `gorm:"autoCreateTime"`
}

func (NativeAccountEvent) TableName() string { return "account_events" }

func NativeAccountsEnabledFromEnv() (bool, error) {
	switch os.Getenv("NATIVE_ACCOUNTS_ENABLED") {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, errors.New("NATIVE_ACCOUNTS_ENABLED must be true or false")
	}
}

func nativeAccountModels() []interface{} {
	return []interface{}{&NativeAccountRecord{}, &NativeAccountMember{}, &NativeAccountInvitation{}, &NativeAccountEvent{}}
}

func nativeAccountMigrationModels(db *gorm.DB) ([]interface{}, error) {
	enabled, err := NativeAccountsEnabledFromEnv()
	if err != nil || !enabled {
		return nil, err
	}
	if db == nil {
		return nil, gorm.ErrInvalidDB
	}
	switch db.Dialector.Name() {
	case "postgres":
		return nativeAccountModels(), nil
	default:
		return nil, errors.New("native account HTTP pilot requires PostgreSQL")
	}
}

// Route registration verifies the same schema as migrate --apply/--verify. It
// must never run DDL or backfill user/payment records outside startup migration.
func EnsureNativeAccountSchemaAtStartup(db *gorm.DB) error {
	models, err := nativeAccountMigrationModels(db)
	if err != nil || len(models) == 0 {
		return err
	}
	return verifyIdentityStorageSchema(db, models)
}
