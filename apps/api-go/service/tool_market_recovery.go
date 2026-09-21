package service

import (
	"context"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
)

type toolMarketRecoveryHandler struct{}

func (toolMarketRecoveryHandler) Type() string            { return "tool_market_recovery" }
func (toolMarketRecoveryHandler) Enabled() bool           { return true }
func (toolMarketRecoveryHandler) Interval() time.Duration { return 15 * time.Second }
func (toolMarketRecoveryHandler) NewPayload() any         { return nil }
func (toolMarketRecoveryHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	count, err := model.RecoverToolMarketCalls(ctx)
	status, message := model.SystemTaskStatusSucceeded, ""
	if err != nil {
		status, message = model.SystemTaskStatusFailed, "Tool market recovery could not complete; retry required"
	}
	_ = model.FinishSystemTask(task.TaskID, runnerID, status, map[string]int{"processed": count}, message)
}
func init() { RegisterSystemTaskHandler(toolMarketRecoveryHandler{}) }
