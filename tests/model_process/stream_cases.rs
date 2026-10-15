//! Independent acceptance for real lmm-core HTTP/SSE on an owned PostgreSQL fixture.
//! No production/provider credentials are used; inputs are bound by the owned runner.
//! The parent harness owns configuration, process identity and the restricted DSN.
use super::*;
use serde_json::json;
use sqlx::PgPool;
use std::{sync::atomic::Ordering, time::Duration};

fn request_id(response: &reqwest::Response) -> String {
    let value = response
        .headers()
        .get("x-request-id")
        .expect("real Core response must identify the durable request")
        .to_str()
        .unwrap()
        .to_owned();
    assert_eq!(value.len(), 64);
    assert!(value.bytes().all(|byte| byte.is_ascii_hexdigit()));
    value
}

fn provider_auth_is_private(provider: &Provider, expected_hits: usize) {
    let auth = provider.auth.lock().unwrap();
    assert_eq!(auth.len(), expected_hits);
    assert!(
        auth.iter()
            .all(|value| value == "Bearer synthetic-upstream-key")
    );
}

async fn open_hold(pool: &PgPool, id: &str, expected_state: Option<&str>) {
    let charge: (String, i64, i64) =
        sqlx::query_as("SELECT state,reserved,settled FROM core_charging.requests WHERE id=$1")
            .bind(id)
            .fetch_one(pool)
            .await
            .unwrap();
    assert_eq!((charge.1, charge.2), (140, 0));
    if let Some(state) = expected_state {
        assert_eq!(charge.0, state);
    }
    let holds: Vec<(i64, i64, bool)> = sqlx::query_as(
        "SELECT reserved,captured,closed FROM core_charging.ledger_holds WHERE request_id=$1",
    )
    .bind(id)
    .fetch_all(pool)
    .await
    .unwrap();
    assert_eq!(holds, vec![(140, 0, false)]);
}

async fn captures(pool: &PgPool) -> i64 {
    sqlx::query_scalar("SELECT count(*) FROM core_billing.ledger_journals WHERE kind='capture'")
        .fetch_one(pool)
        .await
        .unwrap()
}

async fn journals(pool: &PgPool) -> i64 {
    sqlx::query_scalar("SELECT count(*) FROM core_billing.ledger_journals")
        .fetch_one(pool)
        .await
        .unwrap()
}

#[sqlx::test(migrations = false)]
async fn independent_process_sse_paid_and_partial_disconnect_stays_reserved(pool: PgPool) {
    let seed = seed(&pool, 1000).await;
    let provider = Provider::start(true).await;
    let mut host = start_core(&pool, &provider, &[seed.key_id], "stream-disconnect").await;
    let mut paid_ids = Vec::new();
    for (path, key) in [
        ("/v1/chat/completions", "independent-stream-chat"),
        ("/v1/responses", "independent-stream-responses"),
    ] {
        let response = http(&host, Some(&seed.key), path, "hello", key, true).await;
        assert_eq!(response.status(), reqwest::StatusCode::OK);
        let content_type = response.headers()["content-type"].to_str().unwrap();
        assert!(
            content_type
                .split(';')
                .next()
                .unwrap()
                .trim()
                .eq_ignore_ascii_case("text/event-stream"),
            "response MIME essence must be exactly text/event-stream"
        );
        assert_eq!(response.headers()["x-accel-buffering"], "no");
        let id = request_id(&response);
        let bytes = response.bytes().await.unwrap();
        let text = String::from_utf8(bytes.to_vec()).expect("SSE bytes must be valid UTF-8");
        assert!(text.contains("local stream answer"));
        if path.ends_with("responses") {
            assert!(text.contains("response.completed"));
        } else {
            assert!(text.ends_with("data: [DONE]\n\n"));
            assert!(text.contains("\"completion_tokens\":20"));
        }
        paid(&pool, seed.account, &id).await;
        let checkpoints: Vec<(i64, i64, serde_json::Value, bool)> = sqlx::query_as(
            "SELECT sequence,packet_bytes,provider_usage,terminal_observed \
             FROM core_model.output_checkpoints WHERE request_id=$1 ORDER BY sequence",
        )
        .bind(&id)
        .fetch_all(&pool)
        .await
        .unwrap();
        assert!(checkpoints.len() >= 4);
        for (index, row) in checkpoints.iter().enumerate() {
            assert_eq!(row.0, index as i64 + 1);
        }
        assert!(
            checkpoints
                .iter()
                .any(|row| row.1 == 0 && row.2["output_tokens"] == 20)
        );
        assert!(checkpoints.last().unwrap().3);
        paid_ids.push(id);
    }
    balance(&seed.ledger, seed.account, 902, 0).await;
    assert_eq!(captures(&pool).await, 2);
    assert_eq!(provider.hits.load(Ordering::SeqCst), 2);
    provider_auth_is_private(&provider, 2);
    assert_eq!(provider.stream_closed.load(Ordering::SeqCst), 2);
    balanced(&pool).await;

    // Unlike a Router::oneshot body drop, this closes an actual Core TCP client.
    // The provider remains gated, so no usage or terminal frame can be inferred.
    let partial_started = tokio::time::Instant::now();
    let mut response = http(
        &host,
        Some(&seed.key),
        "/v1/chat/completions",
        "stream-pause",
        "independent-partial-disconnect",
        true,
    )
    .await;
    assert_eq!(response.status(), reqwest::StatusCode::OK);
    let interrupted_id = request_id(&response);
    let first = tokio::time::timeout(Duration::from_secs(5), response.chunk())
        .await
        .expect("Core must deliver its first real SSE chunk")
        .unwrap()
        .expect("SSE body ended before the first chunk");
    assert!(!first.is_empty());
    assert!(!String::from_utf8_lossy(&first).contains("[DONE]"));
    let checkpoint: (i64, i64, i64) = sqlx::query_as(
        "SELECT count(*),COALESCE(sum(packet_bytes),0)::bigint, \
         count(*) FILTER (WHERE terminal_observed) FROM core_model.output_checkpoints WHERE request_id=$1",
    )
    .bind(&interrupted_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert!(
        checkpoint.0 >= 1,
        "first emitted bytes need committed evidence"
    );
    assert!(checkpoint.1 >= i64::try_from(first.len()).unwrap());
    assert_eq!(checkpoint.2, 0);
    assert_eq!(provider.stream_closed.load(Ordering::SeqCst), 2);
    drop(response);
    // Observe cancellation while Core is still alive, before its configured
    // 3-second total timeout. SIGTERM and natural timeout cannot satisfy this.
    tokio::time::timeout_at(partial_started + Duration::from_secs(2), async {
        while provider.stream_closed.load(Ordering::SeqCst) < 3 {
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
    })
    .await
    .expect("client disconnect must close the upstream before total timeout or Core stop");
    assert_eq!(provider.stream_closed.load(Ordering::SeqCst), 3);
    assert!(
        tokio::time::Instant::now() < partial_started + Duration::from_secs(2),
        "upstream close must be observed before Core's natural timeout",
    );
    assert!(
        host.child.as_mut().unwrap().try_wait().unwrap().is_none(),
        "Core must remain alive when client disconnect closes the upstream",
    );
    let upstream_closed_ms = partial_started.elapsed().as_millis();
    open_hold(&pool, &interrupted_id, None).await;
    balance(&seed.ledger, seed.account, 762, 140).await;
    let before_stop_terminal: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_model.output_checkpoints WHERE request_id=$1 AND terminal_observed",
    )
    .bind(&interrupted_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(before_stop_terminal, 0);
    host.stop().await;
    // Release only the synthetic upstream writer after Core has stopped. This
    // cannot manufacture a terminal observation in the stopped Core database.
    provider.stream_gate.add_permits(1);
    tokio::time::sleep(Duration::from_secs(21)).await;
    host.restart().await;
    open_hold(&pool, &interrupted_id, Some("reconcile")).await;
    balance(&seed.ledger, seed.account, 762, 140).await;
    let terminal: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_model.output_checkpoints WHERE request_id=$1 AND terminal_observed",
    )
    .bind(&interrupted_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(terminal, 0);
    let replay = http(
        &host,
        Some(&seed.key),
        "/v1/chat/completions",
        "stream-pause",
        "independent-partial-disconnect",
        true,
    )
    .await;
    assert_eq!(replay.status(), reqwest::StatusCode::CONFLICT);
    assert_eq!(provider.hits.load(Ordering::SeqCst), 3);
    provider_auth_is_private(&provider, 3);
    assert_eq!(captures(&pool).await, 2);
    balanced(&pool).await;
    host.finish().await;
    record(
        "stream-disconnect-result",
        &json!({
            "result":"PASS", "paid_request_ids":paid_ids, "partial_request_id":interrupted_id,
            "first_chunk_bytes":first.len(), "checkpoint_rows_before_disconnect":checkpoint.0,
            "provider_dispatches":3, "captures":2, "available":762, "reserved":140,
            "partial_terminal_observed":false, "expired_state":"reconcile",
            "upstream_closed_before_core_stop_and_total_timeout":true,
            "upstream_close_observed_ms":upstream_closed_ms, "core_alive_at_upstream_close":true,
            "restart":"actual Core child; natural lease expiry", "upstream_auth_checked":true
        }),
    );
}

#[sqlx::test(migrations = false)]
async fn independent_process_missing_usage_preserves_hold_through_natural_expiry(pool: PgPool) {
    let seed = seed(&pool, 1000).await;
    let provider = Provider::start(false).await;
    let mut host = start_core(&pool, &provider, &[seed.key_id], "missing-usage").await;
    let response = http(
        &host,
        Some(&seed.key),
        "/v1/chat/completions",
        "hello",
        "independent-missing-usage",
        false,
    )
    .await;
    assert_eq!(response.status(), reqwest::StatusCode::SERVICE_UNAVAILABLE);
    let id = request_id(&response);
    let body: serde_json::Value = response.json().await.unwrap();
    assert_eq!(body["error"]["code"], "settlement_pending");
    assert!(body.get("choices").is_none());
    open_hold(&pool, &id, None).await;
    balance(&seed.ledger, seed.account, 860, 140).await;
    assert_eq!(captures(&pool).await, 0);
    let before_journals = journals(&pool).await;
    assert_eq!(provider.hits.load(Ordering::SeqCst), 1);
    provider_auth_is_private(&provider, 1);
    host.restart().await;
    open_hold(&pool, &id, None).await;
    balance(&seed.ledger, seed.account, 860, 140).await;
    assert_eq!(provider.hits.load(Ordering::SeqCst), 1);
    tokio::time::sleep(Duration::from_secs(21)).await;
    host.restart().await;
    open_hold(&pool, &id, Some("reconcile")).await;
    balance(&seed.ledger, seed.account, 860, 140).await;
    let replay = http(
        &host,
        Some(&seed.key),
        "/v1/chat/completions",
        "hello",
        "independent-missing-usage",
        false,
    )
    .await;
    assert_eq!(replay.status(), reqwest::StatusCode::CONFLICT);
    assert_eq!(provider.hits.load(Ordering::SeqCst), 1);
    assert_eq!(captures(&pool).await, 0);
    assert_eq!(journals(&pool).await, before_journals);
    balanced(&pool).await;
    host.finish().await;
    record(
        "missing-usage-result",
        &json!({
            "result":"PASS", "request_id":id, "provider_dispatches":1, "captures":0,
            "journals_unchanged":true, "available":860, "reserved":140,
            "state":"reconcile", "restarts":2, "expiry":"natural; no lease SQL mutation"
        }),
    );
}

#[sqlx::test(migrations = false)]
async fn independent_process_terminal_evidence_recovers_final_transaction_once(pool: PgPool) {
    let seed = seed(&pool, 1000).await;
    let provider = Provider::start(true).await;
    let mut host = start_core(&pool, &provider, &[seed.key_id], "terminal-recovery").await;
    // Inject only after the real Core has validated the accepted schema.
    // This owner-only trigger exists solely in the newly allocated SQLx database.
    // It rejects final usage, not the earlier append-only output checkpoints.
    sqlx::raw_sql(
        "CREATE FUNCTION public.reject_independent_stream_final() RETURNS trigger LANGUAGE plpgsql \
         AS $$ BEGIN IF NEW.event_id='final' THEN RAISE EXCEPTION 'synthetic independent final commit failure'; \
         END IF; RETURN NEW; END $$; \
         CREATE TRIGGER reject_independent_stream_final BEFORE INSERT ON core_charging.usage_events \
         FOR EACH ROW EXECUTE FUNCTION public.reject_independent_stream_final()",
    )
    .execute(&pool)
    .await
    .unwrap();
    let response = http(
        &host,
        Some(&seed.key),
        "/v1/chat/completions",
        "hello",
        "independent-terminal-recovery",
        true,
    )
    .await;
    assert_eq!(response.status(), reqwest::StatusCode::OK);
    let id = request_id(&response);
    let text = response.text().await.unwrap();
    assert!(text.contains("local stream answer"));
    assert!(text.contains("settlement_pending"));
    assert!(!text.contains("[DONE]"));
    open_hold(&pool, &id, None).await;
    balance(&seed.ledger, seed.account, 860, 140).await;
    assert_eq!(captures(&pool).await, 0);
    let terminal_usage: Vec<serde_json::Value> = sqlx::query_scalar(
        "SELECT provider_usage FROM core_model.output_checkpoints \
         WHERE request_id=$1 AND terminal_observed ORDER BY sequence",
    )
    .bind(&id)
    .fetch_all(&pool)
    .await
    .unwrap();
    assert!(!terminal_usage.is_empty());
    for usage in &terminal_usage {
        assert_eq!(usage["input_tokens"], 10);
        assert_eq!(usage["cached_input_tokens"], 2);
        assert_eq!(usage["output_tokens"], 20);
    }
    let checkpoint_count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_model.output_checkpoints WHERE request_id=$1",
    )
    .bind(&id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(provider.hits.load(Ordering::SeqCst), 1);
    provider_auth_is_private(&provider, 1);
    balanced(&pool).await;
    host.stop().await;
    // Restore the original schema before restarting: runtime schema validation
    // must see the accepted schema, never an exemption for this fault fixture.
    sqlx::raw_sql(
        "DROP TRIGGER reject_independent_stream_final ON core_charging.usage_events; \
         DROP FUNCTION public.reject_independent_stream_final()",
    )
    .execute(&pool)
    .await
    .unwrap();
    tokio::time::sleep(Duration::from_secs(21)).await;
    host.restart().await;
    paid(&pool, seed.account, &id).await;
    balance(&seed.ledger, seed.account, 951, 0).await;
    assert_eq!(captures(&pool).await, 1);
    assert_eq!(provider.hits.load(Ordering::SeqCst), 1);
    let final_events: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_charging.usage_events WHERE request_id=$1 AND event_id='final'",
    )
    .bind(&id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(final_events, 1);
    let settled_journals = journals(&pool).await;
    host.restart().await;
    let replay = http(
        &host,
        Some(&seed.key),
        "/v1/chat/completions",
        "hello",
        "independent-terminal-recovery",
        true,
    )
    .await;
    assert_eq!(replay.status(), reqwest::StatusCode::CONFLICT);
    paid(&pool, seed.account, &id).await;
    balance(&seed.ledger, seed.account, 951, 0).await;
    assert_eq!(captures(&pool).await, 1);
    assert_eq!(journals(&pool).await, settled_journals);
    assert_eq!(provider.hits.load(Ordering::SeqCst), 1);
    let after_checkpoints: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_model.output_checkpoints WHERE request_id=$1",
    )
    .bind(&id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(after_checkpoints, checkpoint_count);
    balanced(&pool).await;
    host.finish().await;
    record(
        "terminal-recovery-result",
        &json!({
            "result":"PASS", "request_id":id, "provider_dispatches":1,
            "terminal_checkpoints":terminal_usage.len(), "checkpoint_rows":checkpoint_count,
            "checkpoint_rows_unchanged":true, "captures":1, "final_usage_events":1,
            "available":951, "reserved":0, "restarts":2,
            "failure":"isolated owner trigger rejects final usage; schema restored before restart",
            "expiry":"natural; no lease SQL mutation"
        }),
    );
}
