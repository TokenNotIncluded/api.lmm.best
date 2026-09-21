package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/setting/console_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrAnnouncementOrder = errors.New("announcement changed or an earlier announcement must be acknowledged first")

type MandatoryAnnouncement struct {
	ID          int64  `json:"id"`
	Content     string `json:"content"`
	Extra       string `json:"extra,omitempty"`
	PublishDate string `json:"publishDate"`
	Mandatory   bool   `json:"mandatory"`
	// AckRevision is the administrator-controlled acknowledgement generation.
	// While it stays unchanged the body may be corrected freely; bumping it is
	// the explicit way to ask every reader to confirm the notice again.
	AckRevision string `json:"ackRevision,omitempty"`
	Revision    string `json:"revision"`
	ReadAt      int64  `json:"read_at"`
}

// A content revision has its own durable acknowledgement. Updating the text
// never silently attributes an old acknowledgement to new instructions.
type AnnouncementRead struct {
	ID             int64  `json:"id" gorm:"primaryKey"`
	UserID         int    `json:"user_id" gorm:"not null;uniqueIndex:idx_announcement_read"`
	AnnouncementID int64  `json:"announcement_id" gorm:"not null;uniqueIndex:idx_announcement_read"`
	Revision       string `json:"revision" gorm:"type:varchar(64);not null;uniqueIndex:idx_announcement_read"`
	ReadAt         int64  `json:"read_at" gorm:"not null"`
}

// announcementRevision derives the durable acknowledgement key. Pinning it to
// an administrator-controlled generation keeps a typo fix from forcing every
// reader who already confirmed through the notice again. A notice published
// before the field existed keeps hashing its content, so no acknowledgement
// recorded by an older release is invalidated by this change.
func announcementRevision(item MandatoryAnnouncement) string {
	fields := []any{item.ID, item.Content, item.Extra, item.PublishDate}
	if generation := strings.TrimSpace(item.AckRevision); generation != "" {
		fields = []any{item.ID, generation}
	}
	encoded, _ := json.Marshal(fields)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func currentMandatoryAnnouncements(now time.Time) ([]MandatoryAnnouncement, error) {
	result := []MandatoryAnnouncement{}
	raw := console_setting.GetConsoleSetting().Announcements
	if raw == "" {
		return result, nil
	}
	var all []MandatoryAnnouncement
	if err := json.Unmarshal([]byte(raw), &all); err != nil {
		return nil, err
	}
	for _, item := range all {
		if !item.Mandatory {
			continue
		}
		published, err := time.Parse(time.RFC3339, item.PublishDate)
		if err != nil {
			return nil, err
		}
		if published.After(now) {
			continue
		}
		item.Revision = announcementRevision(item)
		item.ReadAt = 0
		result = append(result, item)
	}
	sort.SliceStable(result, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339, result[i].PublishDate)
		b, _ := time.Parse(time.RFC3339, result[j].PublishDate)
		if a.Equal(b) {
			return result[i].ID < result[j].ID
		}
		return a.Before(b)
	})
	return result, nil
}

func announcementStatus(db *gorm.DB, userID int, now time.Time) ([]MandatoryAnnouncement, error) {
	items, err := currentMandatoryAnnouncements(now)
	if err != nil || len(items) == 0 {
		return items, err
	}
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	var reads []AnnouncementRead
	if err := db.Where("user_id = ? AND announcement_id IN ?", userID, ids).Find(&reads).Error; err != nil {
		return nil, err
	}
	byRevision := make(map[string]int64, len(reads))
	for _, read := range reads {
		byRevision[read.Revision] = read.ReadAt
	}
	for i := range items {
		items[i].ReadAt = byRevision[items[i].Revision]
	}
	return items, nil
}

func GetAnnouncementStatus(userID int) ([]MandatoryAnnouncement, error) {
	if userID <= 0 || DB == nil {
		return nil, gorm.ErrInvalidData
	}
	return announcementStatus(DB, userID, time.Now())
}

func AcknowledgeAnnouncement(userID int, id int64, revision string) error {
	if userID <= 0 || id <= 0 || len(revision) != 64 || DB == nil {
		return gorm.ErrInvalidData
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		// Serialize requests for the same account; unique key keeps retries safe.
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").First(&user, userID).Error; err != nil {
			return err
		}
		items, err := announcementStatus(tx, userID, time.Now())
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.ID == id && item.Revision == revision && item.ReadAt > 0 {
				return nil
			}
		}
		for _, item := range items {
			if item.ReadAt > 0 {
				continue
			}
			if item.ID != id || item.Revision != revision {
				return ErrAnnouncementOrder
			}
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&AnnouncementRead{UserID: userID, AnnouncementID: id, Revision: revision, ReadAt: time.Now().Unix()}).Error
		}
		return ErrAnnouncementOrder
	})
}
