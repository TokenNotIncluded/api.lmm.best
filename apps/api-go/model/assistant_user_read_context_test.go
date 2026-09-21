// Copyright (C) 2026 LIghtJUNction
package model

import (
	"context"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

// Hold the only SQL connection: cancellation must interrupt acquisition as
// well as a query already executing. No goroutine or global DB swap is used
// to fake a timeout while database work continues in the background.
func TestAssistantUserReadContextCancelsWaitingForDatabase(t *testing.T) {
	db := setupTopUpAccessTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	connection, err := sqlDB.Conn(context.Background())
	require.NoError(t, err)
	defer connection.Close()

	for _, read := range []struct {
		name string
		run  func(context.Context) error
	}{
		{"list", func(ctx context.Context) error {
			_, _, err := GetAllUsersContext(ctx, &common.PageInfo{}, false)
			return err
		}},
		{"search", func(ctx context.Context) error {
			_, _, err := SearchUsersContext(ctx, "", "", nil, nil, false, 0, 10)
			return err
		}},
		{"profiles", func(ctx context.Context) error {
			return PopulateAssistantUserProfilesContext(ctx, []*User{{Id: 1001, Role: common.RoleCommonUser}}, 1, common.RoleRootUser)
		}},
		{"history counts", func(ctx context.Context) error {
			return PopulateAssistantConversationCountsContext(ctx, []*User{{Id: 1001, Role: common.RoleCommonUser}}, 1, common.RoleRootUser)
		}},
		{"topup totals", func(ctx context.Context) error { return PopulateUserTopupsContext(ctx, []*User{{Id: 1001}}) }},
		{"trust enrichment", func(ctx context.Context) error {
			return EnrichUsersTrustLevelsContext(ctx, []*User{{Id: 919191, Role: common.RoleCommonUser}})
		}},
	} {
		t.Run(read.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			require.ErrorIs(t, read.run(ctx), context.DeadlineExceeded)
		})
	}
}
