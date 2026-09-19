package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gorm.io/gorm"
)

var (
	ErrMarketRemoteConnection = errors.New("remote MCP connection failed; check the HTTPS endpoint and availability")
	ErrMarketRemoteAuth       = errors.New("this remote requires authentication; credential-based services are not supported yet")
	ErrMarketRemoteSchema     = errors.New("remote MCP tool definitions are invalid or unsupported")
	ErrMarketRemoteChanged    = errors.New("remote MCP tool definition changed; synchronize and review a new version")
	ErrMarketRemoteInput      = errors.New("arguments do not match the approved tool input schema")
	ErrMarketRemoteResult     = errors.New("remote MCP returned an invalid final result")
	ErrMarketRemoteBusy       = errors.New("tool execution capacity is busy; retry later")
	ErrMarketRemoteNetwork    = errors.New("remote MCP endpoint is not an allowed public HTTPS destination")
)

var marketDeniedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("168.63.129.16/32"), netip.MustParsePrefix("fec0::/10"), netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2001::/32"), netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"),
}

func marketPublicIP(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range marketDeniedPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func marketRemoteURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return nil, ErrMarketRemoteNetwork
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !marketPublicIP(ip) {
		return nil, ErrMarketRemoteNetwork
	}
	return u, nil
}

// Resolves at dial time, validates every answer, then dials a checked literal IP.
// Proxy environment variables, redirects and global "allow private IP" flags
// cannot change this boundary. TLS still verifies the original request host.
func newMarketRemoteHTTPClient() *http.Client {
	transport := &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second, MaxResponseHeaderBytes: 32 << 10, MaxIdleConns: 32, MaxConnsPerHost: 8, IdleConnTimeout: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || port != "443" {
			return nil, ErrMarketRemoteNetwork
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, ErrMarketRemoteConnection
		}
		for _, ip := range ips {
			if !marketPublicIP(ip) {
				return nil, ErrMarketRemoteNetwork
			}
		}
		var last error
		for _, ip := range ips {
			conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
		}
		return nil, last
	}
	return &http.Client{Transport: marketResponseTransport{base: transport}, Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrMarketRemoteNetwork }}
}

type marketResponseTransport struct{ base http.RoundTripper }

func (t marketResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if _, err := marketRemoteURL(req.URL.String()); err != nil {
		return nil, err
	}
	// Only protocol headers are allowed out. Never forward any LMM credential.
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	clone.Header.Del("Authorization")
	clone.Header.Del("Cookie")
	clone.Header.Del("Proxy-Authorization")
	resp, err := t.base.RoundTrip(clone)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		_ = resp.Body.Close()
		return nil, ErrMarketRemoteAuth
	}
	resp.Body = &marketLimitedBody{ReadCloser: resp.Body, remaining: 2 << 20}
	return resp, nil
}

type marketLimitedBody struct {
	io.ReadCloser
	remaining int64
}

func (b *marketLimitedBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		var probe [1]byte
		n, err := b.ReadCloser.Read(probe[:])
		if n > 0 {
			return 0, ErrMarketRemoteResult
		}
		return 0, err
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	return n, err
}

type ToolMarketRemote struct {
	client *http.Client
	slots  chan struct{}
}

var marketRemote = &ToolMarketRemote{client: newMarketRemoteHTTPClient(), slots: make(chan struct{}, 32)}

func (r *ToolMarketRemote) connect(ctx context.Context, endpoint string) (*mcp.ClientSession, error) {
	if _, err := marketRemoteURL(endpoint); err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "lmm-tool-market", Version: "1"}, &mcp.ClientOptions{MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true}})
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: r.client, MaxRetries: -1, DisableStandaloneSSE: true}, nil)
	if err != nil {
		if errors.Is(err, ErrMarketRemoteAuth) {
			return nil, ErrMarketRemoteAuth
		}
		return nil, ErrMarketRemoteConnection
	}
	return session, nil
}

func marketSchema(raw []byte) (*jsonschema.Resolved, error) {
	if len(raw) == 0 || len(raw) > 16384 {
		return nil, ErrMarketRemoteSchema
	}
	var tree any
	if json.Unmarshal(raw, &tree) != nil {
		return nil, ErrMarketRemoteSchema
	}
	if !marketJSONDepth(tree, 0) {
		return nil, ErrMarketRemoteSchema
	}
	if !marketSchemaReferencesSafe(tree) {
		return nil, ErrMarketRemoteSchema
	}
	var schema jsonschema.Schema
	if json.Unmarshal(raw, &schema) != nil || schema.Type != "object" {
		return nil, ErrMarketRemoteSchema
	}
	switch schema.Schema {
	case "", "http://json-schema.org/draft-07/schema#", "https://json-schema.org/draft-07/schema#", "https://json-schema.org/draft/2020-12/schema":
	default:
		return nil, ErrMarketRemoteSchema
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		return nil, ErrMarketRemoteSchema
	}
	return resolved, nil
}

func marketSchemaReferencesSafe(value any) bool {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if key == "$id" || key == "$dynamicRef" || key == "$dynamicAnchor" {
				return false
			}
			if key == "$ref" {
				ref, ok := child.(string)
				if !ok || !strings.HasPrefix(ref, "#/") {
					return false
				}
			}
			if !marketSchemaReferencesSafe(child) {
				return false
			}
		}
	case []any:
		for _, child := range value {
			if !marketSchemaReferencesSafe(child) {
				return false
			}
		}
	}
	return true
}

func marketJSONDepth(value any, depth int) bool {
	if depth > 32 {
		return false
	}
	switch value := value.(type) {
	case map[string]any:
		for _, v := range value {
			if !marketJSONDepth(v, depth+1) {
				return false
			}
		}
	case []any:
		for _, v := range value {
			if !marketJSONDepth(v, depth+1) {
				return false
			}
		}
	}
	return true
}

func marketCanonical(value any) string {
	data, _ := json.Marshal(value)
	var normalized any
	_ = json.Unmarshal(data, &normalized)
	data, _ = json.Marshal(normalized)
	return string(data)
}
func marketRemoteFingerprint(tool *mcp.Tool) string {
	// Include description and hints: these are Agent-facing untrusted content.
	data := marketCanonical(map[string]any{"name": tool.Name, "description": tool.Description, "input": tool.InputSchema, "output": tool.OutputSchema, "annotations": tool.Annotations})
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

func marketListRemote(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Tool, error) {
	items := []*mcp.Tool{}
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < 10; page++ {
		result, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, ErrMarketRemoteSchema
		}
		for _, tool := range result.Tools {
			if tool == nil || tool.Name == "" || len(tool.Name) > 128 || len(tool.Description) > 4000 || seen[tool.Name] {
				return nil, ErrMarketRemoteSchema
			}
			seen[tool.Name] = true
			input, _ := json.Marshal(tool.InputSchema)
			if _, err := marketSchema(input); err != nil {
				return nil, err
			}
			if tool.OutputSchema != nil {
				output, _ := json.Marshal(tool.OutputSchema)
				if _, err := marketSchema(output); err != nil {
					return nil, err
				}
			}
			items = append(items, tool)
			if len(items) > 100 {
				return nil, ErrMarketRemoteSchema
			}
		}
		if result.NextCursor == "" {
			if len(items) == 0 {
				return nil, ErrMarketRemoteSchema
			}
			return items, nil
		}
		if result.NextCursor == cursor {
			return nil, ErrMarketRemoteSchema
		}
		cursor = result.NextCursor
	}
	return nil, ErrMarketRemoteSchema
}

func InspectToolMarketRemote(ctx context.Context, endpoint string) ([]model.ToolMarketToolInput, error) {
	return marketRemote.inspect(ctx, endpoint)
}
func (r *ToolMarketRemote) inspect(ctx context.Context, endpoint string) ([]model.ToolMarketToolInput, error) {
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	default:
		return nil, ErrMarketRemoteBusy
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	session, err := r.connect(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	tools, err := marketListRemote(ctx, session)
	if err != nil {
		return nil, err
	}
	rows := make([]model.ToolMarketToolInput, 0, len(tools))
	for _, tool := range tools {
		input, _ := json.Marshal(tool.InputSchema)
		var output []byte
		if tool.OutputSchema != nil {
			output, _ = json.Marshal(tool.OutputSchema)
		}
		// Hints are not security attestations. Authors must review permissions.
		rows = append(rows, model.ToolMarketToolInput{Name: tool.Name, Description: tool.Description, InputSchema: input, OutputSchema: output, Permissions: []string{"network", "external_account", "write", "delete", "send"}})
	}
	return rows, nil
}

func ValidateToolMarketRemote(ctx context.Context, actor int, serviceID string, review bool) error {
	return marketRemote.validate(ctx, actor, serviceID, review)
}
func (r *ToolMarketRemote) validate(ctx context.Context, actor int, serviceID string, review bool) error {
	var detail *model.ToolMarketDetail
	var err error
	if review {
		detail, err = model.GetToolMarketReview(actor, serviceID)
	} else {
		detail, err = model.GetToolMarketDetail(actor, serviceID, true)
	}
	if err != nil {
		return err
	}
	if detail.Version.ExecutionType != "remote" {
		return ErrMarketRemoteSchema
	}
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	default:
		return ErrMarketRemoteBusy
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	session, err := r.connect(ctx, detail.Version.Endpoint)
	if err != nil {
		return err
	}
	defer session.Close()
	tools, err := marketListRemote(ctx, session)
	if err != nil {
		return err
	}
	fingerprints := map[string]string{}
	for _, local := range detail.Tools {
		found := false
		for _, remote := range tools {
			if remote.Name == local.Name {
				if !marketSchemasMatch(local, remote) {
					return ErrMarketRemoteChanged
				}
				fingerprints[local.ToolID] = marketRemoteFingerprint(remote)
				found = true
				break
			}
		}
		if !found {
			return ErrMarketRemoteChanged
		}
	}
	return model.RecordToolMarketValidation(actor, serviceID, detail.Version.ID, detail.Version.Digest, fingerprints)
}

func marketSchemasMatch(local model.ToolMarketToolVersion, remote *mcp.Tool) bool {
	var input, output any
	if json.Unmarshal([]byte(local.InputSchema), &input) != nil {
		return false
	}
	if local.OutputSchema != "" && json.Unmarshal([]byte(local.OutputSchema), &output) != nil {
		return false
	}
	return marketCanonical(input) == marketCanonical(remote.InputSchema) && marketCanonical(output) == marketCanonical(remote.OutputSchema)
}

type ToolMarketExecutionResponse struct {
	Call            *model.ToolMarketCall `json:"call"`
	Result          json.RawMessage       `json:"result,omitempty"`
	ResultExpiresAt int64                 `json:"result_expires_at,omitempty"`
	ResultExpired   bool                  `json:"result_expired"`
	ErrorCode       string                `json:"error_code,omitempty"`
}

func GetToolMarketExecutionResponse(userID int, clientID, id string) (*ToolMarketExecutionResponse, error) {
	call, err := model.GetToolMarketCall(userID, clientID, id)
	if err != nil {
		return nil, err
	}
	data, expires, err := model.GetToolMarketResult(userID, clientID, id)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &ToolMarketExecutionResponse{Call: call, Result: data, ResultExpiresAt: expires, ResultExpired: errors.Is(err, gorm.ErrRecordNotFound) && call.ExecutionStatus == "succeeded"}, nil
}

func ExecuteToolMarketRemote(ctx context.Context, in model.ToolMarketReserveInput) (*ToolMarketExecutionResponse, error) {
	return marketRemote.execute(ctx, in)
}
func (r *ToolMarketRemote) execute(ctx context.Context, in model.ToolMarketReserveInput) (*ToolMarketExecutionResponse, error) {
	if in.RequestKey == "" || len(in.RequestKey) > 128 {
		return nil, model.ErrToolMarketInput
	}
	if prior, err := model.LookupToolMarketReplay(in); err == nil {
		return GetToolMarketExecutionResponse(in.UserID, in.ClientID, prior.ID)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	execution, err := model.GetToolMarketExecution(in.UserID, in.ClientID, in.ToolID, in.VersionID, in.GrantID)
	if err != nil {
		return nil, err
	}
	if execution.Version.ExecutionType != "remote" || execution.Tool.RemoteDigest == "" {
		return nil, ErrMarketRemoteSchema
	}
	inputSchema, err := marketSchema([]byte(execution.Tool.InputSchema))
	if err != nil {
		return nil, err
	}
	var arguments any
	if len(in.Arguments) > 128<<10 || json.Unmarshal(in.Arguments, &arguments) != nil || !marketJSONDepth(arguments, 0) || inputSchema.Validate(arguments) != nil {
		return nil, ErrMarketRemoteInput
	}
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	default:
		return nil, ErrMarketRemoteBusy
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	// Discovery and schema drift checks are read-only and happen before a hold.
	session, err := r.connect(ctx, execution.Version.Endpoint)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	tools, err := marketListRemote(ctx, session)
	if err != nil {
		return nil, err
	}
	matched := false
	for _, tool := range tools {
		if tool.Name == execution.Tool.Name && marketRemoteFingerprint(tool) == execution.Tool.RemoteDigest {
			matched = true
			break
		}
	}
	if !matched {
		return nil, ErrMarketRemoteChanged
	}
	in.GrantID = execution.Grant.ID
	in.ResolveBy = common.GetTimestamp() + 120
	call, created, err := model.ReserveToolMarketCall(in)
	if err != nil {
		return nil, err
	}
	if !created {
		return GetToolMarketExecutionResponse(in.UserID, in.ClientID, call.ID)
	}
	if ctx.Err() != nil {
		_ = model.FinishToolMarketCall(call.ID, false)
		return GetToolMarketExecutionResponse(in.UserID, in.ClientID, call.ID)
	}
	started, err := model.StartToolMarketCall(call.ID)
	if err != nil || !started {
		if err != nil {
			_ = model.FinishToolMarketCall(call.ID, false)
		}
		return GetToolMarketExecutionResponse(in.UserID, in.ClientID, call.ID)
	}
	result, callErr := session.CallTool(ctx, &mcp.CallToolParams{Name: execution.Tool.Name, Arguments: arguments})
	if callErr != nil || result == nil || len(result.InputRequests) > 0 || result.RequestState != "" {
		_ = model.MarkToolMarketCallUnknown(call.ID)
		response, err := GetToolMarketExecutionResponse(in.UserID, in.ClientID, call.ID)
		if response != nil {
			response.ErrorCode = "TOOL_MARKET_RESULT_UNKNOWN"
		}
		return response, err
	}
	success := !result.IsError
	errorCode := ""
	if success && execution.Tool.OutputSchema != "" {
		output, err := marketSchema([]byte(execution.Tool.OutputSchema))
		if err != nil || output.Validate(result.StructuredContent) != nil {
			success = false
			errorCode = "TOOL_MARKET_INVALID_RESULT"
		}
	}
	// An empty or explicitly intermediate business result is never a paid success.
	if success && len(result.Content) == 0 && result.StructuredContent == nil {
		success = false
		errorCode = "TOOL_MARKET_INVALID_RESULT"
	}
	if values, ok := result.StructuredContent.(map[string]any); ok && success {
		if flag, ok := values["success"].(bool); ok && !flag {
			success = false
		}
		if state, ok := values["status"].(string); ok {
			switch strings.ToLower(state) {
			case "pending", "queued", "accepted", "running", "in_progress", "processing":
				_ = model.MarkToolMarketCallUnknown(call.ID)
				response, err := GetToolMarketExecutionResponse(in.UserID, in.ClientID, call.ID)
				if response != nil {
					response.ErrorCode = "TOOL_MARKET_RESULT_UNKNOWN"
				}
				return response, err
			case "failed", "failure", "error", "cancelled":
				success = false
			}
		}
	}
	data, err := json.Marshal(result)
	if err != nil || len(data) > 2<<20 {
		success = false
		data = []byte(`{"isError":true,"content":[{"type":"text","text":"Remote result exceeds the supported limit."}]}`)
		errorCode = "TOOL_MARKET_INVALID_RESULT"
	}
	if err := model.RecordToolMarketResult(call.ID, success, data); err != nil {
		return nil, err
	}
	if err := model.FinishToolMarketCall(call.ID, success); err != nil {
		response, readErr := GetToolMarketExecutionResponse(in.UserID, in.ClientID, call.ID)
		if response != nil && response.Call.SettlementStatus == "held" {
			response.ErrorCode = "TOOL_MARKET_SETTLEMENT_PENDING"
		}
		return response, readErr
	}
	response, err := GetToolMarketExecutionResponse(in.UserID, in.ClientID, call.ID)
	if response != nil {
		response.ErrorCode = errorCode
	}
	return response, err
}
