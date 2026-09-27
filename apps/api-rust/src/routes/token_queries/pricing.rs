use super::{TokenFacts, TokenQueryState};
use crate::routes::{
    legacy_http::legacy_json,
    relay_openai::billing::{completion_default, option_entry},
};
use axum::{
    http::{StatusCode, header},
    response::{IntoResponse, Response},
};
use chrono::Utc;
use rust_decimal::prelude::ToPrimitive;
use serde_json::{Value, json};
use sqlx::Row;
use std::collections::{BTreeMap, BTreeSet};

pub(super) struct Context {
    options: BTreeMap<String, String>,
    parsed_maps: BTreeMap<&'static str, Value>,
    groups: Vec<String>,
    user_group: String,
    role: i64,
    override_level: Option<i64>,
    created_at: i64,
    last_api_at: i64,
    console_activated_at: i64,
}
fn failure(status: StatusCode, message: &str) -> Response {
    legacy_json(status, json!({"success":false,"message":message}))
}
fn context_error() -> Response {
    failure(
        StatusCode::SERVICE_UNAVAILABLE,
        "pricing context unavailable",
    )
}
fn price_error() -> Response {
    failure(StatusCode::SERVICE_UNAVAILABLE, "pricing unavailable")
}
fn object(
    options: &BTreeMap<String, String>,
    key: &str,
    default: Value,
) -> Result<Value, Response> {
    match options.get(key) {
        Some(raw) => serde_json::from_str(raw).map_err(|_| context_error()),
        None => Ok(default),
    }
}
fn lookup(context: &Context, key: &str, name: &str) -> Result<Option<Value>, Response> {
    if let Some(values) = context.parsed_maps.get(key) {
        return Ok(values.get(name).cloned());
    }
    option_entry(&context.options, key, name).map_err(|_| price_error())
}
fn number(context: &Context, key: &str, name: &str) -> Result<Option<f64>, Response> {
    lookup(context, key, name)?
        .map(|value| match value {
            Value::Null => Ok(0.0),
            Value::Number(number) => number.as_f64().ok_or_else(price_error),
            _ => Err(price_error()),
        })
        .transpose()
}

pub(super) async fn context(
    state: &TokenQueryState,
    token: &TokenFacts,
) -> Result<Context, Response> {
    if token.status != 1 || (!token.unlimited_quota && token.remain_quota <= 0) {
        return Err(failure(StatusCode::UNAUTHORIZED, "API key unavailable"));
    }
    let row=sqlx::query("SELECT COALESCE(\"group\",'') AS user_group,COALESCE(role,0)::BIGINT AS role,to_jsonb(users)->>'trust_level_override' AS override_level,COALESCE((to_jsonb(users)->>'created_at')::BIGINT,0) AS created_at,COALESCE((to_jsonb(users)->>'last_api_activity_at')::BIGINT,0) AS last_api_at,COALESCE((to_jsonb(users)->>'console_activated_at')::BIGINT,0) AS console_activated_at FROM users WHERE id=$1 AND deleted_at IS NULL")
        .bind(token.user_id).fetch_optional(&state.pg).await.map_err(|_|context_error())?.ok_or_else(context_error)?;
    let mut options = if let Some(runtime) = &state.runtime {
        runtime.snapshot().await
    } else {
        sqlx::query_as::<_, (String, String)>("SELECT key,COALESCE(value,'') FROM options")
            .fetch_all(&state.pg)
            .await
            .map_err(|_| context_error())?
            .into_iter()
            .collect()
    };
    for (source, target) in [
        ("group_ratio_setting.group_ratio", "GroupRatio"),
        ("group_ratio_setting.group_group_ratio", "GroupGroupRatio"),
    ] {
        if let Some(value) = options.get(source).cloned() {
            options.insert(target.into(), value);
        }
    }
    let user_group: String = row.try_get("user_group").map_err(|_| context_error())?;
    let mut usable = object(
        &options,
        "UserUsableGroups",
        json!({"default":"默认分组","vip":"vip分组"}),
    )?
    .as_object()
    .cloned()
    .unwrap_or_default();
    let special = object(
        &options,
        "group_ratio_setting.group_special_usable_group",
        json!({}),
    )?;
    if let Some(special) = special.get(&user_group).and_then(Value::as_object) {
        for (group, description) in special {
            if let Some(remove) = group.strip_prefix("-:") {
                usable.remove(remove);
            } else {
                usable.insert(
                    group.strip_prefix("+:").unwrap_or(group).to_owned(),
                    description.clone(),
                );
            }
        }
    }
    if !user_group.is_empty() {
        usable
            .entry(user_group.clone())
            .or_insert(json!("用户分组"));
    }
    let ratios = object(
        &options,
        "GroupRatio",
        json!({"default":1,"vip":1,"svip":1}),
    )?;
    if !token.token_group.is_empty()
        && (!usable.contains_key(&token.token_group)
            || (token.token_group != "auto" && ratios.get(&token.token_group).is_none()))
    {
        return Err(failure(StatusCode::FORBIDDEN, "group unavailable"));
    }
    let groups = if token.token_group == "auto" {
        let per_token = if token.auto_groups.is_empty() {
            None
        } else {
            match serde_json::from_str::<Option<Vec<String>>>(&token.auto_groups) {
                Ok(Some(groups)) if !groups.is_empty() => Some(groups),
                Ok(_) => None,
                Err(_) => Some(Vec::new()),
            }
        };
        let restricted = per_token.is_some();
        let configured = per_token.unwrap_or_else(|| {
            options
                .get("AutoGroups")
                .and_then(|value| serde_json::from_str::<Option<Vec<String>>>(value).ok())
                .map(Option::unwrap_or_default)
                .unwrap_or_else(|| vec!["default".into()])
        });
        let maximum = if restricted {
            options
                .get("MaxTokenAutoGroups")
                .and_then(|value| value.parse::<usize>().ok())
                .filter(|value| *value > 0)
                .unwrap_or(5)
        } else {
            usize::MAX
        };
        let mut seen = BTreeSet::new();
        configured
            .into_iter()
            .filter(|group| {
                !group.is_empty()
                    && group != "auto"
                    && usable.contains_key(group)
                    && ratios.get(group).is_some()
                    && seen.insert(group.clone())
            })
            .take(maximum)
            .collect()
    } else {
        vec![if token.token_group.is_empty() {
            user_group.clone()
        } else {
            token.token_group.clone()
        }]
    };
    let raw: Option<String> = row.try_get("override_level").map_err(|_| context_error())?;
    Ok(Context {
        options,
        parsed_maps: BTreeMap::new(),
        groups,
        user_group,
        role: row.try_get("role").map_err(|_| context_error())?,
        override_level: raw.map(|value| value.parse().unwrap_or(-1)),
        created_at: row.try_get("created_at").map_err(|_| context_error())?,
        last_api_at: row.try_get("last_api_at").map_err(|_| context_error())?,
        console_activated_at: row
            .try_get("console_activated_at")
            .map_err(|_| context_error())?,
    })
}

async fn discount(
    state: &TokenQueryState,
    token: &TokenFacts,
    context: &Context,
) -> Result<f64, Response> {
    let payment = if context.role < 10 && context.override_level.is_none() {
        let mut connection = state.pg.acquire().await.map_err(|_| context_error())?;
        crate::auth::payment_snapshot(&mut connection, token.user_id, &context.options)
            .await
            .map_err(|_| context_error())?
    } else {
        crate::auth::PaymentSnapshot::default()
    };
    let facts = crate::auth::DashboardSelfUserFacts {
        trust_level_override: context.override_level,
        paid_amount: payment.paid_amount,
        paid_activation_complete: payment.paid_activation_complete,
        console_activated: payment.console_activated || context.console_activated_at > 0,
        activity_anchor: context
            .created_at
            .max(context.last_api_at)
            .max(payment.last_paid_complete_at),
        now: Utc::now().timestamp(),
        ..Default::default()
    };
    Ok(crate::auth::evaluate_trust_level(context.role, facts).discount_ratio)
}

pub(super) async fn response(
    state: &TokenQueryState,
    token: &TokenFacts,
    mut context: Context,
    requested: &str,
) -> Response {
    if requested.len() > 512 {
        return failure(StatusCode::BAD_REQUEST, "model query is too long");
    }
    let unit = match context.options.get("QuotaPerUnit") {
        Some(value) => match value.parse::<f64>() {
            Ok(value) => value,
            Err(_) => return price_error(),
        },
        None => 500000.0,
    };
    if !unit.is_finite() || unit <= 0.0 {
        return price_error();
    }
    let discount = match discount(state, token, &context).await {
        Ok(value) => value,
        Err(response) => return response,
    };
    // Parse each saved map once per request. Parsing a full model catalogue
    // for every model would make this list endpoint quadratic in catalogue size.
    for key in [
        "ModelRatio",
        "ModelPrice",
        "CompletionRatio",
        "CacheRatio",
        "CreateCacheRatio",
        "GroupRatio",
        "GroupGroupRatio",
        "billing_setting.billing_mode",
        "billing_setting.billing_expr",
    ] {
        if let Some(raw) = context.options.get(key) {
            let values = if raw.trim().is_empty() {
                Value::Null
            } else {
                match serde_json::from_str::<Value>(raw) {
                    Ok(value) => value,
                    Err(_) => return price_error(),
                }
            };
            if !values.is_null() && !values.is_object() {
                return price_error();
            }
            context.parsed_maps.insert(key, values);
        }
    }
    let metadata=match sqlx::query_as::<_,(String,i64,i64)>("SELECT model_name,COALESCE(status,0)::BIGINT,COALESCE(name_rule,0)::BIGINT FROM models WHERE deleted_at IS NULL ORDER BY id").fetch_all(&state.pg).await{Ok(rows)=>rows,Err(_)=>return price_error()};
    let rows=match sqlx::query_as::<_,(String,String)>("SELECT model,\"group\" FROM abilities WHERE enabled=TRUE ORDER BY model,channel_id,\"group\"").fetch_all(&state.pg).await{Ok(rows)=>rows,Err(_)=>return price_error()};
    let mut catalog = BTreeMap::<String, BTreeSet<String>>::new();
    for (model, group) in rows {
        catalog.entry(model).or_default().insert(group);
    }
    let limits = token.model_limits.split(',').collect::<BTreeSet<_>>();
    let mut result = Vec::new();
    for (name, enabled_groups) in catalog {
        if !requested.is_empty() && requested != name {
            continue;
        }
        let matched = crate::models::legacy_pricing_model_name(&name);
        if token.model_limits_enabled
            && !limits.contains(name.as_str())
            && !limits.contains(matched)
        {
            continue;
        }
        let meta = [0, 1, 3, 2].into_iter().find_map(|rule| {
            metadata.iter().find(|(pattern, _, kind)| {
                *kind == rule
                    && match rule {
                        0 => name == *pattern,
                        1 => name.starts_with(pattern.as_str()),
                        3 => name.ends_with(pattern.as_str()),
                        _ => name.contains(pattern.as_str()),
                    }
            })
        });
        if meta.is_some_and(|(_, status, _)| *status != 1) {
            continue;
        }
        for group in &context.groups {
            if !enabled_groups.contains(group) && !enabled_groups.contains("all") {
                continue;
            }
            let entry = (|| -> Result<Option<Value>, Response> {
                let ordinary = number(&context, "GroupRatio", group)?.unwrap_or(1.0);
                let special = lookup(&context, "GroupGroupRatio", &context.user_group)
                    .map_err(|_| price_error())?
                    .and_then(|value| value.get(group).and_then(Value::as_f64));
                let ratio = special.unwrap_or(ordinary) * discount;
                let mut entry = json!({"model":name,"group":group,"currency":"USD","group_ratio":finite_number(ratio)?,"trust_discount_ratio":finite_number(discount)?});
                let expression = lookup(&context, "billing_setting.billing_mode", &name)
                    .map_err(|_| price_error())?
                    .is_some_and(|value| value == "tiered_expr");
                if expression {
                    let Some(expression) = lookup(&context, "billing_setting.billing_expr", &name)
                        .map_err(|_| price_error())?
                        .and_then(|value| value.as_str().map(str::to_owned))
                    else {
                        return Ok(None);
                    };
                    entry["billing_mode"] = json!("tiered_expr");
                    entry["unit"] = json!("expression");
                    if !expression.is_empty() {
                        entry["billing_expression"] = json!(expression);
                    }
                } else if let Some(price) = number(&context, "ModelPrice", matched)? {
                    entry["billing_mode"] = json!("per_request");
                    entry["unit"] = json!("request");
                    entry["request_price"] = finite_number(price * ratio)?;
                } else {
                    let Some(model) = number(&context, "ModelRatio", matched)? else {
                        return Ok(None);
                    };
                    let (default, locked) = completion_default(matched);
                    let configured = number(&context, "CompletionRatio", matched)?;
                    let completion = if matched.contains('/') || !locked {
                        configured.unwrap_or(default.to_f64().ok_or_else(price_error)?)
                    } else {
                        default.to_f64().ok_or_else(price_error)?
                    };
                    let cache = number(&context, "CacheRatio", &name)?.unwrap_or(1.0);
                    let write = number(&context, "CreateCacheRatio", &name)?.unwrap_or(1.25);
                    entry["billing_mode"] = json!("per_token");
                    entry["unit"] = json!("million_tokens");
                    for (key, value) in [
                        ("model_ratio", model),
                        ("completion_ratio", completion),
                        ("cache_ratio", cache),
                        ("create_cache_ratio", write),
                    ] {
                        if value != 0.0 {
                            entry[key] = finite_number(value)?;
                        }
                    }
                    let input = model * ratio * 1_000_000.0 / unit;
                    entry["input_price"] = finite_number(input)?;
                    entry["output_price"] = finite_number(input * completion)?;
                }
                Ok(Some(entry))
            })();
            match entry {
                Ok(Some(entry)) => result.push(entry),
                Ok(None) => {}
                Err(response) => return response,
            }
        }
    }
    if !requested.is_empty() && result.is_empty() {
        return failure(StatusCode::NOT_FOUND, "model not available");
    }
    pricing_json(
        StatusCode::OK,
        json!({"success":true,"data":result,"updated_at":Utc::now().timestamp(),"scope":"token","price_basis":"configured_base_rates","final_cost_depends_on_usage":true}),
    )
}

// encoding/json formats float64 using the shortest decimal at [1e-6,1e21)
// and scientific notation outside it. Keep integer JSON values as integers:
// converting the whole payload through f64 would corrupt quota-sized values.
fn float_json(value: f64) -> String {
    if value != 0.0 && (value.abs() < 1e-6 || value.abs() >= 1e21) {
        let scientific = format!("{value:e}");
        let (mantissa, exponent) = scientific.split_once('e').expect("float exponent");
        if exponent.starts_with('-') {
            scientific
        } else {
            format!("{mantissa}e+{exponent}")
        }
    } else {
        value.to_string()
    }
}
fn finite_number(value: f64) -> Result<Value, Response> {
    serde_json::Number::from_f64(value)
        .map(Value::Number)
        .ok_or_else(price_error)
}
fn pricing_json(status: StatusCode, body: Value) -> Response {
    fn encode(value: &Value, output: &mut String) {
        match value {
            Value::Number(number) if number.is_f64() => {
                output.push_str(&float_json(number.as_f64().expect("finite JSON float")))
            }
            Value::Array(values) => {
                output.push('[');
                for (index, value) in values.iter().enumerate() {
                    if index != 0 {
                        output.push(',');
                    }
                    encode(value, output);
                }
                output.push(']');
            }
            Value::Object(values) => {
                output.push('{');
                for (index, (key, value)) in values.iter().enumerate() {
                    if index != 0 {
                        output.push(',');
                    }
                    output.push_str(&serde_json::to_string(key).expect("JSON string"));
                    output.push(':');
                    encode(value, output);
                }
                output.push('}');
            }
            _ => output.push_str(&value.to_string()),
        }
    }
    let mut output = String::new();
    encode(&body, &mut output);
    (
        status,
        [(header::CONTENT_TYPE, "application/json; charset=utf-8")],
        output,
    )
        .into_response()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn floats_keep_current_go_decimal_and_exponent_spelling() {
        let oracle: Value = serde_json::from_str(include_str!(
            "../../../tests/fixtures/token-pricing-current-go.json"
        ))
        .unwrap();
        let vectors = oracle["float_json"].as_array().expect("Go float vectors");
        assert!(vectors.len() >= 10);
        for vector in vectors {
            let value: f64 = vector["input"].as_str().unwrap().parse().unwrap();
            assert_eq!(
                float_json(value),
                vector["json"].as_str().unwrap(),
                "{vector}"
            );
        }
        assert!(finite_number(f64::INFINITY).is_err());
        assert!(finite_number(f64::NAN).is_err());
    }
}
