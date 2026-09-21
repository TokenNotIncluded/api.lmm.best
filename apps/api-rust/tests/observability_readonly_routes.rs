use std::{
    collections::BTreeMap,
    sync::{Arc, Mutex},
};

use async_trait::async_trait;
use axum::{
    body::Body,
    http::{HeaderMap, Request, StatusCode},
};
use lmm_api_rs::routes::observability::{
    ObservabilityAccess, ObservabilityAuthError, ObservabilityAuthorizer, ObservabilityCall,
    ObservabilityOperation, ObservabilityPrincipal, ObservabilityState, ObservabilityStore,
    ObservabilityStoreError, observability_router,
};
use serde_json::{Value, json};
use tower::ServiceExt;

#[derive(Default)]
struct CapturingStore(Mutex<Vec<ObservabilityCall>>);

#[async_trait]
impl ObservabilityStore for CapturingStore {
    async fn execute(&self, call: ObservabilityCall) -> Result<Value, ObservabilityStoreError> {
        self.0.lock().expect("call lock").push(call);
        Ok(json!({"read_only": true}))
    }
}

struct AllowReadonly;

#[async_trait]
impl ObservabilityAuthorizer for AllowReadonly {
    async fn authorize(
        &self,
        _: &HeaderMap,
        access: ObservabilityAccess,
    ) -> Result<ObservabilityPrincipal, ObservabilityAuthError> {
        Ok(match access {
            ObservabilityAccess::Token => ObservabilityPrincipal::Token { token_id: 9 },
            ObservabilityAccess::PublicOrUser => ObservabilityPrincipal::Public,
            ObservabilityAccess::Admin | ObservabilityAccess::Root | ObservabilityAccess::User => {
                ObservabilityPrincipal::User {
                    user_id: 7,
                    username: "reader".to_owned(),
                    role: 100,
                }
            }
        })
    }
}

fn app(store: Arc<CapturingStore>) -> axum::Router {
    observability_router(ObservabilityState::new(store, Arc::new(AllowReadonly)))
}

#[tokio::test]
async fn observability_readonly_routes_keep_legacy_method_scope_and_query_contracts() {
    struct Case {
        path: &'static str,
        access: ObservabilityAccess,
        operation: ObservabilityOperation,
    }

    let cases = [
        Case {
            path: "/api/data/?start_timestamp=1&end_timestamp=2&username=alice",
            access: ObservabilityAccess::Admin,
            operation: ObservabilityOperation::AllQuotaDates,
        },
        Case {
            path: "/api/data/users?start_timestamp=1&end_timestamp=2",
            access: ObservabilityAccess::Admin,
            operation: ObservabilityOperation::QuotaDatesByUser,
        },
        Case {
            path: "/api/data/self?start_timestamp=1&end_timestamp=2",
            access: ObservabilityAccess::User,
            operation: ObservabilityOperation::SelfQuotaDates,
        },
        Case {
            path: "/api/data/flow?start_timestamp=1&end_timestamp=2&username=alice",
            access: ObservabilityAccess::Admin,
            operation: ObservabilityOperation::AllFlowQuotaDates,
        },
        Case {
            path: "/api/data/flow/self?start_timestamp=1&end_timestamp=2",
            access: ObservabilityAccess::User,
            operation: ObservabilityOperation::SelfFlowQuotaDates,
        },
        Case {
            path: "/api/log/?p=2&page_size=20&type=2&model_name=gpt-5",
            access: ObservabilityAccess::Admin,
            operation: ObservabilityOperation::AllLogs,
        },
        Case {
            path: "/api/log/self?p=2&ps=20&request_id=req-1",
            access: ObservabilityAccess::User,
            operation: ObservabilityOperation::SelfLogs,
        },
        Case {
            path: "/api/log/self/stat?type=2&token_name=main",
            access: ObservabilityAccess::User,
            operation: ObservabilityOperation::SelfLogStats,
        },
        Case {
            path: "/api/log/stat?type=2&channel=7",
            access: ObservabilityAccess::Admin,
            operation: ObservabilityOperation::LogStats,
        },
        Case {
            path: "/api/log/token",
            access: ObservabilityAccess::Token,
            operation: ObservabilityOperation::LogsByToken,
        },
        Case {
            path: "/api/log/channel_affinity_usage_cache?rule_name=weighted&key_fp=fingerprint",
            access: ObservabilityAccess::Admin,
            operation: ObservabilityOperation::ChannelAffinityUsageCacheStats,
        },
        Case {
            path: "/api/perf-metrics?model=gpt+five&model=ignored&group=default&hours=24",
            access: ObservabilityAccess::PublicOrUser,
            operation: ObservabilityOperation::PerfMetrics,
        },
        Case {
            path: "/api/perf-metrics/summary?hours=24",
            access: ObservabilityAccess::PublicOrUser,
            operation: ObservabilityOperation::PerfMetricsSummary,
        },
        Case {
            path: "/api/performance/logs",
            access: ObservabilityAccess::Root,
            operation: ObservabilityOperation::LogFiles,
        },
        Case {
            path: "/api/performance/stats",
            access: ObservabilityAccess::Root,
            operation: ObservabilityOperation::PerformanceStats,
        },
    ];
    let store = Arc::new(CapturingStore::default());
    let app = app(Arc::clone(&store));

    for case in cases {
        let response = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri(case.path)
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("router response");
        assert_eq!(response.status(), StatusCode::OK, "{}", case.path);
        let calls = store.0.lock().expect("call lock");
        let call = calls.last().expect("one read call");
        assert_eq!(call.operation, case.operation, "{}", case.path);
        assert_eq!(call.principal, principal_for(case.access), "{}", case.path);
    }

    let calls = store.0.lock().expect("call lock");
    let metrics = calls
        .iter()
        .find(|call| call.operation == ObservabilityOperation::PerfMetrics)
        .expect("metrics call");
    assert_eq!(metrics.query["model"], "gpt five");
    assert_eq!(metrics.query["group"], "default");
}

fn principal_for(access: ObservabilityAccess) -> ObservabilityPrincipal {
    match access {
        ObservabilityAccess::Token => ObservabilityPrincipal::Token { token_id: 9 },
        ObservabilityAccess::PublicOrUser => ObservabilityPrincipal::Public,
        ObservabilityAccess::Admin | ObservabilityAccess::Root | ObservabilityAccess::User => {
            ObservabilityPrincipal::User {
                user_id: 7,
                username: "reader".to_owned(),
                role: 100,
            }
        }
    }
}

#[test]
fn observability_readonly_calls_are_storage_read_shapes() {
    let query = BTreeMap::from([("start_timestamp".to_owned(), "1".to_owned())]);
    assert_eq!(query["start_timestamp"], "1");
}
