package controller

import (
	"database/sql"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"gorm.io/gorm"
)

// Historical UsedQuota remains unchanged. A nil current projection is unknown,
// while an available zero is a real zero and must survive JSON serialization.
type usageProjectionFields struct {
	NormalizedUsedQuota      *int `json:"normalized_used_quota,omitempty"`
	UsageProjectionAvailable bool `json:"usage_projection_available"`
}

type usageProjector interface {
	User(userID, currentUsed int) (model.UsageProjection, error)
	Token(tokenID, userID, currentUsed int) (model.UsageProjection, error)
}

// Capture authoritative counters and immutable audit/refund state in one
// read-only snapshot. Display fallback never upgrades stale counters to known.
type usageSnapshot struct {
	projector usageProjector
	users     map[int]model.User
	tokens    map[int]model.Token
}

func loadUsageSnapshot(users []*model.User, tokens []*model.Token) *usageSnapshot {
	if model.DB == nil {
		return nil
	}
	snapshot := &usageSnapshot{users: map[int]model.User{}, tokens: map[int]model.Token{}}
	userIDs := make([]int, 0, len(users))
	tokenIDs := make([]int, 0, len(tokens))
	for _, user := range users {
		if user != nil {
			userIDs = append(userIDs, user.Id)
		}
	}
	for _, token := range tokens {
		if token != nil {
			tokenIDs = append(tokenIDs, token.Id)
		}
	}
	options := &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	switch model.DB.Dialector.Name() {
	case "postgres", "mysql":
		// Never fall back to read-committed for production databases.
	case "sqlite":
		// A SQLite read transaction pins a snapshot; its driver does not expose RR.
		options.Isolation = sql.LevelDefault
	default:
		return nil
	}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if len(userIDs) > 0 {
			var rows []model.User
			if err := tx.Select("id", "quota", "used_quota").Where("id IN ?", userIDs).Find(&rows).Error; err != nil {
				return err
			}
			for _, row := range rows {
				snapshot.users[row.Id] = row
			}
		}
		if len(tokenIDs) > 0 {
			var rows []model.Token
			if err := tx.Select("id", "user_id", "remain_quota", "used_quota", "unlimited_quota", "expired_time").Where("id IN ?", tokenIDs).Find(&rows).Error; err != nil {
				return err
			}
			for _, row := range rows {
				snapshot.tokens[row.Id] = row
			}
		}
		projector, err := model.LoadUsageProjector(tx)
		if err != nil {
			return err
		}
		snapshot.projector = projector
		return nil
	}, options)
	if err != nil {
		return nil
	}
	return snapshot
}

func (snapshot *usageSnapshot) user(user *model.User) (*model.User, usageProjector) {
	if user == nil {
		return nil, nil
	}
	copy := *user
	if snapshot != nil {
		if current, ok := snapshot.users[user.Id]; ok {
			copy.UsedQuota, copy.Quota = current.UsedQuota, current.Quota
			return &copy, snapshot.projector
		}
	}
	return &copy, nil
}

func (snapshot *usageSnapshot) token(token *model.Token) (*model.Token, usageProjector) {
	if token == nil {
		return nil, nil
	}
	copy := *token
	if snapshot != nil {
		if current, ok := snapshot.tokens[token.Id]; ok && current.UserId == token.UserId {
			copy.UsedQuota, copy.RemainQuota = current.UsedQuota, current.RemainQuota
			copy.UnlimitedQuota, copy.ExpiredTime = current.UnlimitedQuota, current.ExpiredTime
			return &copy, snapshot.projector
		}
	}
	return &copy, nil
}

func projectUserUsage(projector usageProjector, user *model.User) usageProjectionFields {
	if projector == nil || user == nil {
		return usageProjectionFields{}
	}
	projection, err := projector.User(user.Id, user.UsedQuota)
	if err != nil {
		return usageProjectionFields{}
	}
	value := projection.NormalizedUsedQuota
	return usageProjectionFields{NormalizedUsedQuota: &value, UsageProjectionAvailable: true}
}

func projectTokenUsage(projector usageProjector, token *model.Token) usageProjectionFields {
	if projector == nil || token == nil {
		return usageProjectionFields{}
	}
	projection, err := projector.Token(token.Id, token.UserId, token.UsedQuota)
	if err != nil {
		return usageProjectionFields{}
	}
	value := projection.NormalizedUsedQuota
	return usageProjectionFields{NormalizedUsedQuota: &value, UsageProjectionAvailable: true}
}

func captureBillingUsage(token *model.Token, userID, remain, used int) (usageProjectionFields, int) {
	if token != nil {
		snapshot := loadUsageSnapshot(nil, []*model.Token{token})
		current, projector := snapshot.token(token)
		// Keep SDK lifecycle/limit fields from the same token snapshot too.
		*token = *current
		return projectTokenUsage(projector, current), current.RemainQuota
	}
	user := &model.User{Id: userID, Quota: remain, UsedQuota: used}
	snapshot := loadUsageSnapshot([]*model.User{user}, nil)
	current, projector := snapshot.user(user)
	return projectUserUsage(projector, current), current.Quota
}
