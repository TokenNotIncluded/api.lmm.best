use super::*;

// Literal wire budget is intentionally independent of the implementation constant.
const GO_CHAT_LIMIT: usize = 64 << 10;

#[tokio::test]
async fn chat_rate_limit_precedes_declared_body_limit() -> TestResult {
    let calls = Arc::new(Mutex::new(Vec::new()));
    let app = fixture_router_with_user_rate_limiter(
        FixtureStore::default(),
        Arc::new(FixtureUserRateLimiter {
            outcome: Ok(CriticalRateLimitOutcome::Rejected {
                retry_after_seconds: 23,
            }),
            calls: Arc::clone(&calls),
        }),
    )?;
    let response = app
        .oneshot(build_request(
            Request::post("/api/assistant/chat")
                .header(header::AUTHORIZATION, "Bearer browser-session")
                .header(header::CONTENT_LENGTH, GO_CHAT_LIMIT + 1),
            Body::empty(),
            "rate limited oversized request",
        )?)
        .await?;
    assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
    assert_eq!(response.headers()[header::RETRY_AFTER], "23");
    assert_eq!(*lock_recover(&calls), vec![("assistant".to_owned(), 7)]);
    Ok(())
}

#[tokio::test]
async fn declared_overflow_rejects_without_polling_body_and_preserves_auth() -> TestResult {
    let app = fixture_router(FixtureStore::default())?;
    for (credential, status) in [
        ("browser-session", StatusCode::PAYLOAD_TOO_LARGE),
        ("invalid", StatusCode::UNAUTHORIZED),
    ] {
        let stream = futures_util::stream::poll_fn(
            |_| -> std::task::Poll<Option<Result<axum::body::Bytes, io::Error>>> {
                panic!("declared oversized or unauthorized requests must not poll the body")
            },
        );
        let response = app
            .clone()
            .oneshot(build_request(
                Request::post("/api/assistant/chat")
                    .header(header::AUTHORIZATION, format!("Bearer {credential}"))
                    .header(header::CONTENT_LENGTH, GO_CHAT_LIMIT + 1),
                Body::from_stream(stream),
                "unread oversized request",
            )?)
            .await?;
        assert_eq!(response.status(), status);
    }
    Ok(())
}

#[tokio::test]
async fn streamed_overflow_stops_reading_and_read_errors_stay_bad_requests() -> TestResult {
    use std::sync::atomic::{AtomicUsize, Ordering};
    let polls = Arc::new(AtomicUsize::new(0));
    let count = Arc::clone(&polls);
    let stream = futures_util::stream::poll_fn(move |_| {
        let index = count.fetch_add(1, Ordering::SeqCst);
        assert!(
            index < 2,
            "must stop as soon as the byte budget is exceeded"
        );
        std::task::Poll::Ready(Some(Ok::<_, io::Error>(axum::body::Bytes::from(
            vec![b' '; GO_CHAT_LIMIT],
        ))))
    });
    let result = assistant_chat_input(build_request(
        Request::post("/api/assistant/chat"),
        Body::from_stream(stream),
        "bounded stream",
    )?)
    .await;
    assert_eq!(
        result
            .err()
            .ok_or_else(|| test_error("accepted oversized stream"))?
            .status(),
        StatusCode::PAYLOAD_TOO_LARGE
    );
    assert_eq!(polls.load(Ordering::SeqCst), 2);
    let body = Body::from_stream(futures_util::stream::once(async {
        Err::<axum::body::Bytes, _>(io::Error::other("broken transport"))
    }));
    let response = assistant_chat_input(build_request(
        Request::post("/api/assistant/chat"),
        body,
        "broken stream",
    )?)
    .await
    .err()
    .ok_or_else(|| test_error("accepted broken stream"))?;
    assert_eq!(response.status(), StatusCode::BAD_REQUEST);
    assert_eq!(
        response_json(response).await?["code"],
        "ASSISTANT_INVALID_REQUEST"
    );
    Ok(())
}

#[tokio::test]
async fn chat_byte_budget_accepts_maximum_conversation_and_tool_metadata() -> TestResult {
    // 12,000 four-byte Unicode scalars plus a 16 KiB tool-argument string.
    let mut messages: Vec<Value> = (0..12)
        .map(|index| {
            json!({
                "role": if index == 0 || index == 11 { "user" } else { "assistant" },
                "content": "🙂".repeat(match index { 0 | 1 => 4000, 2 => 3991, _ => 1 }),
            })
        })
        .collect();
    let arguments = format!("{{\"q\":\"{}\"}}", "x".repeat(16 * 1024 - 8));
    assert_eq!(arguments.len(), 16 * 1024);
    messages[1]["tool_calls"] = json!([{"id":"call","type":"function","function":{"name":"fixture","arguments":arguments}}]);
    let payload = serde_json::to_vec(&json!({"messages":messages}))?;
    assert!(payload.len() < GO_CHAT_LIMIT);
    let input = assistant_chat_input(build_request(
        Request::post("/api/assistant/chat"),
        Body::from(payload),
        "maximum envelope",
    )?)
    .await
    .map_err(|_| test_error("maximum conversation envelope rejected"))?;
    assert!(normalize_assistant_conversation(input).is_ok());
    Ok(())
}

#[tokio::test]
async fn chat_body_limit_matches_go_declared_and_streamed_boundaries() -> TestResult {
    let app = fixture_router(FixtureStore {
        cached_response: Some(AssistantCachedResponse {
            status: StatusCode::OK,
            body: br#"{"fixture":"accepted"}"#.to_vec(),
        }),
        ..FixtureStore::default()
    })?;
    for declared in [false, true] {
        for size in [GO_CHAT_LIMIT - 1, GO_CHAT_LIMIT, GO_CHAT_LIMIT + 1] {
            let mut payload = br#"{"message":"hello"}"#.to_vec();
            payload.resize(size, b' ');
            let mut builder = Request::post("/api/assistant/chat")
                .header(header::AUTHORIZATION, "Bearer browser-session");
            if declared {
                builder = builder.header(header::CONTENT_LENGTH, size);
            }
            let body = Body::from_stream(futures_util::stream::iter(
                payload
                    .chunks(1024)
                    .map(|chunk| Ok::<_, io::Error>(axum::body::Bytes::copy_from_slice(chunk)))
                    .collect::<Vec<_>>(),
            ));
            let response = app
                .clone()
                .oneshot(build_request(builder, body, "body boundary")?)
                .await?;
            if size <= GO_CHAT_LIMIT {
                assert_eq!(
                    response.status(),
                    StatusCode::OK,
                    "valid JSON at {size}, declared={declared}"
                );
                assert_eq!(
                    response_json(response).await?,
                    json!({"fixture":"accepted"})
                );
            } else {
                assert_eq!(response.status(), StatusCode::PAYLOAD_TOO_LARGE);
                let bytes = to_bytes(response.into_body(), 4096).await?;
                if declared {
                    assert!(
                        bytes.is_empty(),
                        "Go rejects declared length with an empty 413"
                    );
                } else {
                    let body: Value = serde_json::from_slice(&bytes)?;
                    assert_eq!(
                        body,
                        json!({"success":false,"code":"ASSISTANT_REQUEST_TOO_LARGE","message":"request body too large","retryable":false})
                    );
                }
            }
        }
    }
    Ok(())
}
