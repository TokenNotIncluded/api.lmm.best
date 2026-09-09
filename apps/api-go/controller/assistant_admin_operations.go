package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/gin-gonic/gin"
)

const (
	assistantAdminOperationsContextKey   = "assistant_admin_operations"
	assistantAdminOperationBodyLimit     = 16 << 10
	assistantAdminOperationResponseLimit = 128 << 10
)

type assistantAdminOperation struct {
	ID       string `json:"operation_id"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Handler  string `json:"handler"`
	MinRole  int    `json:"minimum_role"`
	ReadOnly bool   `json:"read_only"`
}

// AssistantAdminOperationRegistry belongs to one Gin engine. Its catalog is
// populated only while that engine registers its authenticated dashboard routes.
type AssistantAdminOperationRegistry struct {
	engine     *gin.Engine
	mu         sync.RWMutex
	operations map[string]assistantAdminOperation
}

type assistantAdminDispatchKey struct{}

type assistantAdminOperationRequest struct {
	registry   *AssistantAdminOperationRegistry
	headers    http.Header
	host       string
	remoteAddr string
}

func NewAssistantAdminOperationRegistry(engine *gin.Engine) *AssistantAdminOperationRegistry {
	return &AssistantAdminOperationRegistry{engine: engine, operations: make(map[string]assistantAdminOperation)}
}

func (r *AssistantAdminOperationRegistry) Register(method, path, handler string, minimumRole int) {
	if minimumRole < common.RoleAdminUser || !strings.HasPrefix(path, "/api/") {
		return
	}
	op := assistantAdminOperation{ID: method + " " + path, Method: method, Path: path, Handler: handler, MinRole: minimumRole, ReadOnly: assistantAdminHandlerReadOnly(method, handler)}
	r.mu.Lock()
	r.operations[op.ID] = op
	r.mu.Unlock()
}

func (r *AssistantAdminOperationRegistry) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if expected, internal := c.Request.Context().Value(assistantAdminDispatchKey{}).(assistantAdminOperation); internal && (c.FullPath() != expected.Path || c.Request.Method != expected.Method) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "message": "operation route did not match its catalog entry"})
			return
		}
		// PrepareAssistantRequest later changes the relay billing identity and
		// body. Preserve the real HTTP credentials before any such preparation.
		c.Set(assistantAdminOperationsContextKey, &assistantAdminOperationRequest{registry: r, headers: c.Request.Header.Clone(), host: c.Request.Host, remoteAddr: c.Request.RemoteAddr})
		c.Next()
	}
}

func assistantAdminOperationToolDefinitions() []assistantOpenAIToolDefinition {
	return []assistantOpenAIToolDefinition{
		{Type: "function", Function: assistantOpenAIToolFunction{Name: "list_admin_operations", Description: "Discover authorized administration operations from the live dashboard router. Search by handler or route, paginate results, and inspect an exact operation_id to obtain its body/query/path contract. Includes all administrator and root dashboard features accessible to your current account. Unknown or partial schemas require inspection; never invent fields or credentials. The generic execution tool only permits read-only operations; mutations require an explicit UI confirmation flow. Existing role, permission, security-proof and audit controls still apply.", Parameters: objectSchema(map[string]any{
			"query":        map[string]any{"type": "string", "description": "Case-insensitive route or handler search, e.g. pricing, option, channel, subscription."},
			"operation_id": map[string]any{"type": "string", "description": "Exact METHOD /api/path-template from this catalog; returns its full contract."},
			"offset":       map[string]any{"type": "integer", "minimum": 0},
			"limit":        map[string]any{"type": "integer", "minimum": 1, "maximum": 30},
		}, nil)}},
		{Type: "function", Function: assistantOpenAIToolFunction{Name: "execute_admin_operation", Description: "Execute an exact read-only operation discovered with list_admin_operations through the original authenticated dashboard route. Mutations require an explicit administrator confirmation flow and cannot be executed by this tool. Use only documented path_params and query parameters. No URL, headers, token, role or actor override is accepted.", Parameters: objectSchema(map[string]any{
			"operation_id": map[string]any{"type": "string"},
			"path_params":  map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			"query":        map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			"body":         map[string]any{"description": "JSON value matching the discovered body_schema. Omit for body-free endpoints."},
		}, []string{"operation_id"})}},
	}
}

func assistantAdminOperationContext(c *gin.Context, userID int) (*assistantAdminOperationRequest, int, error) {
	if c == nil || c.Request == nil || assistantActorUserID(c) != userID {
		return nil, 0, errors.New("authenticated administrator session is required")
	}
	user, err := validateAssistantAdminAutomationSession(c, userID)
	if err != nil {
		return nil, 0, err
	}
	value, exists := c.Get(assistantAdminOperationsContextKey)
	state, valid := value.(*assistantAdminOperationRequest)
	if !exists || !valid || state.registry == nil || state.registry.engine == nil {
		return nil, 0, errors.New("administrator operation dispatch is unavailable")
	}
	return state, user.Role, nil
}

func executeAssistantAdminOperationsTool(c *gin.Context, userID int, input map[string]any) map[string]any {
	state, role, err := assistantAdminOperationContext(c, userID)
	if err != nil {
		return assistantAdminOperationError("forbidden", err.Error())
	}
	query := strings.ToLower(strings.TrimSpace(inputString(input, "query")))
	id := inputString(input, "operation_id")
	offset := assistantOperationInt(input["offset"], 0)
	limit := assistantOperationInt(input["limit"], 12)
	if offset < 0 || limit < 1 || limit > 30 {
		return assistantAdminOperationError("invalid_arguments", "offset must be nonnegative and limit must be 1 to 30")
	}
	state.registry.mu.RLock()
	operations := make([]assistantAdminOperation, 0, len(state.registry.operations))
	for _, op := range state.registry.operations {
		if op.MinRole <= role && (id == "" || id == op.ID) && (query == "" || strings.Contains(strings.ToLower(op.ID+" "+op.Handler), query)) {
			operations = append(operations, op)
		}
	}
	state.registry.mu.RUnlock()
	sort.Slice(operations, func(i, j int) bool { return operations[i].ID < operations[j].ID })
	total := len(operations)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	items := make([]map[string]any, 0, end-offset)
	for _, op := range operations[offset:end] {
		item := map[string]any{"operation_id": op.ID, "handler": op.Handler, "minimum_role": op.MinRole, "read_only": op.ReadOnly, "path_parameters": assistantOperationPathParameters(op.Path)}
		if id != "" {
			item["contract"] = assistantAdminOperationContract(op.Handler)
		}
		items = append(items, item)
	}
	result := map[string]any{"ok": true, "operations": items, "total": total, "offset": offset, "has_more": end < total, "contract_lookup": "Call again with an exact operation_id to inspect its request contract before execution.", "authorization": "Every execution reruns the original dashboard authentication, permissions, security verification and audit middleware."}
	if end < total {
		result["next_offset"] = end
	}
	return result
}

func assistantOperationInt(value any, fallback int) int {
	if value == nil {
		return fallback
	}
	switch v := value.(type) {
	case float64:
		if v == float64(int(v)) {
			return int(v)
		}
	case int:
		return v
	case json.Number:
		n, err := strconv.Atoi(string(v))
		if err == nil {
			return n
		}
	}
	return -1
}

func assistantAdminOperationReadOnly(c *gin.Context, arguments string) bool {
	if c == nil {
		return false
	}
	value, ok := c.Get(assistantAdminOperationsContextKey)
	state, valid := value.(*assistantAdminOperationRequest)
	if !ok || !valid || state.registry == nil {
		return false
	}
	var input struct {
		OperationID string `json:"operation_id"`
	}
	if json.Unmarshal([]byte(arguments), &input) != nil {
		return false
	}
	state.registry.mu.RLock()
	op, exists := state.registry.operations[input.OperationID]
	state.registry.mu.RUnlock()
	return exists && op.ReadOnly
}

func executeAssistantAdminOperationTool(c *gin.Context, userID int, input map[string]any) map[string]any {
	state, role, err := assistantAdminOperationContext(c, userID)
	if err != nil {
		return assistantAdminOperationError("forbidden", err.Error())
	}
	for key := range input {
		if key != "operation_id" && key != "path_params" && key != "query" && key != "body" {
			return assistantAdminOperationError("invalid_arguments", "unsupported argument: "+key)
		}
	}
	state.registry.mu.RLock()
	op, exists := state.registry.operations[inputString(input, "operation_id")]
	state.registry.mu.RUnlock()
	if !exists || op.MinRole > role {
		return assistantAdminOperationError("forbidden", "operation is unavailable for this account")
	}
	if !op.ReadOnly {
		return assistantAdminOperationError("confirmation_required", "administrator mutations require explicit UI confirmation and cannot be executed by this tool")
	}
	requestPath, err := assistantOperationPath(op.Path, input["path_params"])
	if err != nil {
		return assistantAdminOperationError("invalid_arguments", err.Error())
	}
	query, err := assistantOperationStringMap(input["query"])
	if err != nil {
		return assistantAdminOperationError("invalid_arguments", err.Error())
	}
	values := make(url.Values)
	for key, value := range query {
		if key == "" || strings.ContainsAny(key, "\x00\r\n") {
			return assistantAdminOperationError("invalid_arguments", "invalid query parameter")
		}
		values.Set(key, value)
	}
	var body []byte
	if raw, provided := input["body"]; provided {
		body, err = json.Marshal(raw)
		if err != nil {
			return assistantAdminOperationError("invalid_arguments", "body must be valid JSON")
		}
	}
	if len(body)+len(values.Encode())+len(requestPath) > assistantAdminOperationBodyLimit {
		return assistantAdminOperationError("invalid_arguments", "operation request exceeds 16 KiB; use a smaller documented change")
	}
	target := &url.URL{Path: requestPath, RawQuery: values.Encode()}
	req, err := http.NewRequestWithContext(context.WithValue(c.Request.Context(), assistantAdminDispatchKey{}, op), op.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return assistantAdminOperationError("invalid_arguments", "invalid operation request")
	}
	req.Header = state.headers.Clone()
	req.Header.Del("Accept-Encoding")
	req.Header.Del("Content-Length")
	req.Header.Del("Content-Encoding")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Host = state.host
	req.RemoteAddr = state.remoteAddr
	if err := req.Context().Err(); err != nil {
		return assistantAdminOperationError("cancelled", "operation cancelled before dispatch")
	}
	writer := &assistantOperationWriter{header: make(http.Header), limit: assistantAdminOperationResponseLimit}
	state.registry.engine.ServeHTTP(writer, req)
	result := map[string]any{"operation_id": op.ID, "http_status": writer.statusCode(), "read_only": op.ReadOnly, "dispatched": true, "mutation_attempted": !op.ReadOnly}
	if writer.overflow {
		result["ok"], result["status"], result["do_not_retry"] = false, "response_limit_exceeded", !op.ReadOnly
		result["outcome"] = "possibly_applied"
		result["error"] = "Response exceeded the capture limit. Inspect a narrower read to establish the outcome before further writes."
		return result
	}
	var response any
	if len(writer.body.Bytes()) == 0 {
		response = nil
	} else if json.Unmarshal(writer.body.Bytes(), &response) != nil {
		result["ok"], result["status"], result["do_not_retry"] = false, "non_json_response", !op.ReadOnly
		result["outcome"] = "possibly_applied"
		result["error"] = "This operation returned non-JSON content; it was withheld. Inspect current state before retrying any mutation."
		return result
	}
	success := writer.statusCode() >= 200 && writer.statusCode() < 300
	if object, ok := response.(map[string]any); ok {
		if value, exists := object["success"].(bool); exists {
			success = success && value
		}
	}
	result["ok"] = success
	result["response"] = assistantRedactOperationResponse(response, op.Handler)
	if !success {
		result["status"] = "operation_rejected"
	}
	return result
}

func assistantAdminOperationError(status, message string) map[string]any {
	return map[string]any{"ok": false, "status": status, "error": message}
}

func assistantOperationStringMap(input any) (map[string]string, error) {
	result := make(map[string]string)
	if input == nil {
		return result, nil
	}
	object, ok := input.(map[string]any)
	if !ok {
		return nil, errors.New("path_params and query must be JSON objects with string values")
	}
	for key, value := range object {
		text, ok := value.(string)
		if !ok {
			return nil, errors.New("path_params and query values must be strings")
		}
		result[key] = text
	}
	return result, nil
}

func assistantOperationPathParameters(template string) []string {
	parameters := []string{}
	for _, segment := range strings.Split(template, "/") {
		if strings.HasPrefix(segment, ":") || strings.HasPrefix(segment, "*") {
			parameters = append(parameters, segment[1:])
		}
	}
	return parameters
}

func assistantOperationPath(template string, raw any) (string, error) {
	parameters, err := assistantOperationStringMap(raw)
	if err != nil {
		return "", err
	}
	segments := strings.Split(template, "/")
	used := 0
	for i, segment := range segments {
		if strings.HasPrefix(segment, "*") {
			return "", errors.New("wildcard path operations require the dashboard")
		}
		if !strings.HasPrefix(segment, ":") {
			continue
		}
		name := segment[1:]
		value, exists := parameters[name]
		if !exists || value == "" {
			return "", fmt.Errorf("path parameter %s is required", name)
		}
		if value == "." || value == ".." || strings.ContainsAny(value, "/\\%?#") || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return "", errors.New("path parameter must be one literal segment without escaping or traversal")
		}
		segments[i] = value
		used++
	}
	if used != len(parameters) {
		return "", errors.New("unexpected path parameter")
	}
	return strings.Join(segments, "/"), nil
}

type assistantOperationWriter struct {
	header   http.Header
	body     bytes.Buffer
	status   int
	limit    int
	overflow bool
}

func (w *assistantOperationWriter) Header() http.Header { return w.header }
func (w *assistantOperationWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *assistantOperationWriter) Write(data []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	if w.body.Len()+len(data) > w.limit {
		w.overflow = true
		return len(data), nil
	}
	if !w.overflow {
		_, _ = w.body.Write(data)
	}
	return len(data), nil
}
func (w *assistantOperationWriter) Flush() { w.WriteHeader(http.StatusOK) }
func (w *assistantOperationWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// GET is not sufficient: test/update_balance endpoints perform real mutations.
// Unknown handlers stay serialized mutations until their behavior is reviewed.
func assistantAdminHandlerReadOnly(method, handler string) bool {
	if method != http.MethodGet {
		return false
	}
	switch handler {
	case
		"GetOptions", "GetUsdExchangeRate", "GetHeroSMSOptions", "GetProjectUpdate",
		"GetChannelAffinityCacheStats", "ListWaffoPancakeSubscriptionProductOptions", "GetDynamicPricingStatus", "GetCustomOAuthProvider",
		"GetCustomOAuthProviders", "GetPerformanceStats", "GetLogFiles", "GetSyncableChannels",
		"GetAllChannels", "SearchChannels", "ChannelListModels", "EnabledListModels",
		"GetChannelOps", "GetChannel", "GetTagModels", "GetCodexChannelUsage",
		"GetCodexChannelRateLimitResetCredits", "OllamaVersion", "GetAllUsers", "SearchUsers",
		"GetUser", "GetAllTopUps", "GetUserOAuthBindingsByAdmin", "ListUserDeveloperAccessRecommendationArchives",
		"AdminGetAssistantUserProfile", "AdminListMemories", "Admin2FAStats", "GetAllLogs",
		"SearchAllLogs", "GetLogsStat", "GetChannelAffinityUsageCacheStats", "GetAllQuotaDates",
		"GetQuotaDatesByUser", "GetAllFlowQuotaDates", "GetPermissionCatalog", "GetAllVendors",
		"GetVendorMeta", "SearchVendors", "GetAllModelsMeta", "GetModelMeta",
		"SearchModelsMeta", "GetMissingModels", "SyncUpstreamPreview", "GetGroups",
		"GetPrefillGroups", "GetAllRedemptions", "GetRedemption", "SearchRedemptions",
		"GetAllDiscountCodes", "GetDiscountCode", "SearchDiscountCodes", "AdminGetGifts",
		"AdminGetGiftClaims", "ListDeveloperAccessRequests", "ListAccountActionRequests", "AdminListSubscriptionPlans",
		"AdminListSubscriptionRecords", "AdminListUserSubscriptions", "RootListSubscriptionResetEligible", "GetAdminSecurityPolicy",
		"GetAdminSecurityStats", "ListAdminSecurityEvents", "ListAdminAssistantSecurityReviews", "ListAdminAssistantReviewTasks",
		"PreviewAdminAssistantReviewTaskCleanup", "GetAdminAssistantReviewTask", "ListAdminViolationFeeAppeals", "ListAdminReleaseNotes",
		"ListAdminOpenSourceBountyDisputes", "ListAdminPublicRelayContributions", "ListAdminPublicRelayReports", "GetFinanceOverview",
		"GetFinanceUsers", "GetFinanceUser", "ListFinanceEntries", "ListFinancePaymentMethods",
		"ExportFinancialData", "ListSystemTasks", "GetCurrentSystemTask", "GetSystemTask",
		"ListSystemInstances", "GetAllMidjourney", "GetAllTask", "GetModelDeploymentSettings",
		"GetAllDeployments", "SearchDeployments", "GetHardwareTypes", "GetLocations",
		"GetAvailableReplicas", "CheckClusterNameAvailability", "GetDeployment", "GetDeploymentLogs",
		"ListDeploymentContainers", "GetContainerDetails":
		return true
	default:
		return false
	}
}
