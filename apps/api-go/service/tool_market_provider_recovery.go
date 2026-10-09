package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/marketprovider"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This method performs only status reads for runs durably bound to local calls.
// It never lists a merchant's history or repeats monid_run after uncertainty.
func (r *ToolMarketRemote) recoverProviderCalls(ctx context.Context) (int, error) {
	calls, err := model.PendingToolMarketProviderCalls(ctx)
	if err != nil {
		return 0, err
	}
	processed := 0
	var failures error
	for _, call := range calls {
		if ctx.Err() != nil {
			break
		}
		select {
		case r.slots <- struct{}{}:
		default:
			return processed, failures
		}
		claimed, err := model.ClaimToolMarketProviderPoll(ctx, call.ID)
		if claimed && err == nil {
			pollCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
			err = r.pollMonidRun(pollCtx, &call)
			cancel()
			processed++
		}
		<-r.slots
		// Never attach upstream error strings to task logs or public status.
		if err != nil {
			failures = errors.Join(failures, ErrMarketRemoteConnection)
		}
	}
	return processed, failures
}

func (r *ToolMarketRemote) pollMonidRun(ctx context.Context, call *model.ToolMarketCall) error {
	if call.ProviderQuote == nil || call.ProviderQuote.Provider != "monid" || !marketprovider.ValidRunID(call.ProviderRunID) {
		return model.ErrToolMarketInput
	}
	preset, _ := marketprovider.Find("monid")
	credential, err := model.ResolveToolMarketCredential(call.OwnerID, call.ServiceID, call.VersionID, preset.Endpoint)
	if err != nil {
		return err
	}
	if credential.Mode != "bearer" {
		return ErrMarketRemoteAuth
	}
	var tool model.ToolMarketToolVersion
	if err := model.DB.WithContext(ctx).First(&tool, "tool_id = ? AND version_id = ?", call.ToolID, call.VersionID).Error; err != nil {
		return err
	}
	if tool.ProviderPricing == nil || tool.ProviderPricing.Validate(preset.Endpoint, tool.Name) != nil {
		return model.ErrToolMarketInput
	}
	// The second Monid host is code-owned. No URL in a tool result is followed.
	endpoint := "https://api.monid.ai/v1/runs/" + call.ProviderRunID
	client := *r.client
	transport, ok := r.client.Transport.(marketResponseTransport)
	if !ok {
		transport = marketResponseTransport{base: r.client.Transport}
	}
	if transport.base == nil {
		transport.base = http.DefaultTransport
	}
	transport.endpoint, transport.credential, transport.capture = endpoint, credential, nil
	client.Transport = transport
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return ErrMarketRemoteNetwork }
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ErrMarketRemoteResult
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		return ErrMarketRemoteResult
	}
	var data map[string]any
	if marketDecodeExactJSON(raw, &data) != nil || data == nil || !marketRemoteSecretSafe(data, credential) {
		return ErrMarketRemoteResult
	}
	if data["runId"] != call.ProviderRunID || data["provider"] != call.ProviderQuote.TargetProvider || data["endpoint"] != call.ProviderQuote.TargetEndpoint {
		return ErrMarketRemoteResult
	}
	text, err := json.Marshal(data)
	if err != nil {
		return err
	}
	exact := map[string]any{"structuredContent": data, "content": []any{map[string]any{"type": "text", "text": string(text)}}}
	result := &mcp.CallToolResult{StructuredContent: data, Content: []mcp.Content{&mcp.TextContent{Text: string(text)}}}
	_, err = finishMarketRemoteResult(call, tool, result, exact)
	return err
}
