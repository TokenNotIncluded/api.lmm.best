use super::*;
use serde_json::json;

fn options(entries: &[(&str, Value)]) -> BTreeMap<String, String> {
    entries
        .iter()
        .map(|(key, value)| ((*key).to_owned(), value.to_string()))
        .collect()
}

fn price(entries: &[(&str, Value)]) -> Price {
    Price::from_options(
        &options(entries),
        "test-model",
        "default",
        "default",
        &json!({}),
    )
    .unwrap()
}

fn event(tracker: &mut UsageTracker, value: Value) {
    let wire = format!("data: {value}\n\n");
    // Exercise framing across arbitrary UTF-8 and JSON boundaries.
    for fragment in wire.as_bytes().chunks(3) {
        tracker.feed(fragment);
    }
}

#[test]
fn prices_provider_tokens_and_cache_instead_of_one_per_request() {
    let price = price(&[
        ("ModelRatio", json!({"test-model": 1.25})),
        ("CompletionRatio", json!({"test-model": 4})),
        ("CacheRatio", json!({"test-model": 0.1})),
        ("CreateCacheRatio", json!({"test-model": 1.25})),
        ("ImageRatio", json!({"test-model": 2})),
        ("GroupRatio", json!({"default": 0.8})),
    ]);
    assert_eq!(price.reservation, 500);
    let evidence = Evidence {
        usage: Usage {
            input: 100,
            output: 25,
            cached: 40,
            cache_write: 20,
            image: 10,
        },
        ..Default::default()
    };
    // ((100 - 40 - 20 - 10) + 40 * .1 + 20 * 1.25 + 10 * 2 + 25 * 4) * 1.25 * .8
    assert_eq!(price.quota(&evidence).unwrap(), 179);
}

#[test]
fn cache_prefix_overlap_cannot_create_a_negative_debit() {
    let price = price(&[
        ("ModelRatio", json!({"test-model": 1})),
        ("CacheRatio", json!({"test-model": 0.5})),
    ]);
    let evidence = Evidence {
        usage: Usage {
            input: 10,
            cached: 20,
            cache_write: 20,
            ..Default::default()
        },
        ..Default::default()
    };
    assert_eq!(price.quota(&evidence).unwrap(), 35);
}

#[test]
fn cache_creation_uses_go_cached_creation_field_and_maximum_alias_count() {
    let usage = Usage::parse(
        &json!({"prompt_tokens":100,"completion_tokens":0,"prompt_tokens_details":{"cached_creation_tokens":40,"cache_write_tokens":30}}),
    );
    assert_eq!(usage.cache_write, 40);
    let price = price(&[("ModelRatio", json!({"test-model":1}))]);
    assert_eq!(
        price
            .quota(&Evidence {
                usage,
                ..Default::default()
            })
            .unwrap(),
        110
    );
    let usage = Usage::parse(
        &json!({"input_tokens":100,"input_tokens_details":{"cached_creation_tokens":40,"cache_write_tokens":50}}),
    );
    assert_eq!(usage.cache_write, 50);
}

#[test]
fn special_group_price_is_frozen_and_fixed_price_uses_quota_units() {
    let mut options = options(&[
        ("ModelPrice", json!({"test-model": 0.004})),
        ("GroupRatio", json!({"default": 1})),
        ("GroupGroupRatio", json!({"vip": {"default": 0.5}})),
    ]);
    let price = Price::from_options(&options, "test-model", "vip", "default", &json!({})).unwrap();
    options.insert(
        "ModelPrice".to_owned(),
        json!({"test-model": 20}).to_string(),
    );
    assert_eq!(price.reservation, 1000);
    assert_eq!(
        price
            .quota(&Evidence {
                completed: true,
                ..Default::default()
            })
            .unwrap(),
        1000
    );
}

#[test]
fn trust_discount_applies_to_preconsumption_and_final_usage() {
    let price = Price::from_options_with_discount(
        &options(&[
            ("ModelRatio", json!({"test-model": 2})),
            ("GroupRatio", json!({"default": 0.8})),
        ]),
        "test-model",
        "default",
        "default",
        &json!({}),
        0.97,
    )
    .unwrap();
    assert_eq!(price.reservation, 776);
    assert_eq!(
        price
            .quota(&Evidence {
                usage: Usage {
                    input: 100,
                    ..Default::default()
                },
                ..Default::default()
            })
            .unwrap(),
        155
    );
}

#[test]
fn measured_usage_settles_even_on_failure_but_empty_failure_refunds() {
    let price = price(&[
        ("ModelRatio", json!({"test-model": 2})),
        ("CompletionRatio", json!({"test-model": 3})),
    ]);
    assert_eq!(price.quota(&Evidence::default()).unwrap(), 0);
    assert_eq!(
        price
            .quota(&Evidence {
                completed: true,
                ..Default::default()
            })
            .unwrap(),
        1000
    );
    assert_eq!(
        price
            .quota(&Evidence {
                usage: Usage {
                    input: 20,
                    output: 5,
                    ..Default::default()
                },
                ..Default::default()
            })
            .unwrap(),
        70
    );
}

#[test]
fn prices_round_half_away_from_zero_and_keep_free_models_free() {
    let half = price(&[("ModelRatio", json!({"test-model": 0.5}))]);
    let evidence = Evidence {
        usage: Usage {
            input: 5,
            ..Default::default()
        },
        ..Default::default()
    };
    assert_eq!(half.quota(&evidence).unwrap(), 3);
    let free = price(&[("ModelPrice", json!({"test-model": 0}))]);
    assert_eq!(free.quota(&evidence).unwrap(), 0);
    let ratio_free = price(&[("ModelRatio", json!({"test-model": 0}))]);
    assert_eq!(ratio_free.quota(&evidence).unwrap(), 0);
}

#[test]
fn positive_prices_reserve_at_least_one_and_fixed_free_price_overrides_ratio() {
    let tiny = price(&[("ModelRatio", json!({"test-model":0.0001}))]);
    assert_eq!(tiny.reservation, 1);
    assert_eq!(tiny.with_prompt_tokens(1).unwrap().reservation, 1);
    let free = price(&[
        ("ModelRatio", json!({"test-model":10})),
        ("ModelPrice", json!({"test-model":0})),
        ("quota_setting.enable_free_model_pre_consume", json!(false)),
    ]);
    assert_eq!(free.reservation, 0);
    assert_eq!(
        free.quota(&Evidence {
            usage: Usage {
                input: 100,
                output: 20,
                ..Default::default()
            },
            ..Default::default()
        })
        .unwrap(),
        0
    );
}

#[test]
fn unknown_or_invalid_or_unsupported_prices_never_fall_back_to_fixed_one() {
    for options in [
        options(&[]),
        options(&[("ModelRatio", json!({"test-model": -1}))]),
        options(&[("ModelRatio", json!({"test-model": 1e25}))]),
        options(&[
            ("ModelRatio", json!({"test-model": 1})),
            (
                "billing_setting.billing_mode",
                json!({"test-model": "tiered_expr"}),
            ),
        ]),
    ] {
        assert!(
            Price::from_options(&options, "test-model", "default", "default", &json!({})).is_err()
        );
    }
}

#[test]
fn absent_options_use_go_defaults_but_explicit_maps_replace_defaults() {
    let configured =
        Price::from_options(&BTreeMap::new(), "gpt-4o", "default", "default", &json!({})).unwrap();
    assert_eq!(configured.reservation, 625);
    assert_eq!(
        configured
            .quota(&Evidence {
                usage: Usage {
                    input: 100,
                    output: 10,
                    cached: 50,
                    ..Default::default()
                },
                ..Default::default()
            })
            .unwrap(),
        144
    );
    let options = options(&[("ModelRatio", json!({})), ("ModelPrice", json!({}))]);
    assert!(Price::from_options(&options, "gpt-4o", "default", "default", &json!({})).is_err());
}

#[test]
fn completion_ratio_locks_follow_current_go_order() {
    for (model, expected) in [
        ("gpt-5", 8),
        ("gpt-5.4", 6),
        ("gpt-5.6-sol", 20),
        ("gpt-4o", 20),
        ("gpt-3.5-turbo", 20),
    ] {
        let price = Price::from_options(
            &options(&[
                ("ModelRatio", json!({model: 1})),
                ("CompletionRatio", json!({model: 20})),
            ]),
            model,
            "default",
            "default",
            &json!({}),
        )
        .unwrap();
        assert_eq!(
            price
                .quota(&Evidence {
                    usage: Usage {
                        output: 1,
                        ..Default::default()
                    },
                    ..Default::default()
                })
                .unwrap(),
            expected,
            "{model}"
        );
    }
}

#[test]
fn lifecycle_placeholders_cannot_erase_measured_usage() {
    let mut tracker = UsageTracker::new(OpenAiRelayEndpoint::Responses);
    event(
        &mut tracker,
        json!({"type": "response.created", "response": {"usage": {"input_tokens": 10, "output_tokens": 2}}}),
    );
    event(
        &mut tracker,
        json!({"type": "response.in_progress", "response": {"usage": {"input_tokens": 0, "output_tokens": 0}}}),
    );
    event(
        &mut tracker,
        json!({"type": "response.failed", "response": {"status": "failed"}}),
    );
    assert_eq!(
        tracker.evidence.usage,
        Usage {
            input: 10,
            output: 2,
            ..Default::default()
        }
    );
    assert!(tracker.evidence.terminal);
    assert!(!tracker.evidence.completed);
}

#[test]
fn interrupted_output_is_counted_locally_with_bounded_prompt_estimate() {
    let mut tracker = UsageTracker::new(OpenAiRelayEndpoint::Responses)
        .with_input("gpt-4o".to_owned(), 2_000_000);
    event(
        &mut tracker,
        json!({"type":"response.created","response":{"usage":{"input_tokens":0,"output_tokens":0}}}),
    );
    event(
        &mut tracker,
        json!({"type":"response.output_text.delta","delta":"Hello, "}),
    );
    event(
        &mut tracker,
        json!({"type":"response.output_text.delta","delta":"world!"}),
    );
    tracker.eof(false);
    tracker.finalize();
    assert_eq!(tracker.evidence.usage.input, 1_050_000);
    assert_eq!(tracker.evidence.usage.output, 4);
    assert!(!tracker.evidence.completed);
}

#[test]
fn explicit_terminal_usage_prevents_local_output_from_overwriting_it() {
    let mut tracker =
        UsageTracker::new(OpenAiRelayEndpoint::Responses).with_input("gpt-4o".to_owned(), 100);
    event(
        &mut tracker,
        json!({"type":"response.output_text.delta","delta":"Hello, world!"}),
    );
    event(
        &mut tracker,
        json!({"type":"response.failed","response":{"usage":{"input_tokens":0,"output_tokens":0}}}),
    );
    tracker.finalize();
    assert_eq!(tracker.evidence.usage, Usage::default());
}

#[test]
fn terminal_explicit_zero_is_authoritative() {
    let mut tracker = UsageTracker::new(OpenAiRelayEndpoint::Responses);
    event(
        &mut tracker,
        json!({"type": "response.created", "response": {"usage": {"input_tokens": 10, "output_tokens": 2}}}),
    );
    event(
        &mut tracker,
        json!({"type": "response.failed", "response": {"status": "failed", "usage": {"input_tokens": 0, "output_tokens": 0}}}),
    );
    assert_eq!(tracker.evidence.usage, Usage::default());
    assert!(tracker.evidence.reported);
    assert_eq!(Price::fixed(99).quota(&tracker.evidence).unwrap(), 0);
}

#[test]
fn response_done_with_failed_status_is_not_a_success() {
    for status in ["failed", "incomplete", "cancelled", "canceled"] {
        let mut tracker = UsageTracker::new(OpenAiRelayEndpoint::Responses);
        event(
            &mut tracker,
            json!({"type": "response.done", "response": {"status": status}}),
        );
        assert!(tracker.evidence.terminal);
        assert!(!tracker.evidence.completed);
    }
}

#[test]
fn incomplete_frames_and_bare_done_do_not_complete_responses() {
    for body in [
        "data: [DONE]\n\n",
        "data: {\"type\":\"response.completed\"}",
        "data: malformed\n\n",
    ] {
        let mut tracker = UsageTracker::new(OpenAiRelayEndpoint::Responses);
        tracker.feed(body.as_bytes());
        tracker.eof(true);
        assert!(!tracker.evidence.completed);
    }
}

#[test]
fn chat_usage_after_finish_reason_is_consumed_until_done() {
    let mut tracker = UsageTracker::new(OpenAiRelayEndpoint::ChatCompletions);
    event(
        &mut tracker,
        json!({"choices": [{"delta": {"content": "hello"}, "finish_reason": "stop"}]}),
    );
    assert!(!tracker.evidence.terminal);
    event(
        &mut tracker,
        json!({"choices": [], "usage": {"prompt_tokens": 20, "completion_tokens": 5, "prompt_tokens_details": {"cached_tokens": 7}}}),
    );
    tracker.feed(b"data: [DONE]\n\n");
    assert!(tracker.evidence.completed);
    assert_eq!(
        tracker.evidence.usage,
        Usage {
            input: 20,
            output: 5,
            cached: 7,
            ..Default::default()
        }
    );
}

#[test]
fn malformed_tail_after_terminal_cannot_erase_usage() {
    let mut tracker = UsageTracker::new(OpenAiRelayEndpoint::Responses);
    tracker.feed(b"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":30,\"output_tokens\":4}}}\n\ndata: not-json\n\n");
    assert!(tracker.evidence.completed);
    assert_eq!(tracker.evidence.usage.input, 30);
}

#[test]
fn invalid_json_or_error_envelope_cannot_generate_success_accounting() {
    for wire in [
        b"{} garbage".as_slice(),
        b"null",
        b"{\"error\":{\"message\":\"failed\"}}",
    ] {
        let mut tracker = UsageTracker::new(OpenAiRelayEndpoint::Responses);
        assert!(tracker.json(wire).is_err());
        assert!(!tracker.evidence.completed);
    }
}

#[test]
fn chat_json_missing_usage_counts_each_choice_and_preserves_extra_response_fields() {
    let mut tracker =
        UsageTracker::new(OpenAiRelayEndpoint::ChatCompletions).with_input("gpt-4o".to_owned(), 17);
    let body = json!({"choices":[{"message":{"content":[{"type":"text","text":"Hello, "},{"type":"text","text":"world!"}],"reasoning_content":""}}],"provider_extra":{"kept":true}});
    let rewritten = tracker.json(body.to_string().as_bytes()).unwrap().unwrap();
    let rewritten: Value = serde_json::from_slice(&rewritten).unwrap();
    assert_eq!(tracker.evidence.usage.input, 17);
    assert_eq!(tracker.evidence.usage.output, 4);
    assert_eq!(rewritten["usage"]["prompt_tokens"], 17);
    assert_eq!(rewritten["usage"]["completion_tokens"], 4);
    assert_eq!(rewritten["usage"]["total_tokens"], 21);
    assert_eq!(rewritten["provider_extra"], json!({"kept":true}));
}

#[test]
fn chat_json_missing_prompt_preserves_nonzero_completion_and_prefers_chat_counters() {
    let mut tracker =
        UsageTracker::new(OpenAiRelayEndpoint::ChatCompletions).with_input("gpt-4o".to_owned(), 17);
    let rewritten = tracker
        .json(br#"{"usage":{"prompt_tokens":0,"completion_tokens":5}}"#)
        .unwrap()
        .unwrap();
    assert_eq!(tracker.evidence.usage.input, 17);
    assert_eq!(tracker.evidence.usage.output, 5);
    let value: Value = serde_json::from_slice(&rewritten).unwrap();
    // Go includes zero input_tokens/output_tokens alongside measured Chat
    // fields; they must not replace the prompt/completion totals on re-entry.
    assert_eq!(
        Usage::parse(&value["usage"]),
        Usage {
            input: 17,
            output: 5,
            ..Default::default()
        }
    );
}
