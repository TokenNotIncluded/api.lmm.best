use super::*;
use serde_json::json;
const BIG: i64 = 9_007_199_254_740_993;
fn user(id: i64) -> Value {
    json!({"user_id":id,"username":"synthetic","email":"","active":true,"platform_role":"user","revision":0,"marked":false,"policy_id":null})
}
fn request() -> Value {
    json!({"operation_id":"wire-unit-operation","reason":"Synthetic contract","action":"mark","policy_id":"trap","expected_policy_revision":0,"targets":[{"user_id":BIG.to_string(),"expected_revision":0},{"user_id":"7","expected_revision":0}]})
}
fn preview_value() -> Value {
    json!({"version":1,"preview_id":"a".repeat(64),"preview_hash":"b".repeat(64),"expires_at":1,"operation_id":"wire-unit-operation","action":"mark","policy_id":"trap","policy_revision":0,"targets":[{"user_id":BIG,"username":"synthetic","active":true,"expected_revision":0,"before":{"marked":false,"policy_id":null},"after":{"marked":true,"policy_id":"trap","active":true}}]})
}
fn receipt() -> Value {
    json!({"version":1,"status":"applied","operation_id":"wire-unit-operation","kind":"users","policy_revision":0,"users":[user(BIG)]})
}
fn audit_value() -> Value {
    json!({"version":1,"events":[{"id":BIG,"actor_user_id":i64::MAX,"operation_id":"wire-unit-operation","kind":"users","reason":"Keep arbitrary opaque text user_id=9007199254740993","created_at":1,"detail":{"action":"mark","policy_revision":0,"transitions":[{"user_id":BIG,"before_revision":0,"after_revision":1,"before_marked":false,"after_marked":true,"before_active":true,"after_active":true,"policy_id":"trap"}]}}],"next_after":BIG})
}
#[test]
fn positive_ids_are_canonical_bounded_and_never_float() {
    for (s, n) in [
        ("1", 1),
        ("9007199254740993", BIG),
        ("9223372036854775807", i64::MAX),
    ] {
        assert_eq!(positive_id(s).unwrap(), n);
    }
    for s in [
        "",
        "0",
        "00",
        "01",
        "+1",
        "-1",
        " 1",
        "1 ",
        "1.0",
        "1e3",
        "１",
        "١",
        "9223372036854775808",
        "18446744073709551615",
    ] {
        assert!(
            matches!(positive_id(s), Err(AbuseError::Invalid)),
            "noncanonical ID accepted"
        );
    }
}
#[test]
fn v2_target_ids_require_json_strings_and_closed_fields() {
    for id in [
        json!(BIG),
        json!(7),
        Value::Null,
        json!(true),
        json!("0"),
        json!("01"),
    ] {
        let mut v = request();
        v["targets"][0]["user_id"] = id;
        let parsed = serde_json::from_value::<PreviewRequestV2>(v);
        assert!(
            parsed.is_err() || parsed.unwrap().domain().is_err(),
            "invalid wire ID accepted"
        );
    }
    let mut v = request();
    v["targets"][0]["extra"] = json!(true);
    assert!(serde_json::from_value::<PreviewRequestV2>(v).is_err());
    let raw = r#"{"operation_id":"wire-unit-operation","reason":"x","action":"mark","policy_id":"trap","expected_policy_revision":0,"targets":[{"user_id":"7","user_id":"8","expected_revision":0}]}"#;
    assert!(serde_json::from_str::<PreviewRequestV2>(raw).is_err());
}
#[test]
fn normalized_preview_preserves_input_order_options_and_existing_hash_domain() {
    let domain = serde_json::from_value::<PreviewRequestV2>(request())
        .unwrap()
        .domain()
        .unwrap();
    let old = PreviewRequest {
        operation_id: "wire-unit-operation".into(),
        reason: "Synthetic contract".into(),
        action: Action::Mark,
        policy_id: Some("trap".into()),
        expected_policy_revision: 0,
        targets: vec![
            Target {
                user_id: BIG,
                expected_revision: 0,
            },
            Target {
                user_id: 7,
                expected_revision: 0,
            },
        ],
    };
    assert_eq!(
        serde_json::to_vec(&domain).unwrap(),
        serde_json::to_vec(&old).unwrap()
    );
    assert_eq!(
        crate::abuse::hash(&domain).unwrap(),
        crate::abuse::hash(&old).unwrap()
    );
    let mut sorted = domain;
    sorted.targets.sort_by_key(|v| v.user_id);
    assert_ne!(
        crate::abuse::hash(&sorted).unwrap(),
        crate::abuse::hash(&old).unwrap()
    );
    let mut v = request();
    v.as_object_mut().unwrap().remove("policy_id");
    v["action"] = json!("unmark");
    assert!(
        serde_json::from_value::<PreviewRequestV2>(v)
            .unwrap()
            .domain()
            .unwrap()
            .policy_id
            .is_none()
    );
}
#[test]
fn every_public_user_cursor_and_audit_id_is_projected_losslessly() {
    let original = json!({"version":1,"users":[user(BIG),user(i64::MAX)],"next_after":i64::MAX});
    let original_bytes = serde_json::to_vec(&original).unwrap();
    let v = user_list(original.clone()).unwrap();
    assert_eq!(v["version"], 2);
    assert_eq!(v["users"][0]["user_id"], BIG.to_string());
    assert_eq!(v["users"][1]["user_id"], i64::MAX.to_string());
    assert_eq!(v["next_after"], i64::MAX.to_string());
    assert_eq!(serde_json::to_vec(&original).unwrap(), original_bytes);
    let v = detail(user(BIG)).unwrap();
    assert_eq!(v["version"], 2);
    assert_eq!(v["user_id"], BIG.to_string());
    let p = preview(preview_value()).unwrap();
    assert_eq!(p["targets"][0]["user_id"], BIG.to_string());
    assert_eq!(p["preview_hash"], "b".repeat(64));
    let v = operation(receipt()).unwrap();
    assert_eq!(v["version"], 2);
    assert_eq!(v["users"][0]["user_id"], BIG.to_string());
    let original = audit_value();
    let reason = original["events"][0]["reason"].clone();
    let v = audit(original).unwrap();
    assert_eq!(v["events"][0]["id"], BIG.to_string());
    assert_eq!(v["events"][0]["actor_user_id"], i64::MAX.to_string());
    assert_eq!(
        v["events"][0]["detail"]["transitions"][0]["user_id"],
        BIG.to_string()
    );
    assert_eq!(v["next_after"], BIG.to_string());
    assert_eq!(v["events"][0]["reason"], reason);
}
#[test]
fn policies_receipt_and_audit_detail_are_not_upgraded_or_rehashed() {
    let policy = json!({"version":1,"status":"applied","operation_id":"policy-operation","kind":"policies","policy_revision":1,"applied_config_hash":"c".repeat(64),"users":[]});
    let bytes = serde_json::to_vec(&policy).unwrap();
    assert_eq!(
        serde_json::to_vec(&operation(policy).unwrap()).unwrap(),
        bytes
    );
    let d = json!({"before_revision":0,"after_revision":1,"policy_ids":["policy-9007199254740993"],"config_digest":"c".repeat(64)});
    let v = json!({"version":1,"events":[{"id":1,"actor_user_id":BIG,"operation_id":"policy-operation","kind":"policies","reason":"Synthetic policy","detail":d.clone(),"created_at":1}],"next_after":null});
    let v = audit(v).unwrap();
    assert_eq!(v["events"][0]["detail"], d);
    assert_eq!(v["events"][0]["actor_user_id"], BIG.to_string());
}
#[test]
fn malformed_cached_versions_kinds_required_nullables_and_nested_fields_fail_closed() {
    for path in [
        vec!["policy_id"],
        vec!["targets", "0", "before", "policy_id"],
    ] {
        let mut v = preview_value();
        if path.len() == 1 {
            v.as_object_mut().unwrap().remove(path[0]);
        } else {
            v["targets"][0]["before"]
                .as_object_mut()
                .unwrap()
                .remove("policy_id");
        }
        assert!(matches!(preview(v), Err(AbuseError::Unavailable)));
    }
    for bad in [json!(0), json!(2), json!("1"), Value::Null] {
        let mut v = receipt();
        v["version"] = bad;
        assert!(operation(v).is_err());
    }
    let mut v = receipt();
    v["kind"] = json!("other");
    assert!(operation(v).is_err());
    let mut v = receipt();
    v["users"][0]["revision"] = json!(-1);
    assert!(operation(v).is_err());
    let mut v = receipt();
    v["users"][0]["platform_role"] = json!("operator");
    assert!(operation(v).is_err());
    let mut v = preview_value();
    v["targets"][0]["after"]["extra"] = json!(true);
    assert!(preview(v).is_err());
    let mut v = audit_value();
    v["events"][0]["detail"]["transitions"][0]["user_id"] = json!("7");
    assert!(audit(v).is_err());
    let mut v = audit_value();
    v["events"][0]["detail"]["unknown_user_id"] = json!(BIG);
    assert!(audit(v).is_err());
    let v = json!({"version":1,"users":[]});
    assert!(user_list(v).is_err());
    let v = json!({"version":1,"status":"applied","operation_id":"policy-operation","kind":"policies","policy_revision":1,"applied_config_hash":"c".repeat(64),"users":[user(BIG)]});
    assert!(operation(v).is_err());
}
#[test]
fn cursor_and_result_order_are_numeric_and_bound_to_last_row() {
    let v = json!({"version":1,"users":[user(9),user(10)],"next_after":10});
    assert_eq!(user_list(v).unwrap()["next_after"], "10");
    let v = json!({"version":1,"users":[user(10),user(9)],"next_after":9});
    assert!(user_list(v).is_err());
    let v = json!({"version":1,"users":[user(BIG)],"next_after":7});
    assert!(user_list(v).is_err());
    assert!(
        serde_json::from_value::<UserQueryV2>(json!({"after":"0"}))
            .unwrap()
            .domain()
            .is_err()
    );
    assert!(
        serde_json::from_value::<PageV2>(json!({"after":"01"}))
            .unwrap()
            .domain()
            .is_err()
    );
    assert!(
        serde_json::from_value::<PageV2>(json!({}))
            .unwrap()
            .domain()
            .unwrap()
            .after
            .is_none()
    );
}

#[test]
fn signed_legacy_times_remain_exact_while_revisions_keep_domain_bounds() {
    let mut v = audit_value();
    v["events"][0]["created_at"] = json!(-1);
    assert_eq!(audit(v).unwrap()["events"][0]["created_at"], -1);
    let mut v = preview_value();
    v["expires_at"] = json!(i64::MIN);
    assert_eq!(preview(v).unwrap()["expires_at"].as_i64(), Some(i64::MIN));
    let mut v = receipt();
    v["policy_revision"] = json!(-1);
    assert!(operation(v).is_err());
}
#[test]
fn policy_audit_ids_preserve_order_but_enforce_existing_unique_size_bounds() {
    let mut detail = json!({"before_revision":0,"after_revision":1,"policy_ids":["z","a"],"config_digest":"c".repeat(64)});
    let original = detail.clone();
    audit_detail(&mut detail, false).unwrap();
    assert_eq!(detail, original);
    let mut duplicate = original.clone();
    duplicate["policy_ids"] = json!(["a", "a"]);
    assert!(audit_detail(&mut duplicate, false).is_err());
    let mut oversized = original;
    oversized["policy_ids"] = json!((0..33).map(|n| format!("p{n}")).collect::<Vec<_>>());
    assert!(audit_detail(&mut oversized, false).is_err());
}
