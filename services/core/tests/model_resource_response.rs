//! Independent counterexamples for the owned synthetic Chat acceptance client.
//! This checks response evidence, not production model billing or generic SSE support.
#[path = "common/model_resource_response.rs"]
mod response;
use response::valid_complete_body;
use serde_json::{Value, json};

const REQUEST_ID: &str = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";
const DONE: &str = "data: [DONE]\n\n";

fn usage() -> Value {
    json!({"prompt_tokens":10,"completion_tokens":20,"total_tokens":30,
        "prompt_tokens_details":{"cached_tokens":2}})
}
fn envelope(choices: Value) -> Value {
    json!({"id":format!("chatcmpl_{REQUEST_ID}"),"object":"chat.completion.chunk",
        "created":1700000000,"model":"local-model","choices":choices})
}
fn start() -> Value {
    envelope(json!([{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]))
}
fn content(text: &str) -> Value {
    envelope(json!([{"index":0,"delta":{"content":text},"finish_reason":null}]))
}
fn finish() -> Value {
    envelope(json!([{"index":0,"delta":{},"finish_reason":"stop"}]))
}
fn final_usage() -> Value {
    let mut v = envelope(json!([]));
    v["usage"] = usage();
    v
}
fn values() -> Vec<Value> {
    vec![
        start(),
        content("local stream answer"),
        finish(),
        final_usage(),
    ]
}
fn frame(v: &Value) -> String {
    format!("data: {}\n\n", serde_json::to_string(v).unwrap())
}
fn stream(values: &[Value]) -> Vec<u8> {
    (values.iter().map(frame).collect::<String>() + DONE).into_bytes()
}
fn valid_json() -> Value {
    json!({"id":format!("chatcmpl_{REQUEST_ID}"),"object":"chat.completion",
        "created":1700000000,"model":"local-model",
        "choices":[{"index":0,"message":{"role":"assistant","content":"local synthetic answer"},"finish_reason":"stop"}],
        "usage":usage()})
}
fn reject_sse(label: &str, bytes: &[u8]) {
    assert!(
        !valid_complete_body(bytes, true, REQUEST_ID),
        "accepted {label}"
    );
}
fn reject_json(label: &str, v: &Value) {
    assert!(
        !valid_complete_body(&serde_json::to_vec(v).unwrap(), false, REQUEST_ID),
        "accepted {label}"
    );
}

#[test]
fn actual_core_chat_shapes_complete() {
    assert!(valid_complete_body(&stream(&values()), true, REQUEST_ID));
    assert!(valid_complete_body(
        &serde_json::to_vec(&valid_json()).unwrap(),
        false,
        REQUEST_ID
    ));
}
#[test]
fn legal_content_fragments_and_json_escapes_are_decoded() {
    let parts = vec![
        start(),
        content("local "),
        content("stream "),
        content("answer"),
        finish(),
        final_usage(),
    ];
    assert!(valid_complete_body(&stream(&parts), true, REQUEST_ID));
    let escaped = String::from_utf8(stream(&values()))
        .unwrap()
        .replace("local stream answer", r"\u006cocal stream answer");
    assert!(valid_complete_body(escaped.as_bytes(), true, REQUEST_ID));
}
#[test]
fn legal_crlf_and_multiline_data_preserve_json_frames() {
    let crlf = String::from_utf8(stream(&values()))
        .unwrap()
        .replace('\n', "\r\n");
    assert!(valid_complete_body(crlf.as_bytes(), true, REQUEST_ID));
    let mut multiline = String::new();
    for v in values() {
        for line in serde_json::to_string_pretty(&v).unwrap().lines() {
            multiline.push_str("data: ");
            multiline.push_str(line);
            multiline.push('\n');
        }
        multiline.push('\n');
    }
    multiline.push_str(DONE);
    assert!(valid_complete_body(multiline.as_bytes(), true, REQUEST_ID));
}
#[test]
fn metadata_string_is_not_answer_content() {
    let decoy = json!({"note":"local stream answer","usage":usage()});
    reject_sse(
        "legacy minimal decoy without choices or finish",
        &stream(&[decoy]),
    );
    let mut wrong = values();
    wrong[1]["choices"][0]["delta"]["content"] = json!("wrong answer");
    wrong[1]["note"] = json!("local stream answer");
    reject_sse("expected substring in unrelated metadata", &stream(&wrong));
}
#[test]
fn every_error_frame_and_malformed_json_rejects_entire_response() {
    let good = String::from_utf8(stream(&values())).unwrap();
    for bad in [
        "data: {invalid json\n\n",
        "data: []\n\n",
        "data: null\n\n",
        "data: {\"error\":{\"code\":\"failed\"}}\n\n",
    ] {
        reject_sse(
            "bad frame before otherwise valid complete response",
            format!("{bad}{good}").as_bytes(),
        );
    }
    let mut embedded = values();
    embedded[1]["error"] = json!({"code":"failed"});
    reject_sse("error coexists with genuine content", &stream(&embedded));
}
#[test]
fn usage_is_unique_exact_and_after_finish() {
    let mut cases = Vec::new();
    let mut duplicate = values();
    duplicate.push(final_usage());
    cases.push(("duplicate usage", duplicate));
    let mut early = values();
    early.swap(2, 3);
    cases.push(("usage before finish", early));
    let mut missing = values();
    missing.pop();
    cases.push(("missing usage", missing));
    for (key, value) in [
        ("prompt_tokens", json!(11)),
        ("completion_tokens", json!(19)),
        ("total_tokens", json!(29)),
        ("prompt_tokens", json!("10")),
    ] {
        let mut bad = values();
        bad[3]["usage"][key] = value;
        cases.push(("wrong or untyped usage", bad));
    }
    let mut cached = values();
    cached[3]["usage"]["prompt_tokens_details"]["cached_tokens"] = json!(3);
    cases.push(("wrong cached tokens", cached));
    let mut mixed = values();
    mixed[3]["choices"] = json!([{"index":0,"delta":{"content":""},"finish_reason":null}]);
    cases.push(("usage frame has choices", mixed));
    for (label, v) in cases {
        reject_sse(label, &stream(&v));
    }
}
#[test]
fn finish_is_unique_stop_and_precedes_usage() {
    let mut missing = values();
    missing.remove(2);
    reject_sse("missing finish", &stream(&missing));
    let mut duplicate = values();
    duplicate.insert(3, finish());
    reject_sse("duplicate finish", &stream(&duplicate));
    for reason in [json!("length"), json!("tool_calls"), json!(null)] {
        let mut wrong = values();
        wrong[2]["choices"][0]["finish_reason"] = reason;
        reject_sse("non-stop finish", &stream(&wrong));
    }
    let mut late = values();
    late.insert(3, content(""));
    reject_sse("delta after finish", &stream(&late));
}
#[test]
fn done_is_unique_last_and_required() {
    let complete = String::from_utf8(stream(&values())).unwrap();
    reject_sse(
        "missing DONE",
        complete.strip_suffix(DONE).unwrap().as_bytes(),
    );
    reject_sse("duplicate DONE", format!("{complete}{DONE}").as_bytes());
    reject_sse(
        "DONE before complete response",
        format!("{DONE}{complete}").as_bytes(),
    );
    reject_sse(
        "JSON after DONE",
        format!("{complete}{}", frame(&content("extra"))).as_bytes(),
    );
    reject_sse(
        "data after DONE followed by another DONE",
        format!("{complete}{}{DONE}", frame(&content("extra"))).as_bytes(),
    );
    reject_sse(
        "incomplete trailing frame",
        format!("{complete}data: {{").as_bytes(),
    );
}
#[test]
fn choices_and_delta_types_are_not_optional() {
    let mut cases = Vec::new();
    for choices in [
        json!([]),
        json!(null),
        json!([{"index":1,"delta":{"content":"local stream answer"},"finish_reason":null}]),
        json!([{"index":0,"delta":{"content":"local stream answer"},"finish_reason":null},{"index":1,"delta":{},"finish_reason":null}]),
    ] {
        let mut v = values();
        v[1]["choices"] = choices;
        cases.push(v);
    }
    for delta in [
        json!(null),
        json!({"content":123}),
        json!({"tool_calls":[],"content":"local stream answer"}),
    ] {
        let mut v = values();
        v[1]["choices"][0]["delta"] = delta;
        cases.push(v);
    }
    for v in cases {
        reject_sse("invalid choice or delta", &stream(&v));
    }
    let mut role = values();
    role[0]["choices"][0]["delta"]["role"] = json!("user");
    reject_sse("wrong initial role", &stream(&role));
}
#[test]
fn response_metadata_binds_to_header_and_fixture() {
    for (key, wrong) in [
        ("id", json!("synthetic-stream")),
        ("object", json!("chat.completion")),
        ("model", json!("provider-model")),
    ] {
        let mut v = values();
        v[1][key] = wrong;
        reject_sse("changed stream identity or fixture metadata", &stream(&v));
    }
    let other_id = "1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";
    assert!(!valid_complete_body(&stream(&values()), true, other_id));
    assert!(!valid_complete_body(
        &serde_json::to_vec(&valid_json()).unwrap(),
        false,
        other_id
    ));
}
#[test]
fn invalid_utf8_and_incomplete_json_return_false() {
    for bytes in [vec![0xff], b"{".to_vec(), Vec::new()] {
        assert!(!valid_complete_body(&bytes, true, REQUEST_ID));
        assert!(!valid_complete_body(&bytes, false, REQUEST_ID));
    }
}
#[test]
fn json_errors_metadata_finish_and_choice_structure_reject() {
    let mut cases = Vec::new();
    let mut error = valid_json();
    error["error"] = json!({"code":"failed"});
    cases.push(error);
    for (key, wrong) in [
        ("id", json!("synthetic-completion")),
        ("object", json!("chat.completion.chunk")),
        ("model", json!("provider-model")),
    ] {
        let mut v = valid_json();
        v[key] = wrong;
        cases.push(v);
    }
    for choices in [
        json!([]),
        json!(null),
        json!([{"index":1,"message":{"role":"assistant","content":"local synthetic answer"},"finish_reason":"stop"}]),
    ] {
        let mut v = valid_json();
        v["choices"] = choices;
        cases.push(v);
    }
    for (key, wrong) in [
        ("role", json!("user")),
        ("content", json!(123)),
        ("content", json!("wrong answer")),
    ] {
        let mut v = valid_json();
        v["choices"][0]["message"][key] = wrong;
        cases.push(v);
    }
    let mut no_finish = valid_json();
    no_finish["choices"][0]
        .as_object_mut()
        .unwrap()
        .remove("finish_reason");
    cases.push(no_finish);
    let mut truncated = valid_json();
    truncated["choices"][0]["finish_reason"] = json!("length");
    cases.push(truncated);
    let mut no_usage = valid_json();
    no_usage.as_object_mut().unwrap().remove("usage");
    cases.push(no_usage);
    let mut wrong_usage = valid_json();
    wrong_usage["usage"]["total_tokens"] = json!(31);
    cases.push(wrong_usage);
    for v in cases {
        reject_json("invalid JSON complete response", &v);
    }
}
