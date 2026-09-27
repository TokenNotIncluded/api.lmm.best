use super::super::{OpenAiRelayEndpoint, billing::UsageTracker};
use super::*;

#[test]
fn current_go_handlers_and_tool_price_vectors_match() {
    let vectors: Vec<Value> = serde_json::from_str(include_str!(concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/tests/behavior-oracle/fixtures/relay-tool-billing.json"
    )))
    .unwrap();
    assert_eq!(vectors.len(), 11);
    for vector in vectors {
        let model = vector["model"].as_str().unwrap();
        let prices = ToolPrices::configured(vector["prices"].as_str());
        let mut tracker = UsageTracker::new(OpenAiRelayEndpoint::Responses)
            .with_input(model.to_owned(), 0)
            .with_request(vector["request"].as_str().unwrap().as_bytes());
        let wire = vector["wire"].as_str().unwrap().as_bytes();
        if vector["stream"].as_bool().unwrap() {
            for chunk in wire.chunks(7) {
                tracker.feed(chunk);
            }
            tracker.eof(true);
        } else {
            let _ = tracker.json(wire).unwrap();
        }
        tracker.finalize();
        assert_eq!(
            json!(prices.log_items(&tracker.evidence.tools, model)),
            vector["items"],
            "{}",
            vector["name"]
        );
    }
}

#[test]
fn longest_prefix_explicit_zero_and_invalid_legacy_entries_match_go() {
    let prices = ToolPrices::configured(Some(
        r#"{"web_search_preview":9,"web_search_preview:gpt-4o*":30,"web_search_preview:gpt-4o-mini*":0,"file_search":-1,"custom":"7"}"#,
    ));
    assert_eq!(
        prices.price("web_search_preview", "gpt-4o"),
        Decimal::from(30)
    );
    assert_eq!(
        prices.price("web_search_preview", "gpt-4o-mini-2024"),
        Decimal::ZERO
    );
    assert_eq!(
        prices.price("web_search_preview", "gpt-4.1"),
        Decimal::from(25)
    );
    assert_eq!(
        prices.price("web_search_preview", "custom"),
        Decimal::from(9)
    );
    assert_eq!(prices.price("file_search", "model"), Decimal::new(25, 1));
    assert_eq!(prices.price("custom", "model"), Decimal::ZERO);
}

#[test]
fn declarations_do_not_bill_and_web_search_declaration_selects_its_price_name() {
    let mut calls = ToolCalls::from_request(
        br#"{"tools":[{"type":"web_search"},{"type":"image_generation"}]}"#,
    );
    assert!(calls.final_counts("gpt-4o", true).is_empty());
    calls.observe(
        &json!({"type":"response.output_item.done","item":{"type":"web_search_call"}}),
        true,
        true,
    );
    assert_eq!(calls.counts.get("web_search"), Some(&1));
    let mut both = ToolCalls::from_request(
        br#"{"tools":[{"type":"web_search"},{"type":"web_search_preview"}]}"#,
    );
    both.observe(
        &json!({"type":"response.output_item.done","item":{"type":"web_search_call"}}),
        true,
        true,
    );
    assert_eq!(both.counts.get("web_search_preview"), Some(&1));
}

#[test]
fn repeated_chat_tool_fragments_count_once_and_reserved_function_names_do_not_bill() {
    let mut calls = ToolCalls::default();
    let event = json!({"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"priced_function","arguments":"{"}},{"index":1,"function":{"name":"web_search"}}]}}]});
    calls.observe(&event, false, true);
    calls.observe(&event, false, true);
    assert_eq!(
        calls.counts,
        BTreeMap::from([("priced_function".to_owned(), 1)])
    );
}

#[test]
fn image_terminal_deduplicates_done_and_final_output_and_caps_128() {
    let mut calls = ToolCalls::default();
    let image =
        json!({"type":"image_generation_call","id":"img-1","result":"pixels","status":"completed"});
    calls.observe(
        &json!({"type":"response.output_item.done","output_index":0,"item":image}),
        true,
        true,
    );
    calls.observe(
        &json!({"type":"response.completed","response":{"output":[image]}}),
        true,
        true,
    );
    assert_eq!(calls.counts.get("image_generation"), Some(&1));
    let mut many = ToolCalls::default();
    let output: Vec<Value> = (0..140)
        .map(|i| json!({"type":"image_generation_call","result":format!("pixels-{i}")}))
        .collect();
    many.observe(&json!({"output":output}), true, false);
    assert_eq!(many.counts.get("image_generation"), Some(&128));
}

#[test]
fn failed_images_are_discarded_without_discarding_completed_non_image_tools() {
    let mut calls = ToolCalls::default();
    calls.observe(&json!({"type":"response.output_item.done","item":{"type":"image_generation_call","result":"pixels"}}),true,true);
    calls.observe(
        &json!({"type":"response.output_item.done","item":{"type":"file_search_call"}}),
        true,
        true,
    );
    calls.observe(
        &json!({"type":"response.failed","response":{"status":"failed"}}),
        true,
        true,
    );
    assert_eq!(calls.counts.get("image_generation"), Some(&0));
    assert_eq!(calls.counts.get("file_search"), Some(&1));
}

#[test]
fn tool_only_calls_charge_per_thousand_calls_then_group_and_quota_unit() {
    let prices = ToolPrices::configured(None);
    let calls = BTreeMap::from([("web_search".to_owned(), 2), ("file_search".to_owned(), 1)]);
    assert_eq!(
        prices.surcharge(&calls, "gpt-4o", Decimal::new(5, 1), Decimal::from(500000)),
        Decimal::from(5625)
    );
    assert_eq!(
        prices.log_items(&calls, "gpt-4o"),
        vec![
            json!({"name":"file_search","count":1,"price":2.5}),
            json!({"name":"web_search","count":2,"price":10})
        ]
    );
}
