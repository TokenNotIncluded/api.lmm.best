package model

import (
	"errors"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// Gift 限时补偿礼包。管理员创建后在 [StartAt, EndAt) 窗口内对满足门槛的
// 用户可见，用户需主动领取。窗口通过查询时判断生效，无需额外定时任务。
type Gift struct {
	Id                int    `json:"id" gorm:"primaryKey;autoIncrement"`
	Title             string `json:"title" gorm:"type:varchar(64);not null"`
	Description       string `json:"description" gorm:"type:varchar(255);default:''"`
	Quota             int    `json:"quota" gorm:"not null"`
	StartAt           int64  `json:"start_at" gorm:"bigint;not null;index"`
	EndAt             int64  `json:"end_at" gorm:"bigint;not null;index"`
	MinUsedQuota      int    `json:"min_used_quota" gorm:"not null;default:0"`       // 历史消耗门槛（防刷）
	MinAccountAgeDays int    `json:"min_account_age_days" gorm:"not null;default:0"` // 账号注册天数门槛（防刷）
	Enabled           bool   `json:"enabled" gorm:"not null;default:true"`
	CreatedAt         int64  `json:"created_at" gorm:"bigint"`
}

func (Gift) TableName() string {
	return "gifts"
}

// GiftClaim 礼包领取记录。(gift_id, user_id) 唯一索引保证幂等，
// 并发重复领取会被数据库唯一约束拒绝。
type GiftClaim struct {
	Id        int    `json:"id" gorm:"primaryKey;autoIncrement"`
	GiftId    int    `json:"gift_id" gorm:"not null;uniqueIndex:idx_gift_user"`
	UserId    int    `json:"user_id" gorm:"not null;uniqueIndex:idx_gift_user"`
	Username  string `json:"username" gorm:"type:varchar(64);default:'';index"`
	Quota     int    `json:"quota" gorm:"not null"`
	CreatedAt int64  `json:"created_at" gorm:"bigint"`
}

func (GiftClaim) TableName() string {
	return "gift_claims"
}

// GiftWithClaimStatus 用户视角的礼包（附带是否已领取）
type GiftWithClaimStatus struct {
	Gift
	Claimed   bool   `json:"claimed" gorm:"-:all"`
	ClaimedAt int64  `json:"claimed_at,omitempty" gorm:"-:all"`
	Eligible  bool   `json:"eligible" gorm:"-:all"`
	Reason    string `json:"reason,omitempty" gorm:"-:all"`
}

var (
	ErrGiftNotFound       = errors.New("礼包不存在或未启用")
	ErrGiftNotStarted     = errors.New("礼包尚未开始")
	ErrGiftExpired        = errors.New("礼包已过期")
	ErrGiftAlreadyClaimed = errors.New("已领取过该礼包")
	ErrGiftNotEligible    = errors.New("不满足领取条件")
)

func GetGiftById(id int) (*Gift, error) {
	var gift Gift
	if err := DB.First(&gift, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &gift, nil
}

func GetAllGifts() ([]Gift, error) {
	var gifts []Gift
	err := DB.Order("id DESC").Find(&gifts).Error
	return gifts, err
}

func CreateGift(gift *Gift) error {
	gift.CreatedAt = time.Now().Unix()
	return DB.Create(gift).Error
}

func UpdateGift(gift *Gift) error {
	return DB.Model(&Gift{}).Where("id = ?", gift.Id).Updates(map[string]interface{}{
		"title":                gift.Title,
		"description":          gift.Description,
		"quota":                gift.Quota,
		"start_at":             gift.StartAt,
		"end_at":               gift.EndAt,
		"min_used_quota":       gift.MinUsedQuota,
		"min_account_age_days": gift.MinAccountAgeDays,
		"enabled":              gift.Enabled,
	}).Error
}

func GetGiftClaims(giftId int, page int, pageSize int) ([]GiftClaim, int64, error) {
	var claims []GiftClaim
	var total int64
	query := DB.Model(&GiftClaim{})
	if giftId > 0 {
		query = query.Where("gift_id = ?", giftId)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&claims).Error
	return claims, total, err
}

// checkGiftEligibility 校验用户领取资格（窗口、门槛、是否已领）。
// 返回礼包与错误；err 为 nil 表示可领取。
func checkGiftEligibility(gift *Gift, user *User, now int64) error {
	if !gift.Enabled {
		return ErrGiftNotFound
	}
	if now < gift.StartAt {
		return ErrGiftNotStarted
	}
	if now >= gift.EndAt {
		return ErrGiftExpired
	}
	if gift.MinAccountAgeDays > 0 {
		minCreatedAt := now - int64(gift.MinAccountAgeDays)*86400
		if user.CreatedAt > minCreatedAt {
			return ErrGiftNotEligible
		}
	}
	if gift.MinUsedQuota > 0 && user.UsedQuota < gift.MinUsedQuota {
		return ErrGiftNotEligible
	}
	return nil
}

// GetAvailableGiftsForUser 返回当前全部礼包及该用户的领取状态。
// 已过期太久的礼包不展示（避免列表无限增长）。
func GetAvailableGiftsForUser(userId int) ([]GiftWithClaimStatus, error) {
	user, err := GetUserById(userId, true)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	// 展示：未结束 或 结束不超过 7 天（让用户看到"已过期"状态）
	var gifts []Gift
	if err := DB.Where("enabled = ? AND end_at > ?", true, now-7*86400).
		Order("id DESC").Find(&gifts).Error; err != nil {
		return nil, err
	}

	claimedMap := map[int]GiftClaim{}
	if len(gifts) > 0 {
		giftIds := make([]int, 0, len(gifts))
		for _, g := range gifts {
			giftIds = append(giftIds, g.Id)
		}
		var claims []GiftClaim
		if err := DB.Where("user_id = ? AND gift_id IN ?", userId, giftIds).Find(&claims).Error; err != nil {
			return nil, err
		}
		for _, cl := range claims {
			claimedMap[cl.GiftId] = cl
		}
	}

	result := make([]GiftWithClaimStatus, 0, len(gifts))
	for _, g := range gifts {
		item := GiftWithClaimStatus{Gift: g}
		if cl, ok := claimedMap[g.Id]; ok {
			item.Claimed = true
			item.ClaimedAt = cl.CreatedAt
		}
		if err := checkGiftEligibility(&g, user, now); err != nil {
			item.Eligible = false
			item.Reason = err.Error()
		} else {
			item.Eligible = true
		}
		result = append(result, item)
	}
	return result, nil
}

// ClaimGift 用户主动领取礼包。资格校验 + 领取记录 + 加额度保持原子性：
// MySQL/PostgreSQL 走事务；SQLite 走顺序操作 + 失败回滚（与签到一致）。
// alreadyClaimed 返回 true 表示此前已领取过（额度不会重复发放），
// 调用方应将其视为成功的幂等响应而非错误。
func ClaimGift(userId int, giftId int) (claim *GiftClaim, alreadyClaimed bool, err error) {
	user, err := GetUserById(userId, true)
	if err != nil {
		return nil, false, err
	}
	gift, err := GetGiftById(giftId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, ErrGiftNotFound
		}
		return nil, false, err
	}
	now := time.Now().Unix()
	if err := checkGiftEligibility(gift, user, now); err != nil {
		return nil, false, err
	}

	claim = &GiftClaim{
		GiftId:    giftId,
		UserId:    userId,
		Username:  user.Username,
		Quota:     gift.Quota,
		CreatedAt: now,
	}

	var createErr error
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		createErr = claimGiftWithoutTransaction(claim, userId, gift.Quota)
	} else {
		createErr = claimGiftWithTransaction(claim, userId, gift.Quota)
	}
	if createErr != nil {
		// 唯一索引冲突 = 已领取过：返回既有领取记录，让调用方按幂等成功处理
		if errors.Is(createErr, ErrGiftAlreadyClaimed) {
			var existing GiftClaim
			if err := DB.Where("gift_id = ? AND user_id = ?", giftId, userId).
				First(&existing).Error; err == nil {
				return &existing, true, nil
			}
			return nil, false, createErr
		}
		return nil, false, createErr
	}
	return claim, false, nil
}

func claimGiftWithTransaction(claim *GiftClaim, userId int, quota int) error {
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(claim).Error; err != nil {
			return ErrGiftAlreadyClaimed
		}
		if err := tx.Model(&User{}).Where("id = ?", userId).
			Update("quota", gorm.Expr("quota + ?", quota)).Error; err != nil {
			return errors.New("领取失败：更新额度出错")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if common.RedisEnabled {
		go func() {
			_ = cacheIncrUserQuota(userId, int64(quota))
		}()
	}
	return nil
}

// claimGiftWithoutTransaction 不使用事务执行领取（适用于 SQLite）
func claimGiftWithoutTransaction(claim *GiftClaim, userId int, quota int) error {
	if err := DB.Create(claim).Error; err != nil {
		return ErrGiftAlreadyClaimed
	}
	// 使用 db=true 强制直接写入数据库，不使用批量更新
	if err := IncreaseUserQuota(userId, quota, true); err != nil {
		DB.Delete(claim)
		return errors.New("领取失败：更新额度出错")
	}
	return nil
}
