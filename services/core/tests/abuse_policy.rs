use lmm_core::abuse::{PolicyConfig, UpstreamProtocol};
use serde_json::{Value, json};
use std::collections::BTreeSet;

fn config() -> Value {
    json!({"version":1,"policies":[{"id":"trap","name":"Synthetic isolation","mode":"fixed_text","fixed_text":"configured response","public_models":["public-model"],"clients":{},"limits":{"requests_per_minute_per_user":10,"requests_per_minute_global":20,"max_concurrent_per_user":1,"max_concurrent_global":2,"max_input_bytes":65536,"max_output_bytes":65536,"max_output_tokens":1000,"output_bytes_per_second":0,"total_timeout_seconds":10}}]})
}
#[test]
fn policy_validation_rejects_unknown_modes_protocols_unloaded_secrets_and_external_destinations() {
    let aliases = BTreeSet::from(["isolated".to_owned()]);
    let valid: PolicyConfig = serde_json::from_value(config()).unwrap();
    valid.validate(&aliases).unwrap();
    let mut unknown = config();
    unknown["policies"][0]["mode"] = json!("random_errors");
    assert!(serde_json::from_value::<PolicyConfig>(unknown).is_err());
    let mut secret = config();
    secret["policies"][0]["api_key"] = json!("not-accepted");
    assert!(serde_json::from_value::<PolicyConfig>(secret).is_err());
    let mut duplicate = config();
    duplicate["policies"][0]["public_models"] = json!(["same", "same"]);
    assert!(
        serde_json::from_value::<PolicyConfig>(duplicate)
            .unwrap()
            .validate(&aliases)
            .is_err()
    );
    for protocol in [
        "openai_chat",
        "openai_responses",
        "anthropic_messages",
        "gemini",
    ] {
        let mut custom = config();
        let p = &mut custom["policies"][0];
        p["mode"] = json!("custom_upstream");
        p.as_object_mut().unwrap().remove("fixed_text");
        let target = json!({"base_url":"http://127.0.0.1:49152/v1","upstream_protocol":protocol,"model":"private-upstream-model","credential_reference":"isolated"});
        p["clients"] = json!({"openai_chat":target,"openai_responses":target});
        let c: PolicyConfig = serde_json::from_value(custom.clone()).unwrap();
        c.validate(&aliases).unwrap();
        assert!(c.validate(&BTreeSet::new()).is_err());
        for forbidden in [
            "https://example.invalid/v1",
            "http://localhost/v1",
            "http://127.0.0.1/v1?token=x",
            "http://127.0.0.1/v1#x",
            "http://user:secret@127.0.0.1/v1",
            "http://10.0.0.1/v1",
            "http://127.0.0.1\\evil/v1",
        ] {
            let mut bad = custom.clone();
            bad["policies"][0]["clients"]["openai_chat"]["base_url"] = json!(forbidden);
            assert!(
                serde_json::from_value::<PolicyConfig>(bad)
                    .unwrap()
                    .validate(&aliases)
                    .is_err(),
                "accepted forbidden endpoint"
            );
        }
    }
    assert!(serde_json::from_value::<UpstreamProtocol>(json!("unknown")).is_err());
}
#[test]
fn resource_bounds_and_fixed_text_cannot_exceed_the_administered_output_budget() {
    for (field, value) in [
        ("requests_per_minute_per_user", 0),
        ("max_concurrent_global", 0),
        ("max_input_bytes", 1_048_577),
        ("max_output_tokens", 65_537),
        ("total_timeout_seconds", 241),
        ("output_bytes_per_second", 1_048_577),
    ] {
        let mut bad = config();
        bad["policies"][0]["limits"][field] = json!(value);
        assert!(
            serde_json::from_value::<PolicyConfig>(bad)
                .unwrap()
                .validate(&BTreeSet::new())
                .is_err()
        );
    }
    let mut bad = config();
    bad["policies"][0]["limits"]["max_output_bytes"] = json!(1);
    assert!(
        serde_json::from_value::<PolicyConfig>(bad)
            .unwrap()
            .validate(&BTreeSet::new())
            .is_err()
    );
    let mut bad = config();
    bad["policies"][0]["limits"]["max_concurrent_per_user"] = json!(3);
    assert!(
        serde_json::from_value::<PolicyConfig>(bad)
            .unwrap()
            .validate(&BTreeSet::new())
            .is_err()
    );
}

#[test]
fn policy_receipt_hash_matches_canonical_utf8_cross_language_vector() {
    let c:PolicyConfig=serde_json::from_str(r#"{"version":1,"policies":[{"id":"unicode-policy","name":"中文 é","mode":"fixed_text","fixed_text":"固定文本 é","public_models":["模型一","model-2"],"clients":{},"limits":{"requests_per_minute_per_user":1,"requests_per_minute_global":2,"max_concurrent_per_user":1,"max_concurrent_global":2,"max_input_bytes":128,"max_output_bytes":128,"max_output_tokens":16,"output_bytes_per_second":0,"total_timeout_seconds":10}}]}"#).unwrap();
    assert_eq!(
        lmm_core::abuse::config_hash(&c).unwrap(),
        "28830aa7a6a096a0d62028706ec2c0d6bbec9b4c537440f350fc3419da5024ce"
    );
}
