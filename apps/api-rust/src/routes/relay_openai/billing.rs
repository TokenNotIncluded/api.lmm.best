//! Request-scoped price snapshots and provider usage. Prices are frozen before
//! upstream I/O; a reload must not change the final price of an in-flight turn.

use std::{collections::BTreeMap, str::FromStr, sync::OnceLock};

use axum::http::StatusCode;
use rust_decimal::{Decimal, RoundingStrategy, prelude::ToPrimitive};
use serde::{Deserialize, Serialize};
use serde_json::Value;

use super::tools::{ToolCalls, ToolPrices};
use super::{OpenAiRelayEndpoint, OpenAiRelayFailure};
use crate::routes::sse::SseFrameParser;

const MAX_QUOTA: i64 = i32::MAX as i64;

#[derive(Clone, Copy, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
pub(super) struct Usage {
    pub input: i64,
    pub output: i64,
    pub cached: i64,
    pub cache_write: i64,
    pub image: i64,
}

impl Usage {
    fn parse(value: &Value) -> Self {
        let count = |key: &str| value.get(key).and_then(Value::as_i64).unwrap_or(0).max(0);
        let chat = value.get("prompt_tokens").is_some() || value.get("completion_tokens").is_some();
        let details = value
            .get(if chat {
                "prompt_tokens_details"
            } else {
                "input_tokens_details"
            })
            .filter(|value| value.is_object())
            .or_else(|| {
                value
                    .get(if chat {
                        "input_tokens_details"
                    } else {
                        "prompt_tokens_details"
                    })
                    .filter(|value| value.is_object())
            });
        let detail = |key: &str| {
            details
                .and_then(|value| value.get(key))
                .and_then(Value::as_i64)
                .unwrap_or(0)
                .max(0)
        };
        Self {
            input: if chat {
                count("prompt_tokens")
            } else {
                count("input_tokens")
            },
            output: if chat {
                count("completion_tokens")
            } else {
                count("output_tokens")
            },
            cached: detail("cached_tokens"),
            cache_write: detail("cache_write_tokens")
                .max(detail("cached_creation_tokens"))
                .max(detail("cache_creation_tokens")),
            image: detail("image_tokens"),
        }
    }

    pub fn measured(self) -> bool {
        self.input > 0 || self.output > 0
    }
}

#[derive(Clone, Debug)]
pub(super) struct Price {
    fixed: Option<Decimal>,
    model: Decimal,
    completion: Decimal,
    cache: Decimal,
    cache_write: Decimal,
    image: Decimal,
    group: Decimal,
    quota_unit: Decimal,
    prompt_floor: i64,
    max_output: i64,
    tool_prices: ToolPrices,
    model_name: String,
    free_model: bool,
    pub reservation: i64,
}

impl Price {
    /// Explicit fixed-quota mode retained for isolated executor fixtures.
    pub fn fixed(quota: i64) -> Self {
        Self {
            fixed: Some(Decimal::from(quota)),
            model: Decimal::ONE,
            completion: Decimal::ONE,
            cache: Decimal::ONE,
            cache_write: Decimal::ONE,
            image: Decimal::ONE,
            group: Decimal::ONE,
            quota_unit: Decimal::ONE,
            prompt_floor: 0,
            max_output: 0,
            tool_prices: ToolPrices::default(),
            model_name: String::new(),
            free_model: false,
            reservation: quota,
        }
    }

    #[cfg(test)]
    pub fn from_options(
        options: &BTreeMap<String, String>,
        model: &str,
        user_group: &str,
        using_group: &str,
        request: &Value,
    ) -> Result<Self, OpenAiRelayFailure> {
        Self::from_options_with_discount(options, model, user_group, using_group, request, 1.0)
    }

    pub fn from_options_with_discount(
        options: &BTreeMap<String, String>,
        model: &str,
        user_group: &str,
        using_group: &str,
        request: &Value,
        trust_discount: f64,
    ) -> Result<Self, OpenAiRelayFailure> {
        let matched_model = crate::models::legacy_pricing_model_name(model);
        if option_entry(options, "billing_setting.billing_mode", model)?
            .and_then(|value| value.as_str().map(str::to_owned))
            .is_some_and(|mode| mode == "tiered_expr")
        {
            return Err(price_error(
                "tiered expression billing is not configured for this relay executor",
            ));
        }
        let fixed = ratio(options, "ModelPrice", matched_model)?;
        let model_ratio = ratio(options, "ModelRatio", matched_model)?;
        if fixed.is_none() && model_ratio.is_none() {
            return Err(price_error("model price is not configured"));
        }
        let group = group_ratio(options, user_group, using_group, trust_discount)?;
        let quota_unit = options
            .get("QuotaPerUnit")
            .map(|value| decimal(Value::String(value.clone())))
            .transpose()?
            .unwrap_or(Decimal::from(500_000));
        let (default_completion, locked) = completion_default(matched_model);
        let configured_completion = ratio(options, "CompletionRatio", matched_model)?;
        let completion = if matched_model.contains('/') || !locked {
            configured_completion.unwrap_or(default_completion)
        } else {
            default_completion
        };
        let preconsume = options
            .get("PreConsumedQuota")
            .map(|value| {
                value
                    .parse::<i64>()
                    .map_err(|_| price_error("invalid pre-consumption quota"))
            })
            .transpose()?
            .unwrap_or(500)
            .max(0);
        let max_output = ["max_output_tokens", "max_completion_tokens", "max_tokens"]
            .iter()
            .filter_map(|key| request.get(key).and_then(Value::as_i64))
            .max()
            .unwrap_or(0)
            .max(0);
        let mut price = Self {
            fixed,
            model: if fixed.is_some() {
                Decimal::ZERO
            } else {
                model_ratio.unwrap_or(Decimal::ZERO)
            },
            completion,
            cache: ratio(options, "CacheRatio", model)?.unwrap_or(Decimal::ONE),
            cache_write: ratio(options, "CreateCacheRatio", model)?.unwrap_or(Decimal::new(125, 2)),
            image: ratio(options, "ImageRatio", model)?.unwrap_or(Decimal::ONE),
            group,
            quota_unit,
            prompt_floor: preconsume,
            max_output,
            tool_prices: ToolPrices::configured(
                options.get("tool_price_setting.prices").map(String::as_str),
            ),
            model_name: model.to_owned(),
            free_model: !free_model_preconsume(
                options
                    .get("quota_setting.enable_free_model_pre_consume")
                    .map(String::as_str),
            ) && (group.is_zero()
                || fixed.or(model_ratio).is_some_and(|value| value.is_zero())),
            reservation: 0,
        };
        let raw = if let Some(fixed) = price.fixed {
            product(&[fixed, quota_unit, group])?
        } else {
            product(&[
                Decimal::from(preconsume.saturating_add(max_output)),
                price.model,
                group,
            ])?
        };
        if raw > Decimal::from(MAX_QUOTA) {
            return Err(price_error(
                "pre-consumption quota exceeds supported quota range",
            ));
        }
        price.reservation = raw
            .trunc()
            .to_i64()
            .unwrap_or(0)
            .max(price.minimum_reservation());
        Ok(price)
    }

    fn minimum_reservation(&self) -> i64 {
        i64::from(!self.free_model)
    }

    /// Go rebuilds only GroupRatioInfo after selecting another channel. The
    /// logical request retains its model rates, FreeModel and initial budget.
    pub fn with_retry_group(
        mut self,
        options: &BTreeMap<String, String>,
        user_group: &str,
        using_group: &str,
        trust_discount: f64,
    ) -> Result<Self, OpenAiRelayFailure> {
        self.group = group_ratio(options, user_group, using_group, trust_discount)?;
        Ok(self)
    }

    /// Tool prices and the platform unit are looked up by Go's settlement
    /// calculator, unlike the model/completion rates stored in PriceData.
    pub fn with_settlement_options(
        mut self,
        options: &BTreeMap<String, String>,
    ) -> Result<Self, OpenAiRelayFailure> {
        self.tool_prices =
            ToolPrices::configured(options.get("tool_price_setting.prices").map(String::as_str));
        self.quota_unit = options
            .get("QuotaPerUnit")
            .map(|value| decimal(Value::String(value.clone())))
            .transpose()?
            .unwrap_or(Decimal::from(500_000));
        Ok(self)
    }

    pub fn with_prompt_tokens(mut self, prompt_tokens: i64) -> Result<Self, OpenAiRelayFailure> {
        if self.fixed.is_none() {
            let tokens = self
                .prompt_floor
                .max(prompt_tokens)
                .saturating_add(self.max_output);
            let raw = product(&[Decimal::from(tokens), self.model, self.group])?;
            if raw > Decimal::from(MAX_QUOTA) {
                return Err(price_error(
                    "pre-consumption quota exceeds supported quota range",
                ));
            }
            self.reservation = raw
                .trunc()
                .to_i64()
                .unwrap_or(0)
                .max(self.minimum_reservation());
        }
        Ok(self)
    }

    pub fn quota(&self, evidence: &Evidence) -> Result<i64, OpenAiRelayFailure> {
        let surcharge = self.tool_prices.surcharge(
            &evidence.tools,
            &self.model_name,
            self.group,
            self.quota_unit,
        );
        if !evidence.usage.measured() && surcharge.is_zero() {
            // Only a normally completed response may use its prepayment as
            // missing-usage fallback. Failed empty streams always refund.
            return Ok(if evidence.completed {
                self.reservation
            } else {
                0
            });
        }
        let usage = evidence.usage;
        let raw = if let Some(fixed) = self.fixed {
            product(&[fixed, self.quota_unit, self.group])?
        } else {
            let ordinary = usage
                .input
                .saturating_sub(usage.cached)
                .saturating_sub(usage.cache_write)
                .saturating_sub(usage.image)
                .max(0);
            let mut tokens = Decimal::from(ordinary);
            for (count, ratio) in [
                (usage.output, self.completion),
                (usage.cached, self.cache),
                (usage.cache_write, self.cache_write),
                (usage.image, self.image),
            ] {
                tokens = tokens
                    .checked_add(product(&[Decimal::from(count), ratio])?)
                    .ok_or_else(|| price_error("usage quota arithmetic overflow"))?;
            }
            product(&[tokens, self.model, self.group])?
        };
        let raw = raw.checked_add(surcharge).unwrap_or(Decimal::MAX);
        let quota = raw
            .round_dp_with_strategy(0, RoundingStrategy::MidpointAwayFromZero)
            .min(Decimal::from(MAX_QUOTA))
            .to_i64()
            .unwrap_or(MAX_QUOTA);
        Ok(
            if quota == 0 && !self.model.is_zero() && !self.group.is_zero() {
                1
            } else {
                quota
            },
        )
    }

    pub fn log_metadata(&self, evidence: &Evidence) -> Value {
        let tools = self
            .tool_prices
            .log_items(&evidence.tools, &self.model_name);
        if tools.is_empty() {
            serde_json::json!({})
        } else {
            serde_json::json!({"tool_surcharges":tools})
        }
    }

    pub fn free(&self) -> bool {
        self.free_model
    }

    pub fn snapshot(&self) -> Value {
        serde_json::json!({"version":1,"model":self.model_name,"fixed":self.fixed.map(|value|value.to_string()),
            "model_ratio":self.model.to_string(),"completion_ratio":self.completion.to_string(),"cache_ratio":self.cache.to_string(),
            "cache_write_ratio":self.cache_write.to_string(),"image_ratio":self.image.to_string(),"group_ratio":self.group.to_string(),
            "quota_unit":self.quota_unit.to_string(),"reservation":self.reservation,"free_model":self.free_model,"tool_prices":self.tool_prices.snapshot()})
    }
}

/// The current Go setting defaults to true and is decoded with strconv.ParseBool.
pub(crate) fn free_model_preconsume(raw: Option<&str>) -> bool {
    !matches!(raw, Some("0" | "f" | "F" | "false" | "FALSE" | "False"))
}

fn group_ratio(
    options: &BTreeMap<String, String>,
    user_group: &str,
    using_group: &str,
    trust_discount: f64,
) -> Result<Decimal, OpenAiRelayFailure> {
    let ordinary = ratio(options, "GroupRatio", using_group)?.unwrap_or(Decimal::ONE);
    let special = option_entry(options, "GroupGroupRatio", user_group)?
        .and_then(|groups| groups.get(using_group).cloned())
        .map(decimal)
        .transpose()?;
    product(&[
        special.unwrap_or(ordinary),
        decimal(Value::String(trust_discount.to_string()))?,
    ])
}

fn product(values: &[Decimal]) -> Result<Decimal, OpenAiRelayFailure> {
    values.iter().try_fold(Decimal::ONE, |total, value| {
        total
            .checked_mul(*value)
            .ok_or_else(|| price_error("usage quota arithmetic overflow"))
    })
}

pub(crate) fn option_entry(
    options: &BTreeMap<String, String>,
    key: &str,
    name: &str,
) -> Result<Option<Value>, OpenAiRelayFailure> {
    static GO_DEFAULTS: OnceLock<Value> = OnceLock::new();
    if !options.contains_key(key) {
        let defaults = GO_DEFAULTS.get_or_init(|| {
            serde_json::from_str(include_str!("billing/go_defaults.json"))
                .expect("checked-in Go pricing defaults must be valid JSON")
        });
        return Ok(defaults
            .get(key)
            .and_then(|values| values.get(name))
            .cloned());
    }
    let Some(raw) = options.get(key).filter(|raw| !raw.trim().is_empty()) else {
        return Ok(None);
    };
    let value: Value = serde_json::from_str(raw)
        .map_err(|_| price_error("invalid model pricing configuration"))?;
    if value.is_null() {
        return Ok(None);
    }
    let object = value
        .as_object()
        .ok_or_else(|| price_error("invalid model pricing configuration"))?;
    Ok(object.get(name).cloned())
}

fn decimal(value: Value) -> Result<Decimal, OpenAiRelayFailure> {
    let raw = match value {
        Value::Number(number) => number.to_string(),
        Value::String(raw) => raw,
        Value::Null => "0".to_owned(),
        _ => return Err(price_error("invalid model pricing value")),
    };
    let value = Decimal::from_str(&raw)
        .or_else(|_| Decimal::from_scientific(&raw))
        .map_err(|_| price_error("invalid model pricing value"))?;
    if value.is_sign_negative() {
        return Err(price_error("negative model pricing value"));
    }
    Ok(value)
}

fn ratio(
    options: &BTreeMap<String, String>,
    key: &str,
    name: &str,
) -> Result<Option<Decimal>, OpenAiRelayFailure> {
    option_entry(options, key, name)?.map(decimal).transpose()
}

fn price_error(message: &str) -> OpenAiRelayFailure {
    OpenAiRelayFailure::new(StatusCode::BAD_REQUEST, "model_price_error", message)
}

pub(crate) fn completion_default(model: &str) -> (Decimal, bool) {
    let (ratio, locked) = if model.ends_with("-all") || model.ends_with("-gizmo-*") {
        (2.0, false)
    } else if model.starts_with("gpt-4o") {
        if model == "gpt-4o-2024-05-13" {
            (3.0, true)
        } else if model.starts_with("gpt-4o-mini-tts") {
            (20.0, false)
        } else {
            (4.0, false)
        }
    } else if model.starts_with("gpt-5") {
        if !model.contains('.') {
            (8.0, true)
        } else if model.starts_with("gpt-5.4-nano") {
            (6.25, true)
        } else if model.starts_with("gpt-5.4") {
            (6.0, true)
        } else {
            (6.0, false)
        }
    } else if model.starts_with("gpt-4.5-preview") {
        (2.0, true)
    } else if model.starts_with("gpt-4-turbo")
        || model.ends_with("gpt-4-1106")
        || model.ends_with("gpt-4-1105")
    {
        (3.0, true)
    } else if model.starts_with("gpt-") {
        (2.0, false)
    } else if model.starts_with("o1") || model.starts_with("o3") {
        (4.0, true)
    } else if model == "chatgpt-4o-latest" {
        (3.0, true)
    } else if [
        "claude-3",
        "claude-sonnet-4",
        "claude-opus-4",
        "claude-haiku-4",
    ]
    .iter()
    .any(|name| model.contains(name))
    {
        (5.0, true)
    } else if model.starts_with("mistral-") {
        (3.0, true)
    } else if model.starts_with("gemini-") {
        if model.starts_with("gemini-1.5") || model.starts_with("gemini-2.0") {
            (4.0, true)
        } else if model.starts_with("gemini-2.5-pro") {
            (8.0, false)
        } else if model.starts_with("gemini-2.5-flash-preview") {
            if model.ends_with("-nothinking") {
                (4.0, false)
            } else {
                (3.5 / 0.15, false)
            }
        } else if model.starts_with("gemini-2.5-flash-lite") {
            (4.0, false)
        } else if model.starts_with("gemini-2.5-flash")
            || model.starts_with("gemini-robotics-er-1.5")
        {
            (2.5 / 0.3, false)
        } else if model.starts_with("gemini-3-pro-image") {
            (60.0, false)
        } else if model.starts_with("gemini-3-pro") {
            (6.0, false)
        } else {
            (4.0, false)
        }
    } else if model.starts_with("command") {
        match model {
            "command-r" => (3.0, true),
            "command-r-plus" => (5.0, true),
            "command-r-08-2024" | "command-r-plus-08-2024" => (4.0, true),
            _ => (4.0, false),
        }
    } else if [
        "ERNIE-Speed-",
        "ERNIE-Lite-",
        "ERNIE-Character",
        "ERNIE-Functions",
    ]
    .iter()
    .any(|prefix| model.starts_with(prefix))
    {
        (2.0, true)
    } else {
        match model {
            "llama2-70b-4096" => (0.8 / 0.64, true),
            "llama3-8b-8192" => (2.0, true),
            "llama3-70b-8192" => (0.79 / 0.59, true),
            _ => (1.0, false),
        }
    };
    (
        Decimal::from_str(&ratio.to_string()).unwrap_or(Decimal::ONE),
        locked,
    )
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
pub(super) struct Evidence {
    pub usage: Usage,
    pub completed: bool,
    pub terminal: bool,
    pub reported: bool,
    pub tools: BTreeMap<String, i64>,
    pub observed: bool,
}

pub(super) struct UsageTracker {
    endpoint: OpenAiRelayEndpoint,
    parser: SseFrameParser,
    pub evidence: Evidence,
    finish_reason: bool,
    invalid: bool,
    model: String,
    estimated_input: i64,
    output: String,
    tool_count: i64,
    tools: ToolCalls,
}

impl UsageTracker {
    pub fn new(endpoint: OpenAiRelayEndpoint) -> Self {
        Self {
            endpoint,
            parser: SseFrameParser::default(),
            evidence: Evidence::default(),
            finish_reason: false,
            invalid: false,
            model: String::new(),
            estimated_input: 0,
            output: String::new(),
            tool_count: 0,
            tools: ToolCalls::default(),
        }
    }

    pub fn with_input(mut self, model: String, estimated_input: i64) -> Self {
        self.model = model;
        self.estimated_input = estimated_input;
        self
    }

    pub fn with_request(mut self, raw: &[u8]) -> Self {
        self.tools = ToolCalls::from_request(raw);
        self
    }

    pub fn finalize(&mut self) {
        self.evidence.tools = self.tools.final_counts(&self.model, self.responses());
        if self.model.is_empty() {
            return;
        }
        if self.responses() {
            if !self.evidence.reported && !self.output.is_empty() {
                self.evidence.usage.output =
                    super::token_count::count_text(&self.output, &self.model);
                if self.evidence.usage.output > 0 {
                    self.evidence.usage.input = self.estimated_input.clamp(0, 1_050_000);
                }
            }
        } else if !self.evidence.usage.measured()
            && (self.evidence.completed || !self.output.is_empty() || self.tool_count > 0)
        {
            self.evidence.usage.input = self.estimated_input;
            self.evidence.usage.output =
                super::token_count::estimate_text(&self.output, &self.model)
                    .saturating_add(self.tool_count.saturating_mul(7));
        }
    }

    pub fn feed(&mut self, bytes: &[u8]) {
        if self.evidence.terminal || self.invalid {
            return;
        }
        // Feed complete lines separately, so garbage following a completed
        // event in the same network chunk cannot erase its measured usage.
        for line in bytes.split_inclusive(|byte| *byte == b'\n' || *byte == b'\r') {
            match self.parser.feed(line) {
                Ok(frames) => {
                    for frame in frames {
                        if frame.is_done() {
                            if !self.responses() {
                                self.evidence.terminal = true;
                                self.evidence.completed = true;
                            }
                        } else if frame.has_data && !frame.data.is_empty() {
                            match serde_json::from_str::<Value>(&frame.data) {
                                Ok(value) => self.observe(&value, true),
                                Err(_) => self.invalid = true,
                            }
                        }
                        if self.evidence.terminal || self.invalid {
                            return;
                        }
                    }
                }
                Err(_) => {
                    self.invalid = true;
                    return;
                }
            }
        }
    }

    fn responses(&self) -> bool {
        matches!(
            self.endpoint,
            OpenAiRelayEndpoint::Responses | OpenAiRelayEndpoint::ResponsesCompact
        )
    }

    pub fn json(&mut self, bytes: &[u8]) -> Result<Option<Vec<u8>>, OpenAiRelayFailure> {
        let mut value: Value = serde_json::from_slice(bytes).map_err(|_| {
            OpenAiRelayFailure::new(
                StatusCode::INTERNAL_SERVER_ERROR,
                "bad_response_body",
                "invalid upstream response body",
            )
        })?;
        if !value.is_object() || value.get("error").is_some_and(|value| !value.is_null()) {
            return Err(OpenAiRelayFailure::new(
                StatusCode::BAD_GATEWAY,
                "bad_response_body",
                "upstream returned an error response",
            ));
        }
        self.observe(&value, false);
        if !self.responses() && !self.model.is_empty() && self.evidence.usage.input == 0 {
            let mut completion = self.evidence.usage.output;
            if completion == 0
                && let Some(choices) = value.get("choices").and_then(Value::as_array)
            {
                for choice in choices {
                    let message = &choice["message"];
                    let mut text = String::new();
                    if let Some(content) = message.get("content") {
                        if let Some(content) = content.as_str() {
                            text.push_str(content);
                        } else if let Some(parts) = content.as_array() {
                            for part in parts {
                                if part.get("type").and_then(Value::as_str) == Some("text")
                                    && let Some(content) = part.get("text").and_then(Value::as_str)
                                {
                                    text.push_str(content);
                                }
                            }
                        }
                    }
                    if let Some(reasoning) = message
                        .get("reasoning_content")
                        .filter(|value| !value.is_null())
                        .or_else(|| message.get("reasoning"))
                        .and_then(Value::as_str)
                    {
                        text.push_str(reasoning);
                    }
                    completion = completion
                        .saturating_add(super::token_count::count_text(&text, &self.model));
                }
            }
            self.evidence.usage = Usage {
                input: self.estimated_input,
                output: completion,
                ..Default::default()
            };
            let usage = self.evidence.usage;
            // Match Go's replacement dto.Usage, while preserving unknown
            // response fields outside usage. Null/zero prompt triggers this
            // even when the provider already reported completion tokens.
            value["usage"] = serde_json::json!({
                "prompt_tokens":usage.input,"completion_tokens":usage.output,
                "total_tokens":usage.input.saturating_add(usage.output),
                "prompt_tokens_details":{"cached_tokens":0,"text_tokens":0,"audio_tokens":0,"image_tokens":0},
                "completion_tokens_details":{"text_tokens":0,"audio_tokens":0,"image_tokens":0,"reasoning_tokens":0},
                "input_tokens":0,"output_tokens":0,"input_tokens_details":null,
                "claude_cache_creation_5_m_tokens":0,"claude_cache_creation_1_h_tokens":0
            });
            return serde_json::to_vec(&value)
                .map(Some)
                .map_err(|_| super::internal_failure());
        }
        Ok(None)
    }

    fn observe(&mut self, value: &Value, stream: bool) {
        self.evidence.observed = true;
        let responses = self.responses();
        self.tools.observe(value, responses, stream);
        let kind = value
            .get("type")
            .and_then(Value::as_str)
            .unwrap_or_default();
        let response = if stream && self.responses() {
            value.get("response").unwrap_or(value)
        } else {
            value
        };
        let terminal = !stream
            || matches!(
                kind,
                "response.completed"
                    | "response.done"
                    | "response.failed"
                    | "response.incomplete"
                    | "response.cancelled"
                    | "response.canceled"
                    | "error"
            );
        if stream
            && self.responses()
            && matches!(
                kind,
                "response.output_text.delta"
                    | "response.function_call_arguments.delta"
                    | "response.reasoning_text.delta"
                    | "response.reasoning_summary_text.delta"
            )
        {
            if let Some(delta) = value.get("delta").and_then(Value::as_str) {
                self.output.push_str(delta);
            }
        } else if stream
            && !self.responses()
            && let Some(choices) = value.get("choices").and_then(Value::as_array)
        {
            for choice in choices {
                let delta = choice.get("delta").unwrap_or(choice);
                for key in ["content", "text"] {
                    if let Some(text) = delta.get(key).and_then(Value::as_str) {
                        self.output.push_str(text);
                    }
                }
                if let Some(reasoning) = delta
                    .get("reasoning_content")
                    .or_else(|| delta.get("reasoning"))
                    .and_then(Value::as_str)
                {
                    self.output.push_str(reasoning);
                }
                if let Some(tools) = delta.get("tool_calls").and_then(Value::as_array) {
                    self.tool_count = self
                        .tool_count
                        .max(i64::try_from(tools.len()).unwrap_or(i64::MAX));
                    for tool in tools {
                        for key in ["name", "arguments"] {
                            if let Some(text) = tool
                                .get("function")
                                .and_then(|function| function.get(key))
                                .and_then(Value::as_str)
                            {
                                self.output.push_str(text);
                            }
                        }
                    }
                }
            }
        }
        if let Some(usage) = response.get("usage").filter(|usage| usage.is_object()) {
            let usage = Usage::parse(usage);
            // A zero lifecycle placeholder must not replace measured usage;
            // terminal zero is authoritative, exactly as in Go Responses.
            if usage.measured() || terminal || !self.responses() {
                self.evidence.usage = usage;
                self.evidence.reported = true;
            }
        }
        if let Some(choices) = value.get("choices").and_then(Value::as_array) {
            self.finish_reason |= choices.iter().any(|choice| {
                choice
                    .get("finish_reason")
                    .is_some_and(|reason| !reason.is_null())
            });
        }
        if terminal {
            self.evidence.terminal = true;
            let failed = matches!(
                kind,
                "response.failed"
                    | "response.incomplete"
                    | "response.cancelled"
                    | "response.canceled"
                    | "error"
            ) || response.get("status").and_then(Value::as_str).is_some_and(
                |status| {
                    matches!(
                        status.trim().to_ascii_lowercase().as_str(),
                        "failed" | "incomplete" | "cancelled" | "canceled"
                    )
                },
            ) || value.get("error").is_some_and(|error| !error.is_null());
            self.evidence.completed = !failed;
        }
    }

    pub fn eof(&mut self, clean: bool) {
        if self.evidence.terminal {
            return;
        }
        // A transport error/cancellation is never a normal completion.
        self.evidence.completed = clean && !self.invalid && !self.responses() && self.finish_reason;
    }
}

#[cfg(test)]
mod tests;
