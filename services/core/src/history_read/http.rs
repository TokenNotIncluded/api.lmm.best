//! Existing console-compatible routes. Authority comes from the stored bearer
//! credential, not query parameters, model output or a client-supplied role.
use super::repository::{Audience,Filter,HistoryRead,ReadError};
use crate::finance_export::{MAX_EXPORT_BYTES,archive};
use axum::{Json,Router,body::{Body,Bytes},extract::{OriginalUri,Query,Request,State},
    http::{HeaderMap,HeaderValue,StatusCode,header},middleware::{self,Next},
    response::{IntoResponse,Response},routing::get};
use serde_json::{Value,json};
use sqlx::PgPool;
use std::{io,sync::Arc,time::{Duration,SystemTime,UNIX_EPOCH}};
use tokio::sync::{Semaphore,mpsc};
use tokio_stream::wrappers::ReceiverStream;

#[derive(Clone)]
pub struct HistoryApi { pool:PgPool, exports:Arc<Semaphore> }
impl HistoryApi {
    pub fn new(pool:PgPool)->Self {Self {pool,exports:Arc::new(Semaphore::new(1))}}
}
pub fn router(state:HistoryApi)->Router {
    let mut router=Router::new();
    for path in ["/api/log/","/api/log","/api/log/self","/api/log/token","/api/log/stat",
        "/api/log/self/stat","/api/data/","/api/data","/api/data/users","/api/data/self",
        "/api/data/flow","/api/data/flow/self","/api/finance/export"] {
        router=router.route(path,get(handle));
    }
    router.layer(middleware::from_fn(headers)).with_state(state)
}
async fn headers(request:Request,next:Next)->Response {
    let mut response=if request.uri().query().is_some_and(|q|q.len()>4096) {
        ReadError::Limit.into_response()
    } else { next.run(request).await };
    for (name,value) in [(header::CACHE_CONTROL,"no-store"),(header::REFERRER_POLICY,"no-referrer"),
        (header::X_CONTENT_TYPE_OPTIONS,"nosniff"),(header::CONTENT_SECURITY_POLICY,"default-src 'none'; frame-ancestors 'none'")] {
        response.headers_mut().insert(name,HeaderValue::from_static(value));
    }
    response
}
impl IntoResponse for ReadError {
    fn into_response(self)->Response {
        let (status,code,message)=match self {
            Self::Unauthorized=>(StatusCode::UNAUTHORIZED,"HISTORY_UNAUTHORIZED","Authentication is required"),
            Self::Forbidden=>(StatusCode::FORBIDDEN,"HISTORY_FORBIDDEN","Insufficient permission"),
            Self::Invalid=>(StatusCode::BAD_REQUEST,"HISTORY_INVALID","Invalid history filter or time range"),
            Self::Limit=>(StatusCode::PAYLOAD_TOO_LARGE,"HISTORY_LIMIT","Result exceeds a resource limit; narrow the query"),
            Self::Busy=>(StatusCode::TOO_MANY_REQUESTS,"HISTORY_BUSY","Another financial export is in progress"),
            Self::Unavailable=>(StatusCode::SERVICE_UNAVAILABLE,"HISTORY_SOURCE_UNAVAILABLE","The complete verified history source is unavailable"),
        };
        (status,Json(json!({"success":false,"code":code,"message":message}))).into_response()
    }
}
fn bearer(headers:&HeaderMap)->Result<&str,ReadError> {
    if headers.get_all(header::AUTHORIZATION).iter().count()!=1 {return Err(ReadError::Unauthorized);}
    headers.get(header::AUTHORIZATION).and_then(|h|h.to_str().ok())
        .and_then(|h|h.strip_prefix("Bearer ")).filter(|s|!s.is_empty()).ok_or(ReadError::Unauthorized)
}
async fn handle(State(state):State<HistoryApi>,OriginalUri(uri):OriginalUri,
    headers:HeaderMap,query:Result<Query<Filter>,axum::extract::rejection::QueryRejection>)->Response {
    let result=async {
        let secret=bearer(&headers)?;
        let Query(mut filter)=query.map_err(|_|ReadError::Invalid)?;
        let path=uri.path();
        let audience=match path {
            "/api/log/self"|"/api/log/self/stat"|"/api/data/self"|"/api/data/flow/self"=>Audience::SelfUser,
            "/api/log/token"=>Audience::Token,
            _=>Audience::Administrator,
        };
        if path=="/api/finance/export" {
            // A finance bundle is a complete administrator snapshot, never a
            // partially filtered wallet/log selection labeled as full finance.
            if filter.user_id.is_some() || filter.team_id.is_some() || filter.log_type!=0
                || filter.channel.is_some() || [&filter.username,&filter.token_name,&filter.model_name,&filter.group,&filter.request_id].iter().any(|v|!v.is_empty()) {
                return Err(ReadError::Invalid);
            }
            if filter.end_timestamp==0 {
                filter.end_timestamp=SystemTime::now().duration_since(UNIX_EPOCH).map_err(|_|ReadError::Unavailable)?.as_secs() as i64;
            }
            if filter.start_timestamp==0 {filter.start_timestamp=filter.end_timestamp.saturating_sub(2_592_000).max(1);}
            let permit=state.exports.clone().try_acquire_owned().map_err(|_|ReadError::Busy)?;
            let mut read=HistoryRead::begin(&state.pool,secret,audience,filter).await?;
            let data=archive::build(&mut read,MAX_EXPORT_BYTES).await?;
            read.finish().await?;
            let length=data.len();
            let (sender,receiver)=mpsc::channel::<Result<Bytes,io::Error>>(1);
            tokio::spawn(async move {
                // The pool connection is already released. One producer owns
                // the memory budget. Disconnect or timeout drops the remainder.
                let _permit=permit;
                let mut data=Bytes::from(data);
                let send=async {
                    while !data.is_empty() {
                        let chunk=data.split_to(data.len().min(16*1024));
                        if sender.send(Ok(chunk)).await.is_err() {break;}
                    }
                };
                if tokio::time::timeout(Duration::from_secs(30),send).await.is_err() {
                    let _=sender.try_send(Err(io::Error::new(io::ErrorKind::TimedOut,"export download expired")));
                }
            });
            let mut response=Body::from_stream(ReceiverStream::new(receiver)).into_response();
            response.headers_mut().insert(header::CONTENT_TYPE,HeaderValue::from_static("application/zip"));
            response.headers_mut().insert(header::CONTENT_DISPOSITION,HeaderValue::from_static("attachment; filename=\"lmm-finance.zip\""));
            response.headers_mut().insert(header::CONTENT_LENGTH,HeaderValue::from_str(&length.to_string()).map_err(|_|ReadError::Unavailable)?);
            return Ok(response);
        }
        let mut read=HistoryRead::begin(&state.pool,secret,audience,filter).await?;
        let value:Value=if path.ends_with("/stat") {
            serde_json::to_value(read.statistics().await?).map_err(|_|ReadError::Unavailable)?
        } else if path.starts_with("/api/data") {
            serde_json::to_value(read.buckets(path.ends_with("/users"),path.contains("/flow")).await?).map_err(|_|ReadError::Unavailable)?
        } else {
            serde_json::to_value(read.logs().await?).map_err(|_|ReadError::Unavailable)?
        };
        read.finish().await?;
        Ok::<_,ReadError>(Json(json!({"success":true,"message":"","data":value})).into_response())
    };
    match tokio::time::timeout(Duration::from_secs(30),result).await {
        Ok(Ok(response))=>response,
        Ok(Err(error))=>error.into_response(),
        Err(_)=>ReadError::Unavailable.into_response(),
    }
}
