package controller

// Only reviewed codes leave the server. Raw provider, database and credential
// errors must never appear in a trace or in a rendered error message.
func assistantPublicToolErrorCode(result map[string]any) string {
	status, _ := result["status"].(string)
	switch status {
	case "tool_disabled", "tool_level_denied", "tool_policy_unavailable", "operation_dispatch_unavailable", "invalid_arguments", "response_limit_exceeded", "policy_changed", "policy_unavailable", "not_found":
		return status
	case "tool_not_allowed":
		return "tool_level_denied"
	case "forbidden", "admin_access_denied", "target_forbidden":
		return "admin_access_denied"
	case "context_unavailable", "session_required":
		return "session_required"
	case "response_too_large":
		return "response_limit_exceeded"
	default:
		return "tool_failed"
	}
}
