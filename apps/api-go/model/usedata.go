package model

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// QuotaData 柱状图数据
type QuotaData struct {
	Id        int    `json:"id"`
	UserID    int    `json:"user_id" gorm:"index"`
	Username  string `json:"username" gorm:"index:idx_qdt_model_user_name,priority:2;size:64;default:''"`
	ModelName string `json:"model_name" gorm:"index:idx_qdt_model_user_name,priority:1;size:64;default:''"`
	CreatedAt int64  `json:"created_at" gorm:"bigint;index:idx_qdt_created_at,priority:2"`
	UseGroup  string `json:"use_group" gorm:"index;size:64;default:''"`
	TokenID   int    `json:"token_id" gorm:"index;default:0"`
	ChannelID int    `json:"channel_id" gorm:"index;default:0"`
	NodeName  string `json:"node_name" gorm:"index;size:64;default:''"`
	TokenUsed int    `json:"token_used" gorm:"default:0"`
	Count     int    `json:"count" gorm:"default:0"`
	Quota     int    `json:"quota" gorm:"default:0"`
}

type QuotaDataLogParams struct {
	UserID    int
	Username  string
	ModelName string
	Quota     int
	CreatedAt int64
	TokenUsed int
	UseGroup  string
	TokenID   int
	ChannelID int
	NodeName  string
}

func UpdateQuotaData() {
	UpdateQuotaDataContext(context.Background())
}

// UpdateQuotaDataContext runs the dashboard flush loop until ctx is cancelled.
func UpdateQuotaDataContext(ctx context.Context) {
	for {
		interval := time.Duration(common.DataExportInterval) * time.Minute
		if interval < time.Minute {
			interval = time.Minute
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		case <-quotaDataFlushWake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		if common.DataExportEnabled {
			common.SysLog("正在更新数据看板数据...")
			SaveQuotaDataCache()
		}
	}
}

const quotaDataMaxEntries = 8192
const quotaDataFlushWatermark = quotaDataMaxEntries * 3 / 4

var CacheQuotaData = make(map[string]*QuotaData)
var CacheQuotaDataLock sync.Mutex
var quotaDataFlushLock sync.Mutex
var quotaDataDropped int64
var quotaDataFlushWake = make(chan struct{}, 1)

// quotaDataMaxQueryRows bounds the number of grouped rows materialized for a
// dashboard request. A missing time range must never turn a UI read into an
// unbounded historical export, and a very high-cardinality grouping should
// fail closed before JSON encoding amplifies the allocation.
const quotaDataMaxQueryRows = 100_000

var ErrQuotaDataResultTooLarge = errors.New("quota data result exceeds safety limit")

func quotaDataQueryLimit(query *gorm.DB) *gorm.DB {
	return query.Limit(quotaDataMaxQueryRows + 1)
}

func checkQuotaDataQueryRows(rows []*QuotaData) error {
	if len(rows) > quotaDataMaxQueryRows {
		return ErrQuotaDataResultTooLarge
	}
	return nil
}

func quotaDataKey(quotaData *QuotaData) string {
	return fmt.Sprintf("%d\x00%s\x00%s\x00%d\x00%s\x00%d\x00%d\x00%s",
		quotaData.UserID,
		quotaData.Username,
		quotaData.ModelName,
		quotaData.CreatedAt,
		quotaData.UseGroup,
		quotaData.TokenID,
		quotaData.ChannelID,
		quotaData.NodeName,
	)
}

func mergeQuotaData(cache map[string]*QuotaData, quotaData *QuotaData, limit int) bool {
	key := quotaDataKey(quotaData)
	if cached, ok := cache[key]; ok {
		cached.Count += quotaData.Count
		cached.Quota += quotaData.Quota
		cached.TokenUsed += quotaData.TokenUsed
		return true
	}
	if len(cache) >= limit {
		return false
	}
	cache[key] = quotaData
	return true
}

func LogQuotaData(params QuotaDataLogParams) {
	// 只精确到小时
	createdAt := params.CreatedAt - (params.CreatedAt % 3600)
	quotaData := &QuotaData{
		UserID:    params.UserID,
		Username:  params.Username,
		ModelName: params.ModelName,
		CreatedAt: createdAt,
		UseGroup:  params.UseGroup,
		TokenID:   params.TokenID,
		ChannelID: params.ChannelID,
		NodeName:  params.NodeName,
		Count:     1,
		Quota:     params.Quota,
		TokenUsed: params.TokenUsed,
	}

	CacheQuotaDataLock.Lock()
	stored := mergeQuotaData(CacheQuotaData, quotaData, quotaDataMaxEntries)
	if !stored {
		quotaDataDropped += int64(quotaData.Count)
	}
	shouldFlush := stored && len(CacheQuotaData) >= quotaDataFlushWatermark
	CacheQuotaDataLock.Unlock()
	if shouldFlush {
		select {
		case quotaDataFlushWake <- struct{}{}:
		default:
		}
	}
}

func SaveQuotaDataCache() {
	quotaDataFlushLock.Lock()
	defer quotaDataFlushLock.Unlock()

	pending, dropped := takeQuotaBatch()
	if len(pending) == 0 {
		if dropped > 0 {
			common.SysLog(fmt.Sprintf("quota dashboard flush: persisted=0 failed=0 dropped=%d", dropped))
		}
		return
	}

	failed, firstError := persistQuotaBatch(pending)
	restoreQuotaBatch(failed)
	if firstError != nil {
		common.SysLog(fmt.Sprintf("quota dashboard flush: persisted=%d failed=%d dropped=%d error=%q", len(pending)-len(failed), len(failed), dropped, firstError.Error()))
		return
	}
	common.SysLog(fmt.Sprintf("quota dashboard flush: persisted=%d failed=0 dropped=%d", len(pending), dropped))
}

func takeQuotaBatch() (map[string]*QuotaData, int64) {
	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	pending := CacheQuotaData
	CacheQuotaData = make(map[string]*QuotaData)
	dropped := quotaDataDropped
	quotaDataDropped = 0
	return pending, dropped
}

func persistQuotaBatch(pending map[string]*QuotaData) ([]*QuotaData, error) {
	failed := make([]*QuotaData, 0)
	var firstError error
	for _, quotaData := range pending {
		if err := persistQuotaData(quotaData); err != nil {
			failed = append(failed, quotaData)
			if firstError == nil {
				firstError = err
			}
		}
	}
	return failed, firstError
}

func restoreQuotaBatch(failed []*QuotaData) {
	if len(failed) == 0 {
		return
	}
	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	for _, quotaData := range failed {
		if !mergeQuotaData(CacheQuotaData, quotaData, quotaDataMaxEntries) {
			quotaDataDropped += int64(quotaData.Count)
		}
	}
}

func persistQuotaData(quotaData *QuotaData) error {
	query := DB.Table("quota_data").
		Where("user_id = ? and username = ? and model_name = ? and created_at = ? and use_group = ? and token_id = ? and channel_id = ? and node_name = ?",
			quotaData.UserID, quotaData.Username, quotaData.ModelName, quotaData.CreatedAt, quotaData.UseGroup, quotaData.TokenID, quotaData.ChannelID, quotaData.NodeName)
	var existing QuotaData
	if err := query.First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return DB.Table("quota_data").Create(quotaData).Error
		}
		return err
	}
	guardedQuery, err := GuardWalletQuotaDelta(query, quotaData.Quota)
	if err != nil {
		return err
	}
	result := guardedQuery.Updates(map[string]interface{}{
		"count":      gorm.Expr("count + ?", quotaData.Count),
		"quota":      gorm.Expr("quota + ?", quotaData.Quota),
		"token_used": gorm.Expr("token_used + ?", quotaData.TokenUsed),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrWalletQuotaOutOfRange
	}
	return nil
}

func GetQuotaDataByUsername(username string, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	err = quotaDataQueryLimit(DB.Table("quota_data").
		Select("user_id, username, model_name, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used").
		Where("username = ? and created_at >= ? and created_at <= ?", username, startTime, endTime).
		Group("user_id, username, model_name, created_at")).
		Find(&quotaDatas).Error
	if err == nil {
		err = checkQuotaDataQueryRows(quotaDatas)
	}
	return quotaDatas, err
}

func GetQuotaDataByUserId(userId int, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	err = quotaDataQueryLimit(DB.Table("quota_data").
		Select("user_id, username, model_name, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used").
		Where("user_id = ? and created_at >= ? and created_at <= ?", userId, startTime, endTime).
		Group("user_id, username, model_name, created_at")).
		Find(&quotaDatas).Error
	if err == nil {
		err = checkQuotaDataQueryRows(quotaDatas)
	}
	return quotaDatas, err
}

func GetQuotaDataGroupByUser(startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	err = quotaDataQueryLimit(DB.Table("quota_data").
		Select("username, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used").
		Where("created_at >= ? and created_at <= ?", startTime, endTime).
		Group("username, created_at")).
		Find(&quotaDatas).Error
	if err == nil {
		err = checkQuotaDataQueryRows(quotaDatas)
	}
	return quotaDatas, err
}

func GetAllQuotaDates(startTime int64, endTime int64, username string) (quotaData []*QuotaData, err error) {
	if username != "" {
		return GetQuotaDataByUsername(username, startTime, endTime)
	}
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	// only select model_name, sum(count) as count, sum(quota) as quota, model_name, created_at from quota_data group by model_name, created_at;
	//err = DB.Table("quota_data").Where("created_at >= ? and created_at <= ?", startTime, endTime).Find(&quotaDatas).Error
	err = quotaDataQueryLimit(DB.Table("quota_data").Select("model_name, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, created_at").Where("created_at >= ? and created_at <= ?", startTime, endTime).Group("model_name, created_at")).Find(&quotaDatas).Error
	if err == nil {
		err = checkQuotaDataQueryRows(quotaDatas)
	}
	return quotaDatas, err
}
