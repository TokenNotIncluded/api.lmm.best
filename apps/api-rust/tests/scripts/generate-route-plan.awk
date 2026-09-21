BEGIN {
  FS = OFS = "\t"
  print "method", "path", "legacy_handler", "domain", "auth_scope", "data_access", "streaming", "priority", "planned_rust_module", "job_dependency"
}

function starts(path, prefix) {
  return path == prefix || index(path, prefix "/") == 1
}

function domain_for(path) {
  if (starts(path, "/v1") || starts(path, "/v1beta") || starts(path, "/pg")) return "relay"
  if (path ~ /^\/:mode\/mj/ || starts(path, "/mj") || starts(path, "/suno") || starts(path, "/kling") || starts(path, "/jimeng")) return "media-task"
  if (starts(path, "/api/user")) return "identity-user"
  if (starts(path, "/api/oauth") || starts(path, "/api/passkey")) return "identity-auth"
  if (starts(path, "/api/token")) return "api-token"
  if (starts(path, "/api/channel")) return "channel"
  if (starts(path, "/api/subscription") || path ~ /^\/api\/(stripe|creem|waffo|epay)/ || starts(path, "/dashboard/billing")) return "billing"
  if (starts(path, "/api/deployments")) return "deployment"
  if (starts(path, "/api/log") || starts(path, "/api/data") || starts(path, "/api/perf-metrics")) return "usage-audit"
  if (starts(path, "/api/option") || starts(path, "/api/system") || starts(path, "/api/setup")) return "system-config"
  return "control-plane"
}

function auth_for(path, domain) {
  if (path ~ /^\/api\/(status|notice|about|home_page_content|uptime\/status|user-agreement|privacy-policy|setup)$/) return "public"
  if (path ~ /^\/api\/(stripe|creem|waffo|epay).*\/webhook/ || path ~ /\/notify(\/|$)/) return "webhook"
  # These legacy registrations deliberately bypass the broad domain defaults
  # below. Keep this table aligned with check-route-plan.sh and the root
  # acceptance inventory: it records middleware ownership, not handler names.
  if (path == "/api/mj/self" || path == "/api/models" || path == "/api/task/self") return "user"
  if (starts(path, "/api/ratio_sync")) return "root"
  if (path == "/api/ratio_config" || path == "/api/user/groups") return "public"
  if (path == "/api/usage/token/" || path == "/dashboard/billing/subscription" || path == "/dashboard/billing/usage") return "token"
  if (path == "/api/user/topup/complete") return "admin"
  if (path == "/pg/chat/completions") return "user"
  if (domain == "api-token") return "user"
  if (domain == "relay") return "token"
  if (path ~ /^\/api\/user\/(login|register|reset|auth\/refresh|auth\/logout|passkey\/login|epay\/notify)/) return "public"
  if (path ~ /^\/api\/(oauth|verification|reset_password)/) return "public-or-user"
  if (path ~ /^\/api\/(status\/test|option|system|setup\/migrate)/) return "admin"
  if (path ~ /^\/api\/user/ || path ~ /^\/api\/(subscription\/self|token\/self)/) return "user"
  if (domain == "media-task") return "user-or-token"
  if (path ~ /^\/api\/(pricing|rankings|perf-metrics)/) return "public-or-user"
  return "admin"
}

function streaming_for(path, domain) {
  if (path == "/v1/realtime") return "websocket"
  if (domain == "relay" && path !~ /\/models(\/:model)?$/) return "sse-optional"
  return "none"
}

function access_for(method, domain, streaming) {
  if (streaming != "none") return "read-write-stream"
  if (domain == "relay") return method == "GET" ? "read" : "read-write"
  return method == "GET" ? "read" : "write"
}

function priority_for(path, domain, auth) {
  if (path == "/api/status" || path == "/v1/models" || path == "/api/user/models" || path == "/api/pricing" || path == "/api/uptime/status") return "P0"
  if (domain == "relay" || domain == "identity-auth" || domain == "api-token") return "P1"
  if (domain == "identity-user" || domain == "billing" || domain == "channel") return "P1"
  if (domain == "system-config" || domain == "deployment") return "P2"
  return "P2"
}

function module_for(domain) {
  gsub(/-/, "_", domain)
  return "lmm_api_rs::routes::" domain
}

function job_for(domain, streaming) {
  if (domain == "deployment") return "deployment-runner"
  if (domain == "media-task") return "media-worker"
  if (domain == "relay" || streaming != "none") return "relay-upstream"
  return "none"
}

NF == 3 {
  domain = domain_for($2)
  auth = auth_for($2, domain)
  streaming = streaming_for($2, domain)
  print $1, $2, $3, domain, auth, access_for($1, domain, streaming), streaming, priority_for($2, domain, auth), module_for(domain), job_for(domain, streaming)
}
