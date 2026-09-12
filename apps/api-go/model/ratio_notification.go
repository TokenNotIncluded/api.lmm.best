package model

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// One event represents an entire committed option batch. Payloads are never
// exposed directly: both the feed and dispatcher must apply current visibility.
type RatioNotification struct {
	ID          string `gorm:"primaryKey;size:36" json:"event_id"`
	Changes     string `gorm:"type:text" json:"-"`
	EffectiveAt int64  `json:"effective_at"`
	Cursor      int    `json:"-"`
	Expanded    bool   `gorm:"index" json:"-"`
}
type RatioDelivery struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	EventID   string `gorm:"size:36;uniqueIndex:ratio_recipient" json:"event_id"`
	UserID    int    `gorm:"uniqueIndex:ratio_recipient" json:"user_id"`
	Attempts  int    `json:"attempts"`
	NextAt    int64  `gorm:"index" json:"next_at"`
	Status    string `gorm:"size:16;index" json:"status"`
	LastError string `gorm:"size:200" json:"last_error"`
}
type RatioChange struct {
	Option    string `json:"option"`
	Model     string `json:"model,omitempty"`
	Group     string `json:"group,omitempty"`
	UserGroup string `json:"user_group,omitempty"`
	Old       any    `json:"old"`
	New       any    `json:"new"`
}

func ratioOption(key string) bool {
	switch key {
	case "ModelPrice", "ModelRatio", "CompletionRatio", "CacheRatio", "CreateCacheRatio", "ImageRatio", "AudioRatio", "AudioCompletionRatio", "GroupRatio", "GroupGroupRatio":
		return true
	}
	return false
}

func ratioChanges(key, before, after string) ([]RatioChange, error) {
	var a, b map[string]any
	if before == "" {
		before = "{}"
	}
	if err := json.Unmarshal([]byte(before), &a); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(after), &b); err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for k := range a {
		names[k] = true
	}
	for k := range b {
		names[k] = true
	}
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var changes []RatioChange
	for _, k := range keys {
		if reflect.DeepEqual(a[k], b[k]) {
			continue
		}
		if key == "GroupGroupRatio" {
			aa, _ := json.Marshal(a[k])
			bb, _ := json.Marshal(b[k])
			nested, err := ratioChanges("GroupRatio", string(aa), string(bb))
			if err != nil {
				return nil, err
			}
			for _, c := range nested {
				c.Option = key
				c.UserGroup = k
				changes = append(changes, c)
			}
			continue
		}
		c := RatioChange{Option: key, Old: a[k], New: b[k]}
		if key == "GroupRatio" {
			c.Group = k
		} else {
			c.Model = k
		}
		changes = append(changes, c)
	}
	return changes, nil
}

// Called before option upserts, inside the same transaction. An outbox failure
// aborts the save; network delivery failures can never undo a committed price.
func recordRatioNotification(tx *gorm.DB, values map[string]string) error {
	var changes []RatioChange
	for _, key := range sortedOptionUpdateKeys(values) {
		if !ratioOption(key) {
			continue
		}
		var row Option
		err := tx.Where("key = ?", key).First(&row).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.OptionMapRWMutex.RLock()
			row.Value = common.OptionMap[key]
			common.OptionMapRWMutex.RUnlock()
		}
		diff, err := ratioChanges(key, row.Value, values[key])
		if err != nil {
			return err
		}
		changes = append(changes, diff...)
	}
	if len(changes) == 0 {
		return nil
	}
	payload, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	return tx.Create(&RatioNotification{ID: uuid.NewString(), Changes: string(payload), EffectiveAt: time.Now().Unix()}).Error
}

// Conservative scope: only the user's current billing group and its enabled
// models. Never expose another user's group overrides, even to an admin feed.
func VisibleRatioChanges(event RatioNotification, user User) ([]RatioChange, error) {
	var all []RatioChange
	if err := json.Unmarshal([]byte(event.Changes), &all); err != nil {
		return nil, err
	}
	if user.Status != common.UserStatusEnabled {
		return []RatioChange{}, nil
	}
	models, err := GetGroupEnabledModelsWithError(user.Group)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, m := range models {
		allowed[m] = true
	}
	visible := []RatioChange{}
	for _, c := range all {
		if c.UserGroup != "" && c.UserGroup != user.Group {
			continue
		}
		if c.Group != "" && c.Group != user.Group {
			continue
		}
		if c.Model != "" && !allowed[c.Model] {
			continue
		}
		visible = append(visible, c)
	}
	return visible, nil
}

// Persist a bounded fanout cursor so restart and overlapping workers cannot
// lose recipients. Existing webhook preferences are the subscription mechanism.
func ExpandRatioNotification(event RatioNotification) error {
	var users []User
	if err := DB.Where("id > ? AND status = ?", event.Cursor, common.UserStatusEnabled).Order("id").Limit(100).Find(&users).Error; err != nil {
		return err
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		cursor := event.Cursor
		for _, u := range users {
			cursor = u.Id
			s := u.GetSetting()
			if s.NotifyType != "webhook" || s.WebhookUrl == "" {
				continue
			}
			d := RatioDelivery{EventID: event.ID, UserID: u.Id, Status: "pending"}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&d).Error; err != nil {
				return err
			}
		}
		return tx.Model(&RatioNotification{}).Where("id = ? AND cursor = ?", event.ID, event.Cursor).Updates(map[string]any{"cursor": cursor, "expanded": len(users) < 100}).Error
	})
}
