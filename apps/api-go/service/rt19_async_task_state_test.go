//go:build rt19

package service_test

// These tests are opt-in because some assertions describe unresolved RT-19
// invariants. They use the real model/service code and Kling adapter, not a
// replacement billing implementation. See docs/audits/rt19-async-task-state.md.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/task/kling"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type rt19Env struct {
	t       *testing.T
	db      *gorm.DB
	mu      sync.Mutex
	calls   []string
	allowed map[string]bool
}

type rt19Transport struct {
	env  *rt19Env
	base http.RoundTripper
}

func (r rt19Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.env.mu.Lock()
	allowed := r.env.allowed[req.URL.Host]
	r.env.calls = append(r.env.calls, req.Method+" "+req.URL.String())
	r.env.mu.Unlock()
	ip := net.ParseIP(req.URL.Hostname())
	if !allowed || req.URL.Scheme != "http" || ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("RT19 blocked non-fixture destination: %s", req.URL.Host)
	}
	return r.base.RoundTrip(req)
}

// Each test gets a file-backed database. No DSN or Redis URL comes from the
// environment. Database globals prohibit t.Parallel; concurrency is inside
// individual scenarios. This SQLite suite does NOT prove PostgreSQL row locks.
func rt19Setup(t *testing.T) *rt19Env {
	t.Helper()
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(key, "")
	}
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "rt19.sqlite")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.User{}, &model.Token{}, &model.Log{}, &model.Channel{}, &model.UserSubscription{}))
	oldDB, oldLog := model.DB, model.LOG_DB
	oldRedis, oldMemory, oldBatch, oldLogging := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled
	oldExport := common.DataExportEnabled
	common.DataExportEnabled = false
	oldFactory := service.GetTaskAdaptorFunc
	oldConcurrency, oldLimit, oldTimeout := constant.TaskPollingConcurrency, constant.TaskQueryLimit, constant.TaskTimeoutMinutes
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = false, false, false, true
	constant.TaskPollingConcurrency, constant.TaskQueryLimit, constant.TaskTimeoutMinutes = 1, 100, 0
	service.GetTaskAdaptorFunc = func(p constant.TaskPlatform) service.TaskPollingAdaptor {
		if p == constant.TaskPlatform("kling") {
			return &kling.TaskAdaptor{}
		}
		return nil
	}
	if service.GetHttpClient() == nil {
		service.InitHttpClient()
	}
	client, err := service.GetHttpClientWithProxy("")
	require.NoError(t, err)
	savedClient := *client
	e := &rt19Env{t: t, db: db, allowed: map[string]bool{}}
	// Use a new transport without an environment proxy. Preserve real HTTP,
	// adapter signing, provider response parsing and all billing operations.
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext}
	client.Transport = rt19Transport{env: e, base: transport}
	client.Timeout = 5 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("RT19 forbids redirects") }
	t.Cleanup(func() {
		e.mu.Lock()
		calls := append([]string(nil), e.calls...)
		e.mu.Unlock()
		for _, call := range calls {
			t.Log("upstream:", call)
		}
		*client = savedClient
		transport.CloseIdleConnections()
		common.DataExportEnabled = oldExport
		service.GetTaskAdaptorFunc = oldFactory
		constant.TaskPollingConcurrency, constant.TaskQueryLimit, constant.TaskTimeoutMinutes = oldConcurrency, oldLimit, oldTimeout
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = oldRedis, oldMemory, oldBatch, oldLogging
		model.DB, model.LOG_DB = oldDB, oldLog
		assert.NoError(t, sqlDB.Close())
	})
	return e
}

func (e *rt19Env) server(handler http.HandlerFunc) *httptest.Server {
	s := httptest.NewServer(handler)
	e.mu.Lock()
	e.allowed[strings.TrimPrefix(s.URL, "http://")] = true
	e.mu.Unlock()
	e.t.Cleanup(s.Close)
	return s
}

func rt19Reply(w http.ResponseWriter, id, status, result string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
		"task_id": id, "task_status": status, "task_result": map[string]any{"videos": []map[string]any{{"id": "fixture-video", "url": result, "duration": "5"}}},
	}})
}

// This seed represents a durably accepted task with 100 quota already charged.
// It is NOT evidence that the submit/reservation path has been tested.
func (e *rt19Env) seed(id int, upstream, baseURL string) *model.Task {
	t := e.t
	require.NoError(t, e.db.Create(&model.User{Id: id, Username: fmt.Sprintf("rt19_%d", id), Quota: 900, UsedQuota: 100, RequestCount: 1, Status: common.UserStatusEnabled}).Error)
	require.NoError(t, e.db.Create(&model.Token{Id: id, UserId: id, Key: fmt.Sprintf("rt19-token-%d", id), Name: "fixture", RemainQuota: 900, UsedQuota: 100, Status: common.TokenStatusEnabled}).Error)
	ch := &model.Channel{Id: id, Name: fmt.Sprintf("rt19-%d", id), Type: constant.ChannelTypeKling, Key: "fixture-access|fixture-secret", BaseURL: &baseURL, UsedQuota: 100, Status: common.ChannelStatusEnabled}
	require.NoError(t, e.db.Create(ch).Error)
	now := time.Now().Unix()
	task := &model.Task{TaskID: fmt.Sprintf("task_rt19_%d", id), Platform: constant.TaskPlatform("kling"), UserId: id, ChannelId: id, Action: constant.TaskActionTextGenerate,
		Quota: 100, Status: model.TaskStatusInProgress, Progress: "30%", CreatedAt: now, UpdatedAt: now, SubmitTime: now, Group: "default", Data: json.RawMessage(`{}`),
		Properties:  model.Properties{OriginModelName: "kling-v1"},
		PrivateData: model.TaskPrivateData{UpstreamTaskID: upstream, BillingSource: "wallet", TokenId: id, BillingContext: &model.TaskBillingContext{OriginModelName: "kling-v1", PerCallBilling: true}}}
	require.NoError(t, e.db.Create(task).Error)
	return task
}

func (e *rt19Env) load(task *model.Task) *model.Task {
	e.t.Helper()
	var row model.Task
	require.NoError(e.t, e.db.First(&row, task.ID).Error)
	return &row
}
func (e *rt19Env) poll() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	service.RunTaskPollingOnce(ctx, nil)
}
func (e *rt19Env) accounting(task *model.Task, wallet, quota, used, tokenRemain, tokenUsed int) {
	t := e.t
	t.Helper()
	var u model.User
	var token model.Token
	var ch model.Channel
	var n int64
	require.NoError(t, e.db.First(&u, task.UserId).Error)
	require.NoError(t, e.db.First(&token, task.PrivateData.TokenId).Error)
	require.NoError(t, e.db.First(&ch, task.ChannelId).Error)
	require.NoError(t, e.db.Model(&model.Task{}).Where("task_id = ?", task.TaskID).Count(&n).Error)
	assert.EqualValues(t, 1, n, "original task row must survive")
	row := e.load(task)
	assert.Equal(t, task.GetUpstreamTaskID(), row.GetUpstreamTaskID())
	assert.Equal(t, wallet, u.Quota, "actual wallet")
	assert.Equal(t, quota, row.Quota, "durable task charge")
	assert.Equal(t, used, u.UsedQuota, "user usage is statistics, not wallet")
	assert.Equal(t, used, ch.UsedQuota, "channel usage")
	assert.Equal(t, 1, u.RequestCount, "completion is not a new request")
	assert.Equal(t, tokenRemain, token.RemainQuota, "key remaining quota")
	assert.Equal(t, tokenUsed, token.UsedQuota, "key usage")
	var logs []model.Log
	require.NoError(t, e.db.Where("user_id = ?", task.UserId).Order("id").Find(&logs).Error)
	encoded, err := json.Marshal(logs)
	require.NoError(t, err)
	t.Log("billing logs:", string(encoded))
	delta := quota - 100
	if delta == 0 {
		assert.Empty(t, logs, "no adjustment log when no adjustment committed")
	} else if assert.Len(t, logs, 1, "one committed adjustment requires one log") {
		wantType, wantQuota := model.LogTypeConsume, delta
		if delta < 0 {
			wantType, wantQuota = model.LogTypeRefund, -delta
		}
		assert.Equal(t, wantType, logs[0].Type)
		assert.Equal(t, wantQuota, logs[0].Quota)
		assert.Equal(t, task.UserId, logs[0].UserId)
		assert.Equal(t, task.ChannelId, logs[0].ChannelId)
		assert.Contains(t, logs[0].Other, task.TaskID)
	}
}

func TestRT19KlingChannelScopedExternalID(t *testing.T) {
	e := rt19Setup(t)
	var callsA, callsB atomic.Int32
	a := e.server(func(w http.ResponseWriter, r *http.Request) {
		callsA.Add(1)
		rt19Reply(w, "same-external-id", "succeed", "https://fixture.invalid/A.mp4")
	})
	b := e.server(func(w http.ResponseWriter, r *http.Request) {
		callsB.Add(1)
		rt19Reply(w, "same-external-id", "succeed", "https://fixture.invalid/B.mp4")
	})
	first := e.seed(1901, "same-external-id", a.URL)
	second := e.seed(1902, "same-external-id", b.URL)
	e.poll()
	for i, task := range []*model.Task{first, second} {
		row := e.load(task)
		assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), row.Status)
		assert.Equal(t, []string{"https://fixture.invalid/A.mp4", "https://fixture.invalid/B.mp4"}[i], row.GetResultURL(), "result must stay with its original channel/user")
		e.accounting(task, 900, 100, 100, 900, 100)
	}
	assert.EqualValues(t, 1, callsA.Load())
	assert.EqualValues(t, 1, callsB.Load())
}

func TestRT19KlingRejectsWrongReturnedTaskID(t *testing.T) {
	e := rt19Setup(t)
	s := e.server(func(w http.ResponseWriter, r *http.Request) {
		rt19Reply(w, "different-task", "succeed", "https://fixture.invalid/other-account.mp4")
	})
	task := e.seed(1903, "expected-task", s.URL)
	e.poll()
	row := e.load(task)
	assert.Equal(t, model.TaskStatus(model.TaskStatusInProgress), row.Status, "a response for another external task is not completion")
	assert.Empty(t, row.GetResultURL())
	assert.NotContains(t, string(row.Data), "other-account.mp4")
	e.accounting(task, 900, 100, 100, 900, 100)
}

func TestRT19KlingUnknownStatusCanRecover(t *testing.T) {
	e := rt19Setup(t)
	var calls atomic.Int32
	s := e.server(func(w http.ResponseWriter, r *http.Request) {
		status := "recovering"
		if calls.Add(1) > 1 {
			status = "succeed"
		}
		rt19Reply(w, "unknown-then-success", status, "https://fixture.invalid/recovered.mp4")
	})
	task := e.seed(1904, "unknown-then-success", s.URL)
	e.poll()
	assert.Equal(t, model.TaskStatus(model.TaskStatusInProgress), e.load(task).Status)
	e.accounting(task, 900, 100, 100, 900, 100)
	e.poll()
	assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), e.load(task).Status)
	e.accounting(task, 900, 100, 100, 900, 100)
	assert.EqualValues(t, 2, calls.Load())
}

func TestRT19KlingFailureWinsAgainstInFlightSuccess(t *testing.T) {
	e := rt19Setup(t)
	var calls atomic.Int32
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	s := e.server(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			rt19Reply(w, "late-success", "succeed", "https://fixture.invalid/late.mp4")
			return
		}
		rt19Reply(w, "late-success", "failed", "")
	})
	task := e.seed(1905, "late-success", s.URL)
	// Cleanup is registered after the server, so it unblocks the handler before
	// httptest.Server.Close waits for active requests.
	t.Cleanup(func() {
		unblock()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("poller did not stop")
		}
	})
	go func() { defer close(done); e.poll() }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first poll never reached the fixture")
	}
	e.poll()
	unblock()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("late poll did not return")
	}
	row := e.load(task)
	assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), row.Status)
	assert.Equal(t, model.TaskRefundStatusCompleted, row.RefundStatus)
	assert.Empty(t, row.PrivateData.ResultURL)
	e.accounting(task, 1000, 0, 0, 1000, 0)
	assert.EqualValues(t, 2, calls.Load())
}

func TestRT19SettlementReplayUsesPersistedAmount(t *testing.T) {
	e := rt19Setup(t)
	task := e.seed(1906, "settlement-replay", "http://127.0.0.1:1")
	task.Status = model.TaskStatusSuccess
	task.PrivateData.BillingContext.PerCallBilling = false
	require.NoError(t, e.db.Save(task).Error)
	first, stale := e.load(task), e.load(task)
	service.RecalculateTaskQuota(context.Background(), first, 150, "rt19 completion")
	service.RecalculateTaskQuota(context.Background(), stale, 150, "rt19 replay")
	e.accounting(task, 850, 150, 150, 850, 150)
}

func TestRT19SettlementConcurrentStaleSnapshots(t *testing.T) {
	e := rt19Setup(t)
	task := e.seed(1907, "concurrent-settlement", "http://127.0.0.1:1")
	task.Status = model.TaskStatusSuccess
	task.PrivateData.BillingContext.PerCallBilling = false
	require.NoError(t, e.db.Save(task).Error)
	snapshots := []*model.Task{e.load(task), e.load(task)}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, snapshot := range snapshots {
		wg.Add(1)
		go func(row *model.Task) {
			defer wg.Done()
			<-start
			service.RecalculateTaskQuota(context.Background(), row, 150, "rt19 concurrent completion")
		}(snapshot)
	}
	close(start)
	wg.Wait()
	e.accounting(task, 850, 150, 150, 850, 150)
}

func TestRT19SettlementTaskWriteFailureCannotMoveFunds(t *testing.T) {
	e := rt19Setup(t)
	task := e.seed(1908, "quota-write-failure", "http://127.0.0.1:1")
	task.Status = model.TaskStatusSuccess
	task.PrivateData.BillingContext.PerCallBilling = false
	require.NoError(t, e.db.Save(task).Error)
	const hook = "rt19:reject_task_quota_write"
	require.NoError(t, e.db.Callback().Update().Before("gorm:update").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table == "tasks" {
			tx.AddError(errors.New("RT19 injected task write failure"))
		}
	}))
	t.Cleanup(func() { _ = e.db.Callback().Update().Remove(hook) })
	service.RecalculateTaskQuota(context.Background(), e.load(task), 150, "rt19 injected failure")
	// This is a database write fault, NOT a process-kill test.
	e.accounting(task, 900, 100, 100, 900, 100)
	require.NoError(t, e.db.Callback().Update().Remove(hook))
	service.RecalculateTaskQuota(context.Background(), e.load(task), 150, "rt19 retry")
	e.accounting(task, 850, 150, 150, 850, 150)
}

func TestRT19DeletedKeySubscriptionRefundKeepsOriginalPayer(t *testing.T) {
	e := rt19Setup(t)
	task := e.seed(1909, "subscription-key-deleted", "http://127.0.0.1:1")
	sub := &model.UserSubscription{Id: 1909, UserId: 1909, AmountTotal: 1000, AmountUsed: 100, Status: "active", StartTime: time.Now().Unix(), EndTime: time.Now().Add(24 * time.Hour).Unix()}
	require.NoError(t, e.db.Create(sub).Error)
	require.NoError(t, e.db.Model(&model.User{}).Where("id = ?", task.UserId).Update("quota", 1000).Error)
	task.Status = model.TaskStatusFailure
	task.PrivateData.BillingSource = "subscription"
	task.PrivateData.SubscriptionId = sub.Id
	require.NoError(t, e.db.Save(task).Error)
	require.NoError(t, e.db.Delete(&model.Token{}, task.PrivateData.TokenId).Error)
	first, stale := e.load(task), e.load(task)
	require.True(t, service.RefundTaskQuota(context.Background(), first, "rt19 subscription failure"))
	require.True(t, service.RefundTaskQuota(context.Background(), stale, "rt19 refund replay"))
	var u model.User
	var restored model.UserSubscription
	var n int64
	require.NoError(t, e.db.First(&u, task.UserId).Error)
	require.NoError(t, e.db.First(&restored, sub.Id).Error)
	require.NoError(t, e.db.Model(&model.Token{}).Where("id = ?", task.PrivateData.TokenId).Count(&n).Error)
	assert.Equal(t, 1000, u.Quota, "subscription refund must never become wallet cash")
	assert.Zero(t, u.UsedQuota)
	assert.Equal(t, 1, u.RequestCount)
	var ch model.Channel
	require.NoError(t, e.db.First(&ch, task.ChannelId).Error)
	assert.Zero(t, ch.UsedQuota)
	assert.Zero(t, e.load(task).Quota)
	var logs []model.Log
	require.NoError(t, e.db.Where("user_id = ?", task.UserId).Find(&logs).Error)
	if assert.Len(t, logs, 1) {
		assert.Equal(t, model.LogTypeRefund, logs[0].Type)
		assert.Equal(t, 100, logs[0].Quota)
	}
	assert.Zero(t, restored.AmountUsed)
	assert.Zero(t, n, "deleted key must not be revived")
	assert.Equal(t, model.TaskRefundStatusCompleted, e.load(task).RefundStatus)
	assert.Equal(t, task.PrivateData.SubscriptionId, e.load(task).PrivateData.SubscriptionId)
}

func TestRT19TaskLookupRequiresOwner(t *testing.T) {
	e := rt19Setup(t)
	a := e.seed(1910, "private-upstream-A", "http://127.0.0.1:1")
	b := e.seed(1911, "private-upstream-B", "http://127.0.0.1:1")
	owned, exists, err := model.GetByTaskId(a.UserId, a.TaskID)
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, a.ID, owned.ID)
	for _, id := range []string{a.TaskID, a.GetUpstreamTaskID()} {
		_, exists, err := model.GetByTaskId(b.UserId, id)
		require.NoError(t, err)
		assert.False(t, exists)
	}
	rows, err := model.GetByTaskIds(b.UserId, []any{a.TaskID, b.TaskID})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, b.ID, rows[0].ID)
	// Model ownership checks are not an HTTP authentication/download test.
}
