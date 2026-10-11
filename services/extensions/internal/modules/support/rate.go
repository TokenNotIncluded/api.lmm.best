// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package support

import (
	"context"
	"errors"
	"fmt"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
)

// RateLimitWeb uses the support database's clock and transaction lock. A user's
// scope survives process restarts and cannot be reset by changing IP or worker.
// @web-rate is separate from conversation command/idempotency records.
func (s *Service) RateLimitWeb(ctx context.Context, token string, actor int64, scope string) error {
	maximum := map[string]int{"create": 20, "message": 60, "receipt": 180, "manage": 30}[scope]
	if maximum == 0 {
		return store.ErrInvalid
	}
	if err := s.webActor(ctx, token, actor); err != nil {
		return err
	}
	return s.repo.Within(ctx, fmt.Sprintf("@web-rate-user:%d", actor), func(tx Tx) error {
		clock, ok := tx.(interface{ RateLimitTime() (int64, error) })
		if !ok {
			return store.ErrUnavailable
		}
		now, err := clock.RateLimitTime()
		if err != nil {
			return err
		}
		var bucket struct {
			Recent []int64 `json:"recent"`
		}
		err = tx.Get("commands", "@web-rate:"+scope, &bucket)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if len(bucket.Recent) > maximum {
			return store.ErrUnavailable
		}
		recent := make([]int64, 0, maximum)
		for _, at := range bucket.Recent {
			if at > now-60000 {
				recent = append(recent, at)
			}
		}
		if len(recent) >= maximum {
			return store.ErrRateLimited
		}
		bucket.Recent = append(recent, now)
		return tx.Put("commands", "@web-rate:"+scope, bucket)
	})
}
