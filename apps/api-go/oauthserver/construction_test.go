package oauthserver

import (
	"context"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPostgresIsolationDoesNotDependOnSessionDefault(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		if db.Dialector.Name() != "postgres" {
			return
		}
		require.NoError(t, db.Connection(func(connection *gorm.DB) error {
			if err := connection.Exec("SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL SERIALIZABLE").Error; err != nil {
				return err
			}
			defer func() {
				require.NoError(t, connection.Exec("SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL READ COMMITTED").Error)
			}()
			s, _, _ := testServer(t, connection)
			return s.transact(context.Background(), func(tx *gorm.DB) (*ProtocolError, error) {
				var isolation string
				err := tx.Raw("SHOW transaction_isolation").Scan(&isolation).Error
				require.Equal(t, "read committed", isolation)
				return nil, err
			})
		}))
	})
}

func TestServerRejectsOuterTransactionsAndDryRuns(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		tx := db.Begin()
		require.NoError(t, tx.Error)
		_, err := New(tx, testConfig(), &testPolicy{})
		require.Error(t, err)
		require.NoError(t, tx.Rollback().Error)
		_, err = New(db.Session(&gorm.Session{DryRun: true}), testConfig(), &testPolicy{})
		require.Error(t, err)
		prepared := db.Session(&gorm.Session{PrepareStmt: true}).Begin()
		require.NoError(t, prepared.Error)
		_, err = New(prepared, testConfig(), &testPolicy{})
		require.Error(t, err)
		require.NoError(t, prepared.Rollback().Error)
	})
}

func TestMigrationIsOptInAndIdempotent(t *testing.T) {
	require.Error(t, model.MigrateOAuthServer(nil))
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		require.NoError(t, db.Migrator().DropTable(&model.OAuthServerToken{}, &model.OAuthServerCode{}, &model.OAuthServerGrant{}, &model.OAuthServerAuthorization{}))
		s, _, _ := testServer(t, db)
		require.False(t, db.Migrator().HasTable(&model.OAuthServerAuthorization{}))
		_, err := s.BeginAuthorization(context.Background(), authorizationValues().Encode(), testBrowser)
		expectProtocol(t, err, "server_error")
		require.NoError(t, model.MigrateOAuthServer(db))
		tokens, _ := issueTokens(t, s)
		require.NoError(t, model.MigrateOAuthServer(db))
		verify(t, s, tokens.AccessToken)
	})
}
