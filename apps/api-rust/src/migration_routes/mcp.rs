//! Streamable HTTP MCP endpoints for open-source bounties and drawing.


use axum::{
    Router,
    body::Bytes,
    extract::{Request, State},
    http::{HeaderMap, StatusCode, header},
    response::{IntoResponse, Response},
    routing::connect,
};
use serde_json::json;
use sha2::{Digest, Sha256};
use sqlx::{PgPool, Row};

const MCP_TOKEN_PREFIX: &str = "lmm_mcp_";
const MCP_PROTOCOL_VERSION: &str = "2026-07-28";

#[derive(Clone)]
pub struct McpHttpState {
    pg: PgPool,
    valkey: redis::Client,
    dependency_timeout: std::time::Duration,
}

impl McpHttpState {
    #[must_use]
    pub fn new(pg: PgPool, valkey: redis::Client, dependency_timeout: std::time::Duration) -> Self {
        Self {
            pg,
            valkey,
            dependency_timeout,
        }
    }
}

/// Mounts `/mcp` and `/mcp/drawing` with Go-compatible Any-method handlers.
pub fn mcp_router(state: McpHttpState) -> Router {
    Router::new()
        .route(
            "/mcp",
            connect(open_source_bounty_mcp)
                .trace(open_source_bounty_mcp)
                .get(open_source_bounty_mcp)
                .post(open_source_bounty_mcp)
                .put(open_source_bounty_mcp)
                .delete(open_source_bounty_mcp)
                .patch(open_source_bounty_mcp)
                .head(open_source_bounty_mcp)
                .options(open_source_bounty_mcp),
        )
        .route(
            "/mcp/",
            connect(open_source_bounty_mcp)
                .trace(open_source_bounty_mcp)
                .get(open_source_bounty_mcp)
                .post(open_source_bounty_mcp)
                .put(open_source_bounty_mcp)
                .delete(open_source_bounty_mcp)
                .patch(open_source_bounty_mcp)
                .head(open_source_bounty_mcp)
                .options(open_source_bounty_mcp),
        )
        .route(
            "/mcp/drawing",
            connect(drawing_mcp)
                .trace(drawing_mcp)
                .get(drawing_mcp)
                .post(drawing_mcp)
                .put(drawing_mcp)
                .delete(drawing_mcp)
                .patch(drawing_mcp)
                .head(drawing_mcp)
                .options(drawing_mcp),
        )
        .route(
            "/mcp/drawing/",
            connect(drawing_mcp)
                .trace(drawing_mcp)
                .get(drawing_mcp)
                .post(drawing_mcp)
                .put(drawing_mcp)
                .delete(drawing_mcp)
                .patch(drawing_mcp)
                .head(drawing_mcp)
                .options(drawing_mcp),
        )
        .with_state(state)
}

async fn open_source_bounty_mcp(State(state): State<McpHttpState>, request: Request) -> Response {
    handle_mcp(&state, request, "/mcp").await
}

async fn drawing_mcp(State(state): State<McpHttpState>, request: Request) -> Response {
    handle_mcp(&state, request, "/mcp/drawing").await
}

async fn handle_mcp(state: &McpHttpState, request: Request, endpoint: &str) -> Response {
    let (parts, body) = request.into_parts();
    let headers = parts.headers;
    let method = parts.method;
    if method == axum::http::Method::OPTIONS {
        return preflight_response();
    }
    let token = match bearer_token(&headers) {
        Some(token) => token,
        None => return unauthorized_mcp(),
    };
    let user_id = match verify_mcp_token(&state.pg, &token).await {
        Ok(user_id) => user_id,
        Err(response) => return response,
    };
    let body = match axum::body::to_bytes(body, 1 << 20).await {
        Ok(body) => body,
        Err(_) => return invalid_request("request body is too large"),
    };
    if is_initialize(&body) {
        return json_response(StatusCode::OK, initialize_result(endpoint));
    }
    let _ = user_id;
    service_unavailable(endpoint)
}

fn bearer_token(headers: &HeaderMap) -> Option<String> {
    let value = headers.get(header::AUTHORIZATION)?.to_str().ok()?.trim();
    let token = value.strip_prefix("Bearer ")?.trim();
    if token.is_empty() {
        None
    } else {
        Some(token.to_owned())
    }
}

async fn verify_mcp_token(pg: &PgPool, raw_token: &str) -> Result<i64, Response> {
    if !raw_token.starts_with(MCP_TOKEN_PREFIX) || raw_token.len() < MCP_TOKEN_PREFIX.len() + 32 {
        return Err(unauthorized_mcp());
    }
    let token_hash = hex::encode(Sha256::digest(raw_token.as_bytes()));
    let row = sqlx::query(
        "SELECT token.user_id, token.id, token.last_used_at \
         FROM open_source_bounty_mcp_tokens AS token \
         JOIN users AS token_user ON token_user.id = token.user_id \
           AND token_user.deleted_at IS NULL AND token_user.status = 1 \
           AND token_user.auth_version = token.user_auth_version \
         WHERE token.token_hash = $1",
    )
    .bind(token_hash)
    .fetch_optional(pg)
    .await
    .map_err(|_| internal_mcp())?;
    let Some(row) = row else {
        return Err(unauthorized_mcp());
    };
    let user_id: i64 = row.try_get("user_id").map_err(|_| internal_mcp())?;
    let developer_access: bool = sqlx::query_scalar(
        "SELECT EXISTS (SELECT 1 FROM developer_access_requests WHERE user_id = $1 AND status = 'approved')",
    )
    .bind(user_id)
    .fetch_one(pg)
    .await
    .map_err(|_| internal_mcp())?;
    if !developer_access {
        return Err(unauthorized_mcp());
    }
    let token_id: i64 = row.try_get("id").map_err(|_| internal_mcp())?;
    let last_used_at: i64 = row.try_get("last_used_at").unwrap_or_default();
    let now = unix_seconds();
    if last_used_at < now - 60 {
        let _ = sqlx::query(
            "UPDATE open_source_bounty_mcp_tokens SET last_used_at = $2 WHERE id = $1 AND last_used_at < $3",
        )
        .bind(token_id)
        .bind(now)
        .bind(now - 60)
        .execute(pg)
        .await;
    }
    Ok(user_id)
}

fn is_initialize(body: &Bytes) -> bool {
    serde_json::from_slice::<serde_json::Value>(body)
        .ok()
        .and_then(|value| value.get("method").and_then(|method| method.as_str()).map(str::to_owned))
        .is_some_and(|method| method == "initialize")
}

fn initialize_result(endpoint: &str) -> serde_json::Value {
    json!({
        "jsonrpc": "2.0",
        "result": {
            "protocolVersion": MCP_PROTOCOL_VERSION,
            "capabilities": {},
            "serverInfo": {
                "name": if endpoint == "/mcp/drawing" {
                    "api.lmm.best-drawing"
                } else {
                    "api.lmm.best-open-source-bounties"
                },
                "title": "api.lmm.best MCP",
            },
            "instructions": format!("Connected to {endpoint}. Tool execution is not configured on this listener."),
        }
    })
}

fn service_unavailable(endpoint: &str) -> Response {
    json_response(
        StatusCode::OK,
        json!({
            "jsonrpc": "2.0",
            "error": {
                "code": -32000,
                "message": format!("MCP tool execution is not configured on this listener ({endpoint})"),
            }
        }),
    )
}

fn unauthorized_mcp() -> Response {
    (
        StatusCode::UNAUTHORIZED,
        [(header::WWW_AUTHENTICATE, "Bearer")],
        axum::Json(json!({
            "jsonrpc": "2.0",
            "error": {
                "code": -32001,
                "message": "invalid personal MCP token",
            }
        })),
    )
        .into_response()
}

fn invalid_request(message: &str) -> Response {
    json_response(
        StatusCode::BAD_REQUEST,
        json!({
            "jsonrpc": "2.0",
            "error": {
                "code": -32600,
                "message": message,
            }
        }),
    )
}

fn internal_mcp() -> Response {
    json_response(
        StatusCode::INTERNAL_SERVER_ERROR,
        json!({
            "jsonrpc": "2.0",
            "error": {
                "code": -32603,
                "message": "internal MCP error",
            }
        }),
    )
}

fn preflight_response() -> Response {
    (
        StatusCode::NO_CONTENT,
        [
            (header::ACCESS_CONTROL_ALLOW_METHODS, "GET, POST, PUT, PATCH, DELETE, OPTIONS"),
            (header::ACCESS_CONTROL_ALLOW_HEADERS, "Authorization, Content-Type, MCP-Protocol-Version"),
        ],
    )
        .into_response()
}

fn json_response(status: StatusCode, body: serde_json::Value) -> Response {
    (
        status,
        [(header::CONTENT_TYPE, "application/json; charset=utf-8")],
        axum::Json(body),
    )
        .into_response()
}

fn unix_seconds() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs() as i64
}
