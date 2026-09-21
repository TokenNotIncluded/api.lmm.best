use std::sync::Arc;

use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{HeaderMap, Request, StatusCode},
};
use lmm_api_rs::routes::admin_catalog::{
    AdminCatalogActor, AdminCatalogAuthorizer, AdminCatalogState, CatalogError, CatalogOperation,
    MemoryCatalogProvider, router,
};
use serde_json::{Value, json};
use tower::ServiceExt;

#[derive(Clone)]
struct Admin;

#[async_trait]
impl AdminCatalogAuthorizer for Admin {
    async fn authorize(&self, _: &HeaderMap) -> Result<AdminCatalogActor, CatalogError> {
        Ok(AdminCatalogActor {
            user_id: 7,
            role: 10,
        })
    }
}

fn app(provider: Arc<MemoryCatalogProvider>) -> axum::Router {
    router(AdminCatalogState::new(provider, Arc::new(Admin)))
}

async fn invoke(app: &axum::Router, request: Request<Body>) -> (StatusCode, Value) {
    let response = app.clone().oneshot(request).await.expect("route response");
    let status = response.status();
    let body = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    (
        status,
        serde_json::from_slice(&body).expect("legacy JSON envelope"),
    )
}

#[tokio::test]
async fn crud_routes_dispatch_to_the_frozen_operations_with_legacy_success_envelopes() {
    struct Case {
        method: &'static str,
        uri: &'static str,
        body: &'static str,
        operation: CatalogOperation,
        resource_id: Option<i64>,
    }

    let cases = [
        Case {
            method: "GET",
            uri: "/api/models/?p=2",
            body: "",
            operation: CatalogOperation::ListModels,
            resource_id: None,
        },
        Case {
            method: "GET",
            uri: "/api/models/search?keyword=gpt",
            body: "",
            operation: CatalogOperation::SearchModels,
            resource_id: None,
        },
        Case {
            method: "POST",
            uri: "/api/models/",
            body: r#"{"model_name":"gpt"}"#,
            operation: CatalogOperation::CreateModel,
            resource_id: None,
        },
        Case {
            method: "PUT",
            uri: "/api/models/?status_only=true",
            body: r#"{"id":11,"status":0}"#,
            operation: CatalogOperation::UpdateModel,
            resource_id: None,
        },
        Case {
            method: "GET",
            uri: "/api/models/11",
            body: "",
            operation: CatalogOperation::GetModel,
            resource_id: Some(11),
        },
        Case {
            method: "DELETE",
            uri: "/api/models/11",
            body: "",
            operation: CatalogOperation::DeleteModel,
            resource_id: Some(11),
        },
        Case {
            method: "GET",
            uri: "/api/models/missing",
            body: "",
            operation: CatalogOperation::MissingModels,
            resource_id: None,
        },
        Case {
            method: "GET",
            uri: "/api/vendors/",
            body: "",
            operation: CatalogOperation::ListVendors,
            resource_id: None,
        },
        Case {
            method: "GET",
            uri: "/api/vendors/search?keyword=open",
            body: "",
            operation: CatalogOperation::SearchVendors,
            resource_id: None,
        },
        Case {
            method: "POST",
            uri: "/api/vendors/",
            body: r#"{"name":"OpenAI"}"#,
            operation: CatalogOperation::CreateVendor,
            resource_id: None,
        },
        Case {
            method: "PUT",
            uri: "/api/vendors/",
            body: r#"{"id":12,"name":"OpenAI"}"#,
            operation: CatalogOperation::UpdateVendor,
            resource_id: None,
        },
        Case {
            method: "GET",
            uri: "/api/vendors/12",
            body: "",
            operation: CatalogOperation::GetVendor,
            resource_id: Some(12),
        },
        Case {
            method: "DELETE",
            uri: "/api/vendors/12",
            body: "",
            operation: CatalogOperation::DeleteVendor,
            resource_id: Some(12),
        },
        Case {
            method: "GET",
            uri: "/api/prefill_group/?type=model",
            body: "",
            operation: CatalogOperation::ListPrefillGroups,
            resource_id: None,
        },
        Case {
            method: "POST",
            uri: "/api/prefill_group/",
            body: r#"{"name":"Core","type":"model","items":[]}"#,
            operation: CatalogOperation::CreatePrefillGroup,
            resource_id: None,
        },
        Case {
            method: "PUT",
            uri: "/api/prefill_group/",
            body: r#"{"id":13,"name":"Core","type":"model","items":[]}"#,
            operation: CatalogOperation::UpdatePrefillGroup,
            resource_id: None,
        },
        Case {
            method: "DELETE",
            uri: "/api/prefill_group/13",
            body: "",
            operation: CatalogOperation::DeletePrefillGroup,
            resource_id: Some(13),
        },
        Case {
            method: "GET",
            uri: "/api/redemption/?p=3",
            body: "",
            operation: CatalogOperation::ListRedemptions,
            resource_id: None,
        },
        Case {
            method: "GET",
            uri: "/api/redemption/search?status=expired",
            body: "",
            operation: CatalogOperation::SearchRedemptions,
            resource_id: None,
        },
        Case {
            method: "POST",
            uri: "/api/redemption/",
            body: r#"{"name":"trial","count":1}"#,
            operation: CatalogOperation::CreateRedemption,
            resource_id: None,
        },
        Case {
            method: "PUT",
            uri: "/api/redemption/?status_only=1",
            body: r#"{"id":14,"status":0}"#,
            operation: CatalogOperation::UpdateRedemption,
            resource_id: None,
        },
        Case {
            method: "GET",
            uri: "/api/redemption/14",
            body: "",
            operation: CatalogOperation::GetRedemption,
            resource_id: Some(14),
        },
        Case {
            method: "DELETE",
            uri: "/api/redemption/invalid",
            body: "",
            operation: CatalogOperation::DeleteInvalidRedemptions,
            resource_id: None,
        },
        Case {
            method: "DELETE",
            uri: "/api/redemption/14",
            body: "",
            operation: CatalogOperation::DeleteRedemption,
            resource_id: Some(14),
        },
    ];

    for case in cases {
        let provider = Arc::new(MemoryCatalogProvider::new(json!({"fixture": true})));
        let request = Request::builder()
            .method(case.method)
            .uri(case.uri)
            .header("content-type", "application/json")
            .body(Body::from(case.body))
            .expect("request");
        let (status, envelope) = invoke(&app(Arc::clone(&provider)), request).await;

        assert_eq!(status, StatusCode::OK, "{} {}", case.method, case.uri);
        let expected = match case.operation {
            CatalogOperation::MissingModels => json!({
                "success": true,
                "data": {"fixture": true}
            }),
            CatalogOperation::DeleteRedemption => json!({
                "success": true,
                "message": ""
            }),
            _ => json!({
                "success": true,
                "message": "",
                "data": {"fixture": true}
            }),
        };
        assert_eq!(envelope, expected, "{} {}", case.method, case.uri);
        let calls = provider.calls().expect("calls");
        assert_eq!(calls.len(), 1, "{} {}", case.method, case.uri);
        assert_eq!(
            calls[0].operation, case.operation,
            "{} {}",
            case.method, case.uri
        );
        assert_eq!(
            calls[0].resource_id, case.resource_id,
            "{} {}",
            case.method, case.uri
        );
    }
}

#[tokio::test]
async fn update_queries_cannot_replace_json_identifiers_and_preserve_legacy_status_only_rules() {
    let provider = Arc::new(MemoryCatalogProvider::default());
    let request = Request::builder()
        .method("PUT")
        .uri("/api/redemption/?id=999&status_only=1")
        .header("content-type", "application/json")
        .body(Body::from(r#"{"id":14,"status":0}"#))
        .expect("request");
    let (_, envelope) = invoke(&app(Arc::clone(&provider)), request).await;

    assert_eq!(envelope["success"], true);
    let calls = provider.calls().expect("calls");
    assert_eq!(calls.len(), 1);
    assert_eq!(calls[0].operation, CatalogOperation::UpdateRedemption);
    assert_eq!(
        calls[0].input,
        json!({"id": 14, "status": 0, "status_only": "1"})
    );

    let provider = Arc::new(MemoryCatalogProvider::default());
    let request = Request::builder()
        .method("PUT")
        .uri("/api/models/?id=999&status_only=true")
        .header("content-type", "application/json")
        .body(Body::from(r#"{"id":11,"status":0}"#))
        .expect("request");
    let (_, envelope) = invoke(&app(Arc::clone(&provider)), request).await;

    assert_eq!(envelope["success"], true);
    let calls = provider.calls().expect("calls");
    assert_eq!(calls.len(), 1);
    assert_eq!(
        calls[0].input,
        json!({"id": 11, "status": 0, "status_only": "true"})
    );
}

#[tokio::test]
async fn crud_body_validation_stays_at_the_legacy_business_error_boundary() {
    let provider = Arc::new(MemoryCatalogProvider::default());
    let cases = [
        ("POST", "/api/models/", "[]", "无效的参数"),
        ("PUT", "/api/vendors/", r#"{"id":0}"#, "id 参数错误"),
        (
            "PUT",
            "/api/prefill_group/",
            r#"{"id":"13"}"#,
            "id 参数错误",
        ),
        ("PUT", "/api/redemption/", r#"{"id":0}"#, "id 参数错误"),
    ];
    for (method, uri, body, message) in cases {
        let request = Request::builder()
            .method(method)
            .uri(uri)
            .header("content-type", "application/json")
            .body(Body::from(body))
            .expect("request");
        let (status, envelope) = invoke(&app(Arc::clone(&provider)), request).await;
        assert_eq!(status, StatusCode::OK, "{method} {uri}");
        assert_eq!(envelope, json!({"success": false, "message": message}));
    }
    assert!(provider.calls().expect("calls").is_empty());
}
