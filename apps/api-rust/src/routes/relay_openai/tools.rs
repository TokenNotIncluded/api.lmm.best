//! Go tool-call prices and observations. Declarations authorize a tool; only
//! provider call events count, and failed image turns discard pending images.

use rust_decimal::Decimal;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use std::collections::{BTreeMap, BTreeSet};

#[derive(Clone, Debug, Default)]
pub(super) struct ToolPrices(BTreeMap<String, Decimal>);

impl ToolPrices {
    pub fn configured(raw: Option<&str>) -> Self {
        let mut values = BTreeMap::new();
        for (name, price) in [
            ("web_search", 100),
            ("web_search_preview", 100),
            ("file_search", 25),
            ("google_search", 140),
            ("image_generation", 1500),
            ("web_search_preview:gpt-4o*", 250),
            ("web_search_preview:gpt-4.1*", 250),
            ("web_search_preview:gpt-4o-mini*", 250),
            ("web_search_preview:gpt-4.1-mini*", 250),
        ] {
            values.insert(name.to_owned(), Decimal::new(price, 1));
        }
        if let Some(Value::Object(overrides)) = raw.and_then(|raw| serde_json::from_str(raw).ok()) {
            for (name, value) in overrides {
                if let Some(price) = value
                    .as_f64()
                    .filter(|price| price.is_finite() && *price >= 0.0)
                    && let Ok(price) = Decimal::from_str_exact(&price.to_string())
                        .or_else(|_| Decimal::from_scientific(&price.to_string()))
                {
                    values.insert(name, price);
                }
            }
        }
        Self(values)
    }

    pub fn price(&self, name: &str, model: &str) -> Decimal {
        let mut selected: Option<(&str, Decimal)> = None;
        if !model.is_empty() {
            for (key, value) in &self.0 {
                if let Some((tool, prefix)) = key.split_once(':') {
                    let prefix = prefix.strip_suffix('*').unwrap_or(prefix);
                    if tool == name
                        && model.starts_with(prefix)
                        && selected.is_none_or(|(current, _)| prefix.len() > current.len())
                    {
                        selected = Some((prefix, *value));
                    }
                }
            }
        }
        selected
            .map(|(_, value)| value)
            .or_else(|| self.0.get(name).copied())
            .unwrap_or_default()
    }

    pub fn surcharge(
        &self,
        calls: &BTreeMap<String, i64>,
        model: &str,
        group: Decimal,
        unit: Decimal,
    ) -> Decimal {
        if group.is_zero() || unit.is_zero() {
            return Decimal::ZERO;
        }
        calls
            .iter()
            .filter(|(_, count)| **count > 0)
            .fold(Decimal::ZERO, |total, (name, count)| {
                let line = self
                    .price(name, model)
                    .checked_mul(Decimal::from(*count))
                    .and_then(|value| value.checked_div(Decimal::from(1000)))
                    .and_then(|value| value.checked_mul(group))
                    .and_then(|value| value.checked_mul(unit))
                    .unwrap_or(Decimal::MAX);
                total.checked_add(line).unwrap_or(Decimal::MAX)
            })
    }

    pub fn snapshot(&self) -> Value {
        json!(
            self.0
                .iter()
                .map(|(name, price)| (name.clone(), price.to_string()))
                .collect::<BTreeMap<_, _>>()
        )
    }

    pub fn log_items(&self, calls: &BTreeMap<String, i64>, model: &str) -> Vec<Value> {
        calls
            .iter()
            .filter_map(|(name, count)| {
                let price = self.price(name, model);
                (*count > 0 && price > Decimal::ZERO).then(|| {
                    json!({"name":name,"count":count,
                "price":serde_json::from_str::<Value>(&price.normalize().to_string()).unwrap_or(Value::Null)})
                })
            })
            .collect()
    }
}

#[derive(Default)]
pub(super) struct ToolCalls {
    pub counts: BTreeMap<String, i64>,
    declarations: BTreeSet<String>,
    images: BTreeSet<String>,
    image_count: i64,
    image_committed: bool,
    chat_functions: BTreeSet<(i64, i64)>,
    started: bool,
}

impl ToolCalls {
    pub fn from_request(raw: &[u8]) -> Self {
        let mut result = Self::default();
        if let Ok(request) = serde_json::from_slice::<Value>(raw)
            && let Some(tools) = request.get("tools").and_then(Value::as_array)
        {
            result.declarations.extend(
                tools
                    .iter()
                    .filter_map(|tool| tool.get("type").and_then(Value::as_str))
                    .map(str::to_owned),
            );
        }
        result
    }

    fn increment(&mut self, name: &str) {
        let count = self.counts.entry(name.to_owned()).or_default();
        *count = count.saturating_add(1);
    }

    fn call(&mut self, kind: &str, name: &str) {
        match kind {
            "web_search_call" => {
                let name = if self.declarations.contains("web_search_preview")
                    || !self.declarations.contains("web_search")
                {
                    "web_search_preview"
                } else {
                    "web_search"
                };
                self.increment(name);
            }
            "file_search_call" => self.increment("file_search"),
            "function_call" | "tool_use"
                if !name.is_empty()
                    && !matches!(
                        name,
                        "web_search"
                            | "web_search_preview"
                            | "file_search"
                            | "google_search"
                            | "image_generation"
                    ) =>
            {
                self.increment(name)
            }
            _ => {}
        }
    }

    fn image(&mut self, item: &Value, index: Option<i64>) {
        if self.image_committed
            || self.image_count >= 128
            || item.get("type").and_then(Value::as_str) != Some("image_generation_call")
        {
            return;
        }
        let Some(result) = item
            .get("result")
            .and_then(Value::as_str)
            .filter(|result| !result.trim().is_empty())
        else {
            return;
        };
        let status = item
            .get("status")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .trim()
            .to_ascii_lowercase();
        if matches!(
            status.as_str(),
            "failed" | "cancelled" | "canceled" | "incomplete" | "partial"
        ) {
            return;
        }
        let mut aliases = vec![format!(
            "result:{}",
            hex::encode(Sha256::digest(result.as_bytes()))
        )];
        for key in ["id", "call_id"] {
            if let Some(value) = item
                .get(key)
                .and_then(Value::as_str)
                .filter(|value| !value.is_empty())
            {
                aliases.push(format!(
                    "{key}:{}",
                    hex::encode(Sha256::digest(value.as_bytes()))
                ));
            }
        }
        if let Some(index) = index.filter(|index| *index >= 0) {
            aliases.push(format!("index:{index}"));
        }
        if aliases.iter().any(|alias| self.images.contains(alias)) {
            return;
        }
        self.images.extend(aliases);
        self.image_count += 1;
    }

    pub fn observe(&mut self, value: &Value, responses: bool, stream: bool) {
        self.started = true;
        if responses {
            let kind = value
                .get("type")
                .and_then(Value::as_str)
                .unwrap_or_default();
            if stream
                && kind == "response.output_item.done"
                && let Some(item) = value.get("item")
            {
                self.call(
                    item.get("type").and_then(Value::as_str).unwrap_or_default(),
                    item.get("name").and_then(Value::as_str).unwrap_or_default(),
                );
                self.image(item, value.get("output_index").and_then(Value::as_i64));
            }
            if !stream
                || matches!(
                    kind,
                    "response.completed"
                        | "response.done"
                        | "response.failed"
                        | "response.incomplete"
                        | "response.cancelled"
                        | "response.canceled"
                )
            {
                let response = if stream {
                    value.get("response").unwrap_or(value)
                } else {
                    value
                };
                let status = response
                    .get("status")
                    .and_then(Value::as_str)
                    .unwrap_or_default()
                    .trim()
                    .to_ascii_lowercase();
                let failed = matches!(
                    kind,
                    "response.failed"
                        | "response.incomplete"
                        | "response.cancelled"
                        | "response.canceled"
                ) || matches!(
                    status.as_str(),
                    "failed" | "incomplete" | "cancelled" | "canceled"
                );
                if let Some(outputs) = response.get("output").and_then(Value::as_array) {
                    for (index, item) in outputs.iter().enumerate() {
                        if !stream {
                            self.call(
                                item.get("type").and_then(Value::as_str).unwrap_or_default(),
                                item.get("name").and_then(Value::as_str).unwrap_or_default(),
                            );
                        }
                        if !failed {
                            self.image(item, i64::try_from(index).ok());
                        }
                    }
                }
                self.counts.insert(
                    "image_generation".to_owned(),
                    if failed { 0 } else { self.image_count },
                );
                self.image_committed = true;
            }
        } else if let Some(choices) = value.get("choices").and_then(Value::as_array) {
            for choice in choices {
                let message = choice
                    .get(if stream { "delta" } else { "message" })
                    .unwrap_or(choice);
                if let Some(tools) = message.get("tool_calls").and_then(Value::as_array) {
                    for (index, tool) in tools.iter().enumerate() {
                        let name = tool
                            .pointer("/function/name")
                            .and_then(Value::as_str)
                            .unwrap_or_default();
                        if name.is_empty() {
                            continue;
                        }
                        let identity = (
                            choice.get("index").and_then(Value::as_i64).unwrap_or(0),
                            tool.get("index")
                                .and_then(Value::as_i64)
                                .unwrap_or(index as i64),
                        );
                        if !stream || self.chat_functions.insert(identity) {
                            self.call("function_call", name);
                        }
                    }
                }
            }
        }
    }

    pub fn final_counts(&self, model: &str, responses: bool) -> BTreeMap<String, i64> {
        let mut counts = self.counts.clone();
        if self.started && !responses && model.ends_with("search-preview") {
            let count = counts.entry("web_search_preview".to_owned()).or_default();
            *count = count.saturating_add(1);
        }
        counts
    }
}

#[cfg(test)]
mod tests;
