/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/

package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"golang.org/x/sync/singleflight"
)

type UserUsageRankingsResponse struct {
	Period                    string            `json:"period"`
	UpdatedAt                 int64             `json:"updated_at"`
	TotalTokens               int64             `json:"total_tokens"`
	TotalRequests             int64             `json:"total_requests"`
	ParticipantCount          int               `json:"participant_count"`
	AnonymousParticipantCount int               `json:"anonymous_participant_count"`
	Users                     []RankedUserUsage `json:"users"`
}

type RankedUserUsage struct {
	Rank        int     `json:"rank"`
	Name        string  `json:"name,omitempty"`
	Anonymous   bool    `json:"anonymous"`
	TotalTokens int64   `json:"total_tokens"`
	Requests    int64   `json:"requests"`
	Share       float64 `json:"share"`
}

type userUsageCandidate struct {
	userID    int
	name      string
	anonymous bool
	requests  int64
	tokens    int64
}

type userUsageRankingCacheItem struct {
	expiresAt time.Time
	revision  int64
	data      *UserUsageRankingsResponse
}

type userUsageRankingFlightResult struct {
	revision int64
	data     *UserUsageRankingsResponse
}

const (
	userUsageRankingStabilityAttempts = 3
	userUsageRankingBuildTimeout      = 30 * time.Second
)

var (
	userUsageRankingCacheMu sync.RWMutex
	userUsageRankingCache   = map[string]userUsageRankingCacheItem{}
	userUsageRankingFlights singleflight.Group
)

func GetUserUsageRankingsSnapshot(ctx context.Context, period string) (*UserUsageRankingsResponse, error) {
	config, err := rankingConfig(period)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, fmt.Errorf("get user usage rankings: nil context")
	}

	for attempt := 0; attempt < userUsageRankingStabilityAttempts; attempt++ {
		resultChannel := userUsageRankingFlights.DoChan(config.id, func() (any, error) {
			buildCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), userUsageRankingBuildTimeout)
			defer cancel()
			return getUserUsageRankingsSnapshot(buildCtx, config)
		})
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result := <-resultChannel:
			if result.Err != nil {
				return nil, result.Err
			}
			flightResult, ok := result.Val.(userUsageRankingFlightResult)
			if !ok || flightResult.data == nil {
				return nil, fmt.Errorf("unexpected user rankings cache result")
			}
			current, err := userUsageRankingFlightIsCurrent(ctx, flightResult)
			if err != nil {
				return nil, err
			}
			if current {
				return flightResult.data, nil
			}
		}
	}
	return nil, fmt.Errorf("user ranking visibility changed while returning the snapshot")
}

func userUsageRankingFlightIsCurrent(ctx context.Context, result userUsageRankingFlightResult) (bool, error) {
	currentRevision, err := model.CurrentUserRankingRevision(ctx)
	if err != nil {
		return false, fmt.Errorf("validate user ranking revision: %w", err)
	}
	return currentRevision == result.revision, nil
}

func getUserUsageRankingsSnapshot(ctx context.Context, config rankingPeriodConfig) (userUsageRankingFlightResult, error) {
	for attempt := 0; attempt < userUsageRankingStabilityAttempts; attempt++ {
		revision, err := model.CurrentUserRankingRevision(ctx)
		if err != nil {
			return userUsageRankingFlightResult{}, fmt.Errorf("read user ranking revision: %w", err)
		}

		now := time.Now()
		userUsageRankingCacheMu.RLock()
		item, ok := userUsageRankingCache[config.id]
		userUsageRankingCacheMu.RUnlock()
		if ok && now.Before(item.expiresAt) && item.revision == revision {
			return userUsageRankingFlightResult{revision: revision, data: item.data}, nil
		}

		data, err := buildUserUsageRankingsSnapshot(ctx, config, now)
		if err != nil {
			return userUsageRankingFlightResult{}, fmt.Errorf("build user usage rankings: %w", err)
		}
		stableRevision, err := model.CurrentUserRankingRevision(ctx)
		if err != nil {
			return userUsageRankingFlightResult{}, fmt.Errorf("recheck user ranking revision: %w", err)
		}
		if stableRevision != revision {
			continue
		}

		userUsageRankingCacheMu.Lock()
		userUsageRankingCache[config.id] = userUsageRankingCacheItem{
			expiresAt: time.Now().Add(rankingCacheTTL),
			revision:  stableRevision,
			data:      data,
		}
		userUsageRankingCacheMu.Unlock()
		return userUsageRankingFlightResult{revision: stableRevision, data: data}, nil
	}
	return userUsageRankingFlightResult{}, fmt.Errorf("user ranking visibility changed while building the snapshot")
}

func buildUserUsageRankingsSnapshot(ctx context.Context, config rankingPeriodConfig, now time.Time) (*UserUsageRankingsResponse, error) {
	startTime, endTime := rankingTimeRange(config, now)
	candidates := make([]userUsageCandidate, 0, rankingLeaderboardLimit)
	var totalTokens int64
	var totalRequests int64
	participantCount := 0
	anonymousParticipantCount := 0

	err := model.IterateUserRankingRows(ctx, startTime, endTime, func(row model.UserRankingRow) error {
		if row.Status != common.UserStatusEnabled {
			return nil
		}

		visibility := dto.NormalizeUsageLeaderboardVisibility(
			(&model.User{Setting: row.Setting}).GetSetting().UsageLeaderboardVisibility,
		)
		if visibility == dto.UsageLeaderboardVisibilityHidden {
			return nil
		}

		participantCount++
		totalTokens += row.TotalTokens
		totalRequests += row.Requests
		candidate := userUsageCandidate{
			userID:   row.UserID,
			requests: row.Requests,
			tokens:   row.TotalTokens,
		}

		if visibility == dto.UsageLeaderboardVisibilityAnonymous {
			anonymousParticipantCount++
			// Anonymous visibility hides the user's name, not their independent
			// ranking row. Keep each user's totals separate so one participant
			// cannot make all anonymous usage appear as a single account.
			candidate.anonymous = true
		} else {
			name := strings.TrimSpace(row.DisplayName)
			if name == "" {
				name = strings.TrimSpace(row.Username)
			}
			if name == "" {
				return nil
			}
			candidate.name = name
		}
		retainUsageCandidate(&candidates, candidate)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.SliceStable(candidates, func(i, j int) bool { return userUsageCandidateBetter(candidates[i], candidates[j]) })

	rows := make([]RankedUserUsage, 0, minInt(len(candidates), rankingLeaderboardLimit))
	for index, candidate := range candidates {
		if index >= rankingLeaderboardLimit {
			break
		}
		share := 0.0
		if totalTokens > 0 {
			share = float64(candidate.tokens) / float64(totalTokens)
		}
		rows = append(rows, RankedUserUsage{
			Rank:        index + 1,
			Name:        candidate.name,
			Anonymous:   candidate.anonymous,
			TotalTokens: candidate.tokens,
			Requests:    candidate.requests,
			Share:       share,
		})
	}

	return &UserUsageRankingsResponse{
		Period:                    config.id,
		UpdatedAt:                 now.Unix(),
		TotalTokens:               totalTokens,
		TotalRequests:             totalRequests,
		ParticipantCount:          participantCount,
		AnonymousParticipantCount: anonymousParticipantCount,
		Users:                     rows,
	}, nil
}

func userUsageCandidateBetter(left, right userUsageCandidate) bool {
	if left.tokens != right.tokens {
		return left.tokens > right.tokens
	}
	if left.requests != right.requests {
		return left.requests > right.requests
	}
	if left.anonymous != right.anonymous {
		return !left.anonymous
	}
	if left.name != right.name {
		return left.name < right.name
	}
	return left.userID < right.userID
}

// retainUsageCandidate keeps only the public leaderboard window. Its linear
// scan is intentional: rankingLeaderboardLimit is small and fixed, while the
// source may contain millions of users.
func retainUsageCandidate(candidates *[]userUsageCandidate, candidate userUsageCandidate) {
	if len(*candidates) < rankingLeaderboardLimit {
		*candidates = append(*candidates, candidate)
		return
	}
	worst := 0
	for index := 1; index < len(*candidates); index++ {
		if userUsageCandidateBetter((*candidates)[worst], (*candidates)[index]) {
			worst = index
		}
	}
	if userUsageCandidateBetter(candidate, (*candidates)[worst]) {
		(*candidates)[worst] = candidate
	}
}
