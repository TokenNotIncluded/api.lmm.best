package constant

type ContextKey string

const (
	ContextKeyTokenCountMeta  ContextKey = "token_count_meta"
	ContextKeyPromptTokens    ContextKey = "prompt_tokens"
	ContextKeyEstimatedTokens ContextKey = "estimated_tokens"

	ContextKeyOriginalModel     ContextKey = "original_model"
	ContextKeyRequestStartTime  ContextKey = "request_start_time"
	ContextKeyResponseByteLimit ContextKey = "response_byte_limit"

	// Set only after server-side drawing API-key authentication, never from input.
	ContextKeyDrawingRealToken ContextKey = "drawing_real_token"
	// Browser-only starting balance, set by the server, never request headers.
	ContextKeyWebDrawingMinimumQuota ContextKey = "web_drawing_minimum_quota"
	// Authenticated assistant actor retained before the relay switches to its
	// payer. These are server context values, never request JSON or headers.
	ContextKeyAssistantActorUserID ContextKey = "assistant_actor_user_id"
	ContextKeyAssistantActorGroup  ContextKey = "assistant_actor_group"

	/* token related keys */
	ContextKeyTokenUnlimited         ContextKey = "token_unlimited_quota"
	ContextKeyTokenKey               ContextKey = "token_key"
	ContextKeyTokenId                ContextKey = "token_id"
	ContextKeyTokenGroup             ContextKey = "token_group"
	ContextKeyTokenSpecificChannelId ContextKey = "specific_channel_id"
	ContextKeyTokenModelLimitEnabled ContextKey = "token_model_limit_enabled"
	ContextKeyTokenModelLimit        ContextKey = "token_model_limit"
	ContextKeyTokenCrossGroupRetry   ContextKey = "token_cross_group_retry"
	ContextKeyTokenAutoGroups        ContextKey = "token_auto_groups"

	/* channel related keys */
	ContextKeyChannelId                ContextKey = "channel_id"
	ContextKeyChannelName              ContextKey = "channel_name"
	ContextKeyChannelCreateTime        ContextKey = "channel_create_time"
	ContextKeyChannelBaseUrl           ContextKey = "base_url"
	ContextKeyChannelType              ContextKey = "channel_type"
	ContextKeyChannelSetting           ContextKey = "channel_setting"
	ContextKeyChannelOtherSetting      ContextKey = "channel_other_setting"
	ContextKeyChannelParamOverride     ContextKey = "param_override"
	ContextKeyChannelHeaderOverride    ContextKey = "header_override"
	ContextKeyChannelOrganization      ContextKey = "channel_organization"
	ContextKeyChannelAutoBan           ContextKey = "auto_ban"
	ContextKeyChannelModelMapping      ContextKey = "model_mapping"
	ContextKeyChannelStatusCodeMapping ContextKey = "status_code_mapping"
	ContextKeyChannelIsMultiKey        ContextKey = "channel_is_multi_key"
	ContextKeyChannelMultiKeyIndex     ContextKey = "channel_multi_key_index"
	ContextKeyChannelKey               ContextKey = "channel_key"

	// These request-scoped markers let relay selection distinguish an upstream
	// capability/availability failure from an application error. They are used
	// only for the current request; they never mutate the channel's persisted
	// status.
	ContextKeyUpstreamChannelFailure       ContextKey = "upstream_channel_failure"
	ContextKeyUpstreamCapabilityMismatch   ContextKey = "upstream_capability_mismatch"
	ContextKeyUpstreamUnsupportedParameter ContextKey = "upstream_unsupported_parameter"

	ContextKeyAutoGroup           ContextKey = "auto_group"
	ContextKeyAutoGroupIndex      ContextKey = "auto_group_index"
	ContextKeyAutoGroupRetryIndex ContextKey = "auto_group_retry_index"

	/* user related keys */
	ContextKeyUserId      ContextKey = "id"
	ContextKeyUserSetting ContextKey = "user_setting"
	ContextKeyUserQuota   ContextKey = "user_quota"
	ContextKeyUserStatus  ContextKey = "user_status"
	ContextKeyUserEmail   ContextKey = "user_email"
	ContextKeyUserGroup   ContextKey = "user_group"
	ContextKeyUsingGroup  ContextKey = "group"
	ContextKeyUserName    ContextKey = "username"

	ContextKeyLocalCountTokens ContextKey = "local_count_tokens"

	ContextKeySystemPromptOverride ContextKey = "system_prompt_override"

	// ContextKeyFileSourcesToCleanup stores file sources that need cleanup when request ends
	ContextKeyFileSourcesToCleanup ContextKey = "file_sources_to_cleanup"

	// ContextKeyAdminRejectReason stores an admin-only reject/block reason extracted from upstream responses.
	// It is not returned to end users, but can be persisted into consume/error logs for debugging.
	ContextKeyAdminRejectReason ContextKey = "admin_reject_reason"

	// ContextKeyBillingExemptReason is set only by upstream response validation.
	// It changes settlement, never the usage returned to the client.
	ContextKeyBillingExemptReason ContextKey = "billing_exempt_reason"

	// ContextKeyLanguage stores the user's language preference for i18n
	ContextKeyLanguage ContextKey = "language"
	ContextKeyIsStream ContextKey = "is_stream"
	// These belong to an HTTP SSE attempt, not a WebSocket connection or an
	// internal assistant recorder that can reset a failed attempt.
	ContextKeyHTTPStreamCommitted         ContextKey = "http_stream_committed"
	ContextKeyHTTPStreamDownstreamFailure ContextKey = "http_stream_downstream_failure"
	// ContextKeyRelayInfo exposes the active attempt's final protocol outcome to
	// request middleware. Store the RelayInfo pointer because streams replace status.
	ContextKeyRelayInfo ContextKey = "relay_info"

	// ContextKeyAuditLogged marks that the current request has already recorded
	// a manage/operation audit log inside the handler. When set, the admin-audit
	// fallback in authHelper (finishAdminAudit) skips its record to avoid
	// duplicate entries.
	ContextKeyAuditLogged ContextKey = "audit_logged"
)

const BillingExemptReasonClaudeRefusalNoOutput = "claude_refusal_no_output"
