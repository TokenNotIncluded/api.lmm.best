// Package service: dynamic pricing ticker.
//
// The ticker periodically aggregates consume logs over a sliding window
// (GROUP BY model, channel) and feeds the measurements into
// pkg/dynamic_pricing.Tick for every model seen in the window. Per-model
// pricing state is kept in memory and best-effort persisted to Redis via
// pkg/dynamic_pricing.SetState; each node runs its own ticker so the request
// path can read the multiplier locally.
//
// Cost semantics:
//   - Upstream cost is computed from the ADMIN-CONFIGURED channel costs
//     (setting/dynamic_pricing_setting.GetChannelCost), never from billed
//     quota amounts. A channel without a configured cost has its tokens
//     excluded from the upstream-cost calculation (and from cheap/backup
//     route selection). Request-path pricing separately fails closed for a
//     selected channel without a configured cost when RequireChannelCost is
//     enabled, so missing data cannot silently create upstream spend.
//   - Revenue is deliberately never used as a load input: charging more
//     under load would feed back into measured revenue and could spiral.
//     Only token/request volume and upstream cost drive the factor.
//
// The tick is a no-op while the feature is disabled, checked per tick so an
// admin can toggle dynamic_pricing_setting.enabled without a restart.
package service

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/dynamic_pricing"
	"github.com/LIghtJUNction/api.lmm.best/setting/dynamic_pricing_setting"

	"github.com/bytedance/gopkg/util/gopool"
)

var dynamicPricingTickerOnce sync.Once

// A single tick is intentionally bounded. If the database produces more
// aggregate rows than this, the tick fails closed instead of applying prices
// to an arbitrary prefix and pretending the result is complete.
const dynamicPricingMaxWindowRows = 100_000

// dynamicPricingWindowRow is one (model, channel) aggregate row from the
// consume-log window.
type dynamicPricingWindowRow struct {
	ModelName string `gorm:"column:model_name"`
	ChannelId int    `gorm:"column:channel_id"`
	Tokens    int64  `gorm:"column:tokens"`
	Requests  int64  `gorm:"column:requests"`
}

// dynamicPricingModelWindow holds the per-model aggregation of the window
// rows, plus the cheap/backup route costs derived from the configured channel
// costs of the channels seen in the window.
type dynamicPricingModelWindow struct {
	tokens           float64
	requests         float64
	pricedTokens     float64         // tokens from channels with a configured cost
	pricedRequests   float64         // requests from channels with a configured cost
	unpricedTokens   float64         // tokens from channels without a configured cost
	unpricedRequests float64         // requests from channels without a configured cost
	costUSD          float64         // sum of priced tokens×configured cost / 1e6, USD
	cheap            float64         // cheapest configured cost among the model's channels in window; 0 = unknown
	backup           float64         // second-cheapest configured cost; 0 = none
	channelCosts     map[int]float64 // configured cost per channel, deduped
}

// StartDynamicPricingTicker launches the background pricing ticker
// goroutine. It is safe to call once (sync.Once). The interval is re-read
// from the setting every iteration, so TickIntervalSeconds changes apply on
// the next sleep; whether a tick actually does work is decided inside
// runDynamicPricingTick, which returns immediately while the feature is
// disabled.
func StartDynamicPricingTicker() {
	dynamicPricingTickerOnce.Do(func() {
		gopool.Go(func() { RunDynamicPricingTicker(context.Background()) })
	})
}

func RunDynamicPricingTicker(ctx context.Context) {
	logger.LogInfo(ctx, "dynamic pricing ticker started")
	for {
		timer := time.NewTimer(dynamicPricingTickInterval())
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
		// One panic must stop only that tick iteration, never the ticker.
		func() {
			defer func() {
				if r := recover(); r != nil {
					common.SysError(fmt.Sprintf("dynamic pricing: tick panic recovered: %v", r))
				}
			}()
			runDynamicPricingTick()
		}()
	}
}

// dynamicPricingTickInterval returns the configured tick interval, falling
// back to one minute when the setting is not positive.
func dynamicPricingTickInterval() time.Duration {
	s := dynamic_pricing_setting.GetSetting()
	if s.TickIntervalSeconds <= 0 {
		return time.Minute
	}
	return time.Duration(s.TickIntervalSeconds) * time.Second
}

// runDynamicPricingTick executes one pricing tick: aggregates the consume
// logs over [now-window, now] and evolves the per-model state through
// pkg/dynamic_pricing.Tick. It is a no-op while the feature is disabled or
// the log database is unavailable.
func runDynamicPricingTick() {
	s := dynamic_pricing_setting.GetSetting()
	if !s.Enabled {
		return
	}
	if err := s.Validate(); err != nil {
		common.SysError(fmt.Sprintf("dynamic pricing: invalid configuration: %s", err.Error()))
		return
	}
	if s.WindowMinutes <= 0 {
		common.SysLog("dynamic pricing: window_minutes must be positive, tick skipped")
		return
	}
	if model.LOG_DB == nil {
		common.SysLog("dynamic pricing: log database unavailable, tick skipped")
		return
	}

	now := common.GetTimestamp()
	windowMinutes := float64(s.WindowMinutes)
	start := now - int64(s.WindowMinutes)*60

	rows, err := queryDynamicPricingWindow(start, now)
	if err != nil {
		common.SysError(fmt.Sprintf("dynamic pricing: window aggregation failed: %s", err.Error()))
		return
	}
	perModel := aggregateDynamicPricingWindow(rows)
	modelNames := make(map[string]struct{}, len(perModel))
	for modelName := range perModel {
		modelNames[modelName] = struct{}{}
	}
	// Tick models already in memory even when the current window has no rows.
	// This provides a zero-load sample so stale factors decay instead of
	// remaining elevated forever after traffic stops.
	for _, modelName := range dynamic_pricing.AllModels() {
		modelNames[modelName] = struct{}{}
	}
	orderedModels := make([]string, 0, len(modelNames))
	for modelName := range modelNames {
		orderedModels = append(orderedModels, modelName)
	}
	sort.Strings(orderedModels)
	for _, modelName := range orderedModels {
		mw := perModel[modelName]
		if mw == nil {
			mw = &dynamicPricingModelWindow{}
		}
		// Resolve per-model target overrides into the tick's setting snapshot.
		s.TargetTPM, s.TargetRPM, s.TargetCostRate = dynamic_pricing_setting.GetModelTargets(modelName)
		s.BasePriceUSDPerMillion = dynamic_pricing_setting.GetModelBasePrice(modelName)

		// Cold start: in-memory state, then Redis persistence, then fresh. The
		// demand premium still moves through the configured smoothing controls,
		// while Tick applies minimum and known-cost safety floors immediately.
		state, ok := dynamic_pricing.GetState(modelName)
		if !ok {
			if loaded, loadedOK := dynamic_pricing.LoadFromRedis(modelName); loadedOK {
				state = loaded
			} else {
				state = &dynamic_pricing.ModelState{Factor: s.MinFactor}
			}
		}
		dynamic_pricing.ClampState(state, s.MaxFactor)

		in := dynamic_pricing.TickInput{
			Model:                  modelName,
			WindowTokens:           mw.pricedTokens,
			WindowRequests:         mw.pricedRequests,
			WindowUpstreamCostUSD:  mw.costUSD,
			WindowMinutes:          windowMinutes,
			CheapCost:              mw.cheap,
			BackupCost:             mw.backup,
			WindowUnpricedTokens:   mw.unpricedTokens,
			WindowUnpricedRequests: mw.unpricedRequests,
			BasePriceUSDPerMillion: s.BasePriceUSDPerMillion,
			Now:                    now,
		}
		dynamic_pricing.Tick(state, in, s)
		dynamic_pricing.SetState(modelName, state)

		if state.LoadEMA > 1.0 {
			common.SysLog(fmt.Sprintf("dynamic pricing: model %s load EMA %.2f exceeds target; dynamic pricing only raises the price factor and cannot replace capacity control", modelName, state.LoadEMA))
		}
	}
}

// RunDynamicPricingTickNow refreshes the in-memory preview immediately after
// an administrator saves the feature settings. The regular background ticker
// remains the source of ongoing updates.
func RunDynamicPricingTickNow() {
	runDynamicPricingTick()
}

// queryDynamicPricingWindow aggregates consume (type=2) logs over
// [start, end] into per (model, channel) token/request totals. It uses the
// same LOG_DB handle as the rest of the log queries in package model.
func queryDynamicPricingWindow(startTimestamp, endTimestamp int64) ([]dynamicPricingWindowRow, error) {
	var rows []dynamicPricingWindowRow
	err := model.LOG_DB.Model(&model.Log{}).
		Select("model_name, channel_id, COALESCE(SUM(prompt_tokens), 0) + COALESCE(SUM(completion_tokens), 0) AS tokens, COUNT(*) AS requests").
		Where("type = ? AND created_at >= ? AND created_at <= ?", model.LogTypeConsume, startTimestamp, endTimestamp).
		Group("model_name, channel_id").
		Limit(dynamicPricingMaxWindowRows + 1).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) > dynamicPricingMaxWindowRows {
		return nil, fmt.Errorf("dynamic pricing window has more than %d model/channel rows", dynamicPricingMaxWindowRows)
	}
	return rows, err
}

// aggregateDynamicPricingWindow folds the per (model, channel) rows into
// per-model windows. Upstream cost uses the configured channel cost (USD per
// 1M tokens); channels without a configured cost are excluded from the cost
// and from cheap/backup route selection. cheap is the cheapest configured
// cost among the model's channels in the window, backup the second-cheapest
// (0 when fewer than two configured channels were seen).
func aggregateDynamicPricingWindow(rows []dynamicPricingWindowRow) map[string]*dynamicPricingModelWindow {
	perModel := map[string]*dynamicPricingModelWindow{}
	for _, row := range rows {
		mw := perModel[row.ModelName]
		if mw == nil {
			mw = &dynamicPricingModelWindow{channelCosts: map[int]float64{}}
			perModel[row.ModelName] = mw
		}
		mw.tokens += float64(row.Tokens)
		mw.requests += float64(row.Requests)

		cost, ok := dynamic_pricing_setting.GetChannelCost(row.ChannelId)
		if !ok || cost <= 0 {
			mw.unpricedTokens += float64(row.Tokens)
			mw.unpricedRequests += float64(row.Requests)
			continue // channel without configured cost: excluded from priced load/cost
		}
		mw.pricedTokens += float64(row.Tokens)
		mw.pricedRequests += float64(row.Requests)
		mw.costUSD += float64(row.Tokens) / 1e6 * cost
		if _, seen := mw.channelCosts[row.ChannelId]; !seen {
			mw.channelCosts[row.ChannelId] = cost
		}
	}
	for _, mw := range perModel {
		if len(mw.channelCosts) == 0 {
			continue
		}
		costs := make([]float64, 0, len(mw.channelCosts))
		for _, c := range mw.channelCosts {
			costs = append(costs, c)
		}
		sort.Float64s(costs)
		mw.cheap = costs[0]
		if len(costs) > 1 {
			mw.backup = costs[1]
		}
	}
	return perModel
}
