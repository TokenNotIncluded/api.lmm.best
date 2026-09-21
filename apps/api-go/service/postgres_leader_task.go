package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/leadership"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/bytedance/gopkg/util/gopool"
)

func postgresLeaderTaskDB(ctx context.Context, label string) (*sql.DB, error) {
	if ctx == nil {
		return nil, errors.New("leader task context is nil")
	}
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		return nil, fmt.Errorf("%s requires PostgreSQL advisory-lock leadership; current primary database is %s", label, common.MainDatabaseType())
	}
	if model.DB == nil {
		return nil, fmt.Errorf("%s requires an initialized primary database", label)
	}
	sqlDB, err := model.DB.DB()
	if err != nil {
		return nil, fmt.Errorf("open %s leadership pool: %w", label, err)
	}
	return sqlDB, nil
}

func runPostgresLeaderTaskWithDB(ctx context.Context, sqlDB *sql.DB, namespace, label string, run func(context.Context)) error {
	return leadership.Run(ctx, sqlDB, namespace, leadership.RunOptions{
		OnRetryable: func(err error) {
			logger.LogWarn(ctx, fmt.Sprintf("%s leadership retry: %v", label, err))
		},
	}, run)
}

// runPostgresLeaderTask runs synchronously so an application lifecycle can
// track cancellation and wait for the leadership loop before closing the DB.
func runPostgresLeaderTask(ctx context.Context, namespace, label string, run func(context.Context)) error {
	sqlDB, err := postgresLeaderTaskDB(ctx, label)
	if err != nil {
		return err
	}
	return runPostgresLeaderTaskWithDB(ctx, sqlDB, namespace, label, run)
}

// startPostgresLeaderTask preserves the detached source-compatible startup API.
func startPostgresLeaderTask(ctx context.Context, namespace, label string, run func(context.Context)) error {
	sqlDB, err := postgresLeaderTaskDB(ctx, label)
	if err != nil {
		return err
	}
	gopool.Go(func() {
		err := runPostgresLeaderTaskWithDB(ctx, sqlDB, namespace, label, run)
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.LogError(context.Background(), fmt.Sprintf("%s leadership stopped: %v", label, err))
		}
	})
	return nil
}
