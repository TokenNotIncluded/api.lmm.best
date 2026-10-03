package perfmetrics

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
)

func TestRecordTaskResult(t *testing.T) {
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })
	for _, tc := range []struct {
		name   string
		change func(*model.Task, *relaycommon.TaskInfo)
		model  string
		group  string
		want   counters
	}{
		{"billing_context_model", nil, "priced-model", "task-group", counters{requestCount: 1, successCount: 1, totalLatencyMs: 120_000, outputTokens: 5000, generationMs: 100_000}},
		{"properties_fallback", func(task *model.Task, _ *relaycommon.TaskInfo) { task.PrivateData.BillingContext = nil }, "legacy-model", "task-group", counters{requestCount: 1, successCount: 1, totalLatencyMs: 120_000, outputTokens: 5000, generationMs: 100_000}},
		{"empty_billing_model_fallback", func(task *model.Task, _ *relaycommon.TaskInfo) { task.PrivateData.BillingContext.OriginModelName = "" }, "legacy-model", "task-group", counters{requestCount: 1, successCount: 1, totalLatencyMs: 120_000, outputTokens: 5000, generationMs: 100_000}},
		{"default_group", func(task *model.Task, _ *relaycommon.TaskInfo) { task.Group = "" }, "priced-model", "default", counters{requestCount: 1, successCount: 1, totalLatencyMs: 120_000, outputTokens: 5000, generationMs: 100_000}},
		{"completion_tokens", func(_ *model.Task, result *relaycommon.TaskInfo) {
			result.TotalTokens, result.CompletionTokens = 0, 3000
		}, "priced-model", "task-group", counters{requestCount: 1, successCount: 1, totalLatencyMs: 120_000, outputTokens: 3000, generationMs: 100_000}},
		{"negative_total_uses_completion", func(_ *model.Task, result *relaycommon.TaskInfo) {
			result.TotalTokens, result.CompletionTokens = -1, 3000
		}, "priced-model", "task-group", counters{requestCount: 1, successCount: 1, totalLatencyMs: 120_000, outputTokens: 3000, generationMs: 100_000}},
		{"no_usage", func(_ *model.Task, result *relaycommon.TaskInfo) { result.TotalTokens, result.CompletionTokens = 0, 0 }, "priced-model", "task-group", counters{requestCount: 1, successCount: 1, totalLatencyMs: 120_000}},
		{"negative_usage", func(_ *model.Task, result *relaycommon.TaskInfo) {
			result.TotalTokens, result.CompletionTokens = -1, -1
		}, "priced-model", "task-group", counters{requestCount: 1, successCount: 1, totalLatencyMs: 120_000}},
		{"persisted_failure_ignores_usage", func(task *model.Task, _ *relaycommon.TaskInfo) { task.Status = model.TaskStatusFailure }, "priced-model", "task-group", counters{requestCount: 1, totalLatencyMs: 120_000}},
		{"persisted_success_controls_outcome", func(_ *model.Task, result *relaycommon.TaskInfo) { result.Status = model.TaskStatusFailure }, "priced-model", "task-group", counters{requestCount: 1, successCount: 1, totalLatencyMs: 120_000, outputTokens: 5000, generationMs: 100_000}},
		{"missing_start_uses_submit", func(task *model.Task, _ *relaycommon.TaskInfo) { task.StartTime = 0 }, "priced-model", "task-group", counters{requestCount: 1, successCount: 1, totalLatencyMs: 120_000, outputTokens: 5000, generationMs: 120_000}},
		{"invalid_timestamps", func(task *model.Task, _ *relaycommon.TaskInfo) {
			task.SubmitTime, task.StartTime = task.FinishTime+1, task.FinishTime+1
		}, "priced-model", "task-group", counters{requestCount: 1, successCount: 1}},
		{"missing_timestamps", func(task *model.Task, _ *relaycommon.TaskInfo) { task.SubmitTime, task.StartTime = 0, 0 }, "priced-model", "task-group", counters{requestCount: 1, successCount: 1}},
		{"timestamp_overflow", func(task *model.Task, _ *relaycommon.TaskInfo) {
			task.SubmitTime, task.StartTime, task.FinishTime = 1, 1, math.MaxInt64
		}, "priced-model", "task-group", counters{requestCount: 1, successCount: 1}},
		{"missing_model_ignored", func(task *model.Task, _ *relaycommon.TaskInfo) {
			task.PrivateData.BillingContext, task.Properties = nil, model.Properties{}
		}, "", "", counters{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetHotBucketsForTest()
			t.Cleanup(resetHotBucketsForTest)
			task := &model.Task{
				TaskID: "private-task-id", UserId: 42,
				Status: model.TaskStatusSuccess, Group: "task-group",
				SubmitTime: 1000, StartTime: 1020, FinishTime: 1120,
				Properties:  model.Properties{OriginModelName: "legacy-model"},
				PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{OriginModelName: "priced-model"}},
			}
			result := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess, TotalTokens: 5000, CompletionTokens: 9999}
			if tc.change != nil {
				tc.change(task, result)
			}
			RecordTaskResult(task, result)
			var got counters
			hotBuckets.Range(func(key, value any) bool {
				k := key.(bucketKey)
				if k.model != tc.model || k.group != tc.group {
					t.Fatalf("metric key=%+v, want model=%s group=%s", k, tc.model, tc.group)
				}
				merge := map[bucketKey]counters{k: got}
				mergeCounters(merge, k, value.(*atomicBucket).snapshot())
				got = merge[k]
				return true
			})
			if got != tc.want {
				t.Fatalf("counters=%+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestRecordTaskResultMissingFinishAndNilUsageIsReadOnly(t *testing.T) {
	resetHotBucketsForTest()
	t.Cleanup(resetHotBucketsForTest)
	task := &model.Task{
		Status: model.TaskStatusFailure, SubmitTime: time.Now().Unix() - 60,
		Properties: model.Properties{OriginModelName: "suno"},
	}
	before := *task
	start := time.Now().Unix()
	RecordTaskResult(task, nil)
	end := time.Now().Unix()
	if !reflect.DeepEqual(before, *task) {
		t.Fatal("metric sampling mutated the task")
	}
	hotBuckets.Range(func(_, value any) bool {
		got := value.(*atomicBucket).snapshot()
		if got.requestCount != 1 || got.successCount != 0 || got.outputTokens != 0 || got.generationMs != 0 || got.ttftCount != 0 {
			t.Fatalf("unexpected counters: %+v", got)
		}
		if got.totalLatencyMs < (start-task.SubmitTime)*1000 || got.totalLatencyMs > (end-task.SubmitTime)*1000 {
			t.Fatalf("fallback latency=%d, outside observation interval", got.totalLatencyMs)
		}
		return true
	})
	if hotBucketCount.Load() != 1 {
		t.Fatalf("bucket count=%d, want 1", hotBucketCount.Load())
	}
}

func TestRecordTaskResultIgnoresNonTerminalTasks(t *testing.T) {
	resetHotBucketsForTest()
	t.Cleanup(resetHotBucketsForTest)
	RecordTaskResult(nil, nil)
	for _, status := range []model.TaskStatus{model.TaskStatusSubmitted, model.TaskStatusQueued, model.TaskStatusInProgress, model.TaskStatusNotStart, model.TaskStatusUnknown, ""} {
		RecordTaskResult(&model.Task{Status: status, Properties: model.Properties{OriginModelName: "nonterminal"}}, &relaycommon.TaskInfo{TotalTokens: 5000})
	}
	if got := hotBucketCount.Load(); got != 0 {
		t.Fatalf("nonterminal bucket count=%d, want 0", got)
	}
}
