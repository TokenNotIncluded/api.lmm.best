package service

import (
	"context"
	"fmt"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
)

const authArtifactCleanupInterval = time.Hour

const (
	secureCardScrubBatchSize  = 200
	secureCardScrubMaxBatches = 20
)

// StartAuthArtifactCleanup removes expired dashboard Sessions and old
// one-time authentication flows. Only the master instance performs cleanup.
func StartAuthArtifactCleanup() {
	if common.IsMasterNode {
		go RunAuthArtifactCleanup(context.Background())
	}
}

func RunAuthArtifactCleanup(ctx context.Context) {
	if !common.IsMasterNode {
		return
	}
	cleanupAuthArtifacts()
	ticker := time.NewTicker(authArtifactCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanupAuthArtifacts()
		}
	}
}

func cleanupAuthArtifacts() {
	now := time.Now()
	count, err := model.CountUserSessionsCreatedSince(0, now.Add(-time.Hour).Unix())
	if err != nil {
		common.SysError("failed to count hourly user session issuance: " + err.Error())
	} else if count > int64(common.UserSessionHourlyAlertThreshold) {
		common.SysError(fmt.Sprintf(
			"hourly user session issuance exceeded alert threshold: count=%d threshold=%d window_seconds=%d",
			count,
			common.UserSessionHourlyAlertThreshold,
			int64(time.Hour/time.Second),
		))
	}
	if err := model.DeleteExpiredUserSessions(now.Unix()); err != nil {
		common.SysError("failed to delete expired user sessions: " + err.Error())
	}
	if err := model.DeleteOldRevokedUserSessions(now.Unix()); err != nil {
		common.SysError("failed to delete old revoked user sessions: " + err.Error())
	}
	if err := model.DeleteExpiredAuthFlows(now); err != nil {
		common.SysError("failed to delete expired authentication flows: " + err.Error())
	}
	// A minimal test database or a node still completing its first migration
	// may not have assistant storage yet. The next hourly pass will pick it up.
	if model.DB.Migrator().HasTable(&model.AssistantSecureCard{}) {
		for batch := 0; batch < secureCardScrubMaxBatches; batch++ {
			count, err := model.ScrubExpiredAssistantSecureCards(context.Background(), now.Unix(), secureCardScrubBatchSize)
			if err != nil {
				common.SysError("failed to scrub expired assistant secure cards: " + err.Error())
				break
			}
			if count < secureCardScrubBatchSize {
				break
			}
		}
	}
}
