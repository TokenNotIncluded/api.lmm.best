package model

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type migrationUnixEndpoint struct {
	address    driver.Value
	port       driver.Value
	database   string
	oid        int64
	systemID   string
	controlErr error
}

type migrationUnixQuery struct {
	connection int
	kind       string
}

type migrationUnixDriverState struct {
	mu          sync.Mutex
	endpoints   []migrationUnixEndpoint
	connections int
	queries     []migrationUnixQuery
	lockOwner   int
	fenceOwner  int
	fenceRecord bool
	failRelease bool
}

type migrationUnixDriver struct{ state *migrationUnixDriverState }
type migrationUnixConn struct {
	state *migrationUnixDriverState
	id    int
}
type migrationUnixRows struct {
	values []driver.Value
	read   bool
}

var migrationUnixDriverSequence atomic.Uint64

func migrationUnixEndpointFor(systemID string) migrationUnixEndpoint {
	return migrationUnixEndpoint{database: "migration_lock_test", oid: 16384, systemID: systemID}
}

func openMigrationUnixTestDB(t *testing.T, endpoints ...migrationUnixEndpoint) (*sql.DB, *gorm.DB, *migrationUnixDriverState) {
	t.Helper()
	state := &migrationUnixDriverState{endpoints: endpoints}
	name := fmt.Sprintf("migration-unix-test-%d", migrationUnixDriverSequence.Add(1))
	sql.Register(name, &migrationUnixDriver{state: state})
	db, err := sql.Open(name, "")
	require.NoError(t, err)
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db, &gorm.DB{Config: &gorm.Config{ConnPool: db}}, state
}

func (fixture *migrationUnixDriver) Open(string) (driver.Conn, error) {
	fixture.state.mu.Lock()
	defer fixture.state.mu.Unlock()
	fixture.state.connections++
	return &migrationUnixConn{state: fixture.state, id: fixture.state.connections}, nil
}

func (*migrationUnixConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("migration identity fixture does not support prepared statements")
}
func (*migrationUnixConn) Begin() (driver.Tx, error) {
	return nil, errors.New("migration identity fixture does not support transactions")
}
func (conn *migrationUnixConn) Close() error {
	conn.state.mu.Lock()
	defer conn.state.mu.Unlock()
	if conn.state.lockOwner == conn.id {
		conn.state.lockOwner = 0
	}
	if conn.state.fenceOwner == conn.id {
		conn.state.fenceOwner = 0
	}
	return nil
}

func (conn *migrationUnixConn) QueryContext(_ context.Context, query string, arguments []driver.NamedValue) (driver.Rows, error) {
	conn.state.mu.Lock()
	defer conn.state.mu.Unlock()
	index := conn.id - 1
	if index >= len(conn.state.endpoints) {
		index = len(conn.state.endpoints) - 1
	}
	endpoint := conn.state.endpoints[index]
	kind := ""
	var values []driver.Value
	switch {
	case strings.Contains(query, "inet_server_addr"):
		kind = "identity"
		values = []driver.Value{endpoint.address, endpoint.port, endpoint.database, endpoint.oid}
	case strings.Contains(query, "pg_control_system"):
		kind = "control"
		conn.state.queries = append(conn.state.queries, migrationUnixQuery{conn.id, kind})
		if endpoint.controlErr != nil {
			return nil, endpoint.controlErr
		}
		return &migrationUnixRows{values: []driver.Value{endpoint.systemID}}, nil
	case strings.Contains(query, "pg_try_advisory_lock"):
		kind = "acquire"
		if len(arguments) != 1 {
			return nil, errors.New("migration fixture received an incorrect advisory-lock key")
		}
		owner := &conn.state.lockOwner
		if arguments[0].Value == deploymentfence.AdvisoryKey {
			kind, owner = "fence-acquire", &conn.state.fenceOwner
		} else if arguments[0].Value != MigrationAdvisoryLockKey {
			return nil, errors.New("migration fixture received an incorrect advisory-lock key")
		}
		locked := *owner == 0
		if locked {
			*owner = conn.id
		}
		values = []driver.Value{locked}
	case strings.Contains(query, "pg_advisory_unlock"):
		kind = "release"
		if len(arguments) != 1 {
			return nil, errors.New("migration fixture received an incorrect release key")
		}
		owner := &conn.state.lockOwner
		if arguments[0].Value == deploymentfence.AdvisoryKey {
			kind, owner = "fence-release", &conn.state.fenceOwner
		} else if arguments[0].Value != MigrationAdvisoryLockKey {
			return nil, errors.New("migration fixture received an incorrect release key")
		}
		if conn.state.failRelease {
			conn.state.queries = append(conn.state.queries, migrationUnixQuery{conn.id, kind})
			return nil, errors.New("fixture cannot confirm advisory unlock")
		}
		unlocked := *owner == conn.id
		if unlocked {
			*owner = 0
		}
		values = []driver.Value{unlocked}
	case strings.Contains(query, deploymentfence.PostgreSQLPresencePredicate):
		kind, values = "fence-presence", []driver.Value{conn.state.fenceRecord}
	default:
		return nil, fmt.Errorf("unexpected migration identity fixture query %q", query)
	}
	conn.state.queries = append(conn.state.queries, migrationUnixQuery{conn.id, kind})
	return &migrationUnixRows{values: values}, nil
}

func (rows *migrationUnixRows) Columns() []string {
	columns := make([]string, len(rows.values))
	for index := range columns {
		columns[index] = fmt.Sprintf("column_%d", index)
	}
	return columns
}
func (*migrationUnixRows) Close() error { return nil }
func (rows *migrationUnixRows) Next(values []driver.Value) error {
	if rows.read {
		return io.EOF
	}
	rows.read = true
	copy(values, rows.values)
	return nil
}

func TestPostgresMigrationUnixNullIdentityUsesPinnedClusterReaders(t *testing.T) {
	sqlDB, db, state := openMigrationUnixTestDB(t, migrationUnixEndpointFor("123456789"))
	lock, err := openPostgresMigrationLock(db)
	require.NoError(t, err)
	require.Equal(t, postgresDatabaseIdentity{
		DatabaseName: "migration_lock_test", DatabaseOID: 16384,
		UnixSocket: true, SystemIdentifier: "123456789",
	}, lock.Identity())
	require.Equal(t, []migrationUnixQuery{{1, "identity"}, {1, "control"}, {2, "identity"}, {2, "control"}}, state.queries)
	require.Equal(t, 1, sqlDB.Stats().InUse, "pool identity reader must return its pinned connection before acquisition")
	require.NoError(t, lock.Acquire())
	require.NoError(t, lock.Release())
	require.Equal(t, []migrationUnixQuery{{1, "acquire"}, {1, "release"}}, state.queries[4:])
	require.Zero(t, sqlDB.Stats().InUse)
}

func TestPostgresMigrationUnixMixedNullIdentityFailsBeforeClusterRead(t *testing.T) {
	for _, test := range []struct {
		name    string
		address driver.Value
		port    driver.Value
	}{
		{"null_address_only", nil, int64(5432)},
		{"null_port_only", "127.0.0.1", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			endpoint := migrationUnixEndpointFor("123456789")
			endpoint.address, endpoint.port = test.address, test.port
			sqlDB, db, state := openMigrationUnixTestDB(t, endpoint)
			lock, err := openPostgresMigrationLock(db)
			require.Nil(t, lock)
			require.ErrorContains(t, err, "inconsistent NULL state")
			require.Equal(t, []migrationUnixQuery{{1, "identity"}}, state.queries)
			require.Zero(t, sqlDB.Stats().InUse)
		})
	}
}

func TestPostgresMigrationUnixSystemIdentifierMustBeCanonicalPositiveUint64(t *testing.T) {
	for _, systemID := range []string{"", "0", "00", "01", "+1", "-1", " 1", "1 ", "1.0", "18446744073709551616"} {
		t.Run("reject_"+systemID, func(t *testing.T) {
			sqlDB, db, state := openMigrationUnixTestDB(t, migrationUnixEndpointFor(systemID))
			lock, err := openPostgresMigrationLock(db)
			require.Nil(t, lock)
			require.ErrorContains(t, err, "cluster identity is incomplete or ambiguous")
			require.Equal(t, []migrationUnixQuery{{1, "identity"}, {1, "control"}}, state.queries)
			require.Zero(t, sqlDB.Stats().InUse)
		})
	}
	for _, systemID := range []string{"1", "18446744073709551615"} {
		t.Run("accept_"+systemID, func(t *testing.T) {
			_, db, _ := openMigrationUnixTestDB(t, migrationUnixEndpointFor(systemID))
			lock, err := openPostgresMigrationLock(db)
			require.NoError(t, err)
			require.Equal(t, systemID, lock.Identity().SystemIdentifier)
			require.NoError(t, lock.Release())
		})
	}
}

func TestPostgresMigrationUnixPoolMismatchFailsBeforeAdvisoryLock(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*migrationUnixEndpoint)
	}{
		{"different_cluster_same_name_and_oid", func(endpoint *migrationUnixEndpoint) { endpoint.systemID = "987654321" }},
		{"different_database_same_oid", func(endpoint *migrationUnixEndpoint) { endpoint.database = "another_database" }},
		{"different_oid_same_database", func(endpoint *migrationUnixEndpoint) { endpoint.oid++ }},
		{"different_database_and_oid", func(endpoint *migrationUnixEndpoint) { endpoint.database, endpoint.oid = "another_database", 16385 }},
		{"pool_changes_to_tcp", func(endpoint *migrationUnixEndpoint) { endpoint.address, endpoint.port = "127.0.0.1", int64(5432) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			dedicated := migrationUnixEndpointFor("123456789")
			pool := dedicated
			test.mutate(&pool)
			sqlDB, db, state := openMigrationUnixTestDB(t, dedicated, pool)
			lock, err := openPostgresMigrationLock(db)
			require.Nil(t, lock)
			require.ErrorContains(t, err, "different physical databases")
			for _, query := range state.queries {
				require.NotEqual(t, "acquire", query.kind)
			}
			require.Zero(t, sqlDB.Stats().InUse)
		})
	}
	t.Run("pool_cluster_permission_failure", func(t *testing.T) {
		dedicated := migrationUnixEndpointFor("123456789")
		pool := dedicated
		pool.controlErr = errors.New("permission denied for function pg_control_system")
		sqlDB, db, state := openMigrationUnixTestDB(t, dedicated, pool)
		lock, err := openPostgresMigrationLock(db)
		require.Nil(t, lock)
		require.ErrorContains(t, err, "permission denied")
		require.Equal(t, []migrationUnixQuery{{1, "identity"}, {1, "control"}, {2, "identity"}, {2, "control"}}, state.queries)
		require.Zero(t, sqlDB.Stats().InUse)
	})
}

func TestPostgresMigrationTCPDoesNotReadControlOrPoolIdentity(t *testing.T) {
	endpoint := migrationUnixEndpointFor("")
	endpoint.address, endpoint.port = "127.0.0.1", int64(5432)
	endpoint.controlErr = errors.New("permission denied for function pg_control_system")
	sqlDB, db, state := openMigrationUnixTestDB(t, endpoint)
	lock, err := openPostgresMigrationLock(db)
	require.NoError(t, err)
	require.False(t, lock.Identity().UnixSocket)
	require.Empty(t, lock.Identity().SystemIdentifier)
	require.Equal(t, []migrationUnixQuery{{1, "identity"}}, state.queries)
	require.Equal(t, 1, state.connections)
	require.NoError(t, lock.Acquire())
	require.NoError(t, lock.Release())
	require.Zero(t, sqlDB.Stats().InUse)
}

func TestPostgresMigrationUnixDeduplicatesOnlySameClusterAndDatabase(t *testing.T) {
	primary, sameDatabase, otherCluster := new(gorm.DB), new(gorm.DB), new(gorm.DB)
	identity := postgresDatabaseIdentity{UnixSocket: true, SystemIdentifier: "123456789", DatabaseName: "same_database", DatabaseOID: 16384}
	other := identity
	other.SystemIdentifier = "987654321"
	registry := newFakeMigrationLockRegistry(map[*gorm.DB]postgresDatabaseIdentity{
		primary: identity, sameDatabase: identity, otherCluster: other,
	})
	session := newStartupMigrationSession(DBMigrationModeApply)
	session.lockFactory = registry.factory
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	require.NoError(t, session.acquirePostgresLock(primary))
	require.NoError(t, session.acquirePostgresLock(sameDatabase))
	require.Len(t, session.locks, 1)
	require.Equal(t, 1, registry.acquireCounts[identity])
	require.NoError(t, session.acquirePostgresLock(otherCluster))
	require.Len(t, session.locks, 2, "two physical clusters may have identical database names and OIDs")
	require.Equal(t, 1, registry.acquireCounts[other])
	require.NoError(t, session.Close())
	require.Empty(t, registry.held)
}

// These opt-in tests only read metadata and acquire/release advisory locks on
// separately created test clusters. They never migrate schemas or business data.
func migrationUnixRealFixtureDB(t *testing.T, envName string, tcp bool) *gorm.DB {
	t.Helper()
	if os.Getenv("LMM_MIGRATION_UNIX_TEST_ONLY") != "1" {
		t.Skip("set LMM_MIGRATION_UNIX_TEST_ONLY=1 and dedicated temporary cluster DSNs to run real PostgreSQL migration-lock tests")
	}
	dsn := os.Getenv(envName)
	if dsn == "" {
		t.Skip(envName + " is not configured")
	}
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	require.Contains(t, []string{"postgres", "postgresql"}, parsed.Scheme)
	require.Equal(t, "/unix_lock_fixture", parsed.Path, "real lock tests require a dedicated test database")
	if tcp {
		require.Equal(t, "127.0.0.1", parsed.Hostname())
		require.NotEmpty(t, parsed.Port())
	} else {
		host := parsed.Query().Get("host")
		require.True(t, strings.HasPrefix(host, "/"))
		require.Contains(t, host, "api-unix-lock-test-", "socket must belong to a dedicated temporary test fixture")
		require.Empty(t, parsed.Host)
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	return db
}

func TestPostgresMigrationUnixRealClustersAcquireDeduplicateAndRelease(t *testing.T) {
	first := migrationUnixRealFixtureDB(t, "LMM_MIGRATION_UNIX_TEST_DSN_A", false)
	firstAgain := migrationUnixRealFixtureDB(t, "LMM_MIGRATION_UNIX_TEST_DSN_A", false)
	second := migrationUnixRealFixtureDB(t, "LMM_MIGRATION_UNIX_TEST_DSN_B", false)
	session := newStartupMigrationSession(DBMigrationModeApply)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	require.NoError(t, session.acquirePostgresLock(first), "actual Unix NULL address/port must reach successful lock acquisition")
	firstIdentity := session.locks[0].Identity()
	require.True(t, firstIdentity.UnixSocket)
	require.NoError(t, validatePostgresDatabaseIdentity(firstIdentity))
	require.NoError(t, session.acquirePostgresLock(firstAgain))
	require.Len(t, session.locks, 1)
	contender, err := openPostgresMigrationLock(firstAgain)
	require.NoError(t, err)
	require.ErrorContains(t, contender.Acquire(), "held by another startup")
	require.NoError(t, contender.Release())
	require.NoError(t, session.acquirePostgresLock(second))
	require.Len(t, session.locks, 2)
	secondIdentity := session.locks[1].Identity()
	require.NotEqual(t, firstIdentity.SystemIdentifier, secondIdentity.SystemIdentifier)
	require.Equal(t, firstIdentity.DatabaseName, secondIdentity.DatabaseName)
	require.Equal(t, firstIdentity.DatabaseOID, secondIdentity.DatabaseOID, "fresh clusters intentionally reuse the same database OID")
	t.Logf("temporary PostgreSQL Unix clusters have distinct system identifiers %s/%s and equal database %s/OID %d", firstIdentity.SystemIdentifier, secondIdentity.SystemIdentifier, firstIdentity.DatabaseName, firstIdentity.DatabaseOID)
	require.NoError(t, session.Close())
	for _, db := range []*gorm.DB{first, second} {
		lock, err := openPostgresMigrationLock(db)
		require.NoError(t, err)
		require.NoError(t, lock.Acquire(), "session.Close must release each physical cluster's advisory lock")
		require.NoError(t, lock.Release())
	}
}

func TestPostgresMigrationRestrictedRealRoleTCPWorksAndUnixFailsClosed(t *testing.T) {
	tcpDB := migrationUnixRealFixtureDB(t, "LMM_MIGRATION_UNIX_TEST_DSN_RESTRICTED_TCP", true)
	var allowed bool
	require.NoError(t, tcpDB.Raw("SELECT pg_catalog.has_function_privilege(CURRENT_USER, 'pg_catalog.pg_control_system()', 'EXECUTE')").Scan(&allowed).Error)
	require.False(t, allowed, "fixture role must actually lack pg_control_system execution permission")
	lock, err := openPostgresMigrationLock(tcpDB)
	require.NoError(t, err, "existing TCP callers must not acquire a new pg_control_system privilege requirement")
	require.False(t, lock.Identity().UnixSocket)
	require.Empty(t, lock.Identity().SystemIdentifier)
	require.NoError(t, lock.Acquire())
	require.NoError(t, lock.Release())
	unixDB := migrationUnixRealFixtureDB(t, "LMM_MIGRATION_UNIX_TEST_DSN_RESTRICTED_UNIX", false)
	lock, err = openPostgresMigrationLock(unixDB)
	require.Nil(t, lock)
	require.ErrorContains(t, err, "permission denied for function pg_control_system")
	sqlDB, err := unixDB.DB()
	require.NoError(t, err)
	require.Zero(t, sqlDB.Stats().InUse)
}
