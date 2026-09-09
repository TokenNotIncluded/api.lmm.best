//! Go option_price_lock.go compatibility for configuration writes.
use std::collections::{BTreeMap, BTreeSet};

use serde_json::{Map, Value};

const LOCK_KEY: &str = "ModelPriceLock";

fn normalized(key: &str) -> Option<bool> {
    match key {
        "ModelRatio" | "ModelPrice" | "CompletionRatio" | "AudioRatio" | "AudioCompletionRatio" => {
            Some(true)
        }
        "CacheRatio"
        | "CreateCacheRatio"
        | "ImageRatio"
        | "billing_setting.billing_mode"
        | "billing_setting.billing_expr" => Some(false),
        _ => None,
    }
}

fn object(value: &str) -> Result<Map<String, Value>, String> {
    match serde_json::from_str(value).map_err(|error| error.to_string())? {
        Value::Object(object) => Ok(object),
        Value::Null => Ok(Map::new()),
        _ => Err("must be a JSON object".to_owned()),
    }
}

fn locks(value: &str) -> Result<BTreeSet<String>, String> {
    if value.is_empty() {
        return Ok(BTreeSet::new());
    }
    let Value::Object(entries) =
        serde_json::from_str(value).map_err(|_| format!("{LOCK_KEY} must be a JSON object"))?
    else {
        return Err(format!("{LOCK_KEY} must be a JSON object"));
    };
    let mut locked = BTreeSet::new();
    for (name, value) in entries {
        if name.trim().is_empty() || !value.is_boolean() {
            return Err(format!(
                "{LOCK_KEY} must map non-empty model names to booleans"
            ));
        }
        if value == true {
            locked.insert(name);
        }
    }
    Ok(locked)
}

pub(super) fn validate(key: &str, value: &str) -> Result<(), String> {
    if key == LOCK_KEY {
        if value.is_empty() {
            return Err(format!("{LOCK_KEY} must be a JSON object"));
        }
        locks(value)?;
    } else if normalized(key).is_some() {
        let entries = object(value).map_err(|error| format!("{key}: {error}"))?;
        for entry in entries.values() {
            // Go's map unmarshaller accepts null as the element's zero value.
            let valid = entry.is_null()
                || if key.starts_with("billing_setting.") {
                    entry.is_string()
                } else {
                    entry.as_f64().is_some_and(f64::is_finite)
                };
            if !valid {
                return Err(format!("{key}: invalid pricing value"));
            }
        }
    }
    Ok(())
}

fn equal(left: &Value, right: &Value) -> bool {
    match (left, right) {
        (Value::Number(a), Value::Number(b)) => a.as_f64() == b.as_f64(),
        (Value::Array(a), Value::Array(b)) => {
            a.len() == b.len() && a.iter().zip(b).all(|(a, b)| equal(a, b))
        }
        (Value::Object(a), Value::Object(b)) => {
            a.len() == b.len()
                && a.iter()
                    .all(|(key, a)| b.get(key).is_some_and(|b| equal(a, b)))
        }
        _ => left == right,
    }
}

pub(super) fn filter(
    saved: &BTreeMap<String, String>,
    values: &mut BTreeMap<String, String>,
) -> Result<Vec<String>, String> {
    let locked = locks(saved.get(LOCK_KEY).map(String::as_str).unwrap_or(""))?;
    let aliases: BTreeSet<_> = locked
        .iter()
        .map(|name| crate::models::legacy_pricing_model_name(name))
        .collect();
    let mut changed = BTreeSet::new();
    for (key, value) in values.iter_mut() {
        let Some(normalize) = normalized(key) else {
            continue;
        };
        let mut candidate = object(value).map_err(|error| format!("{key}: {error}"))?;
        let previous = object(
            saved
                .get(key)
                .map(String::as_str)
                .filter(|value| !value.is_empty())
                .unwrap_or("{}"),
        )
        .map_err(|error| format!("saved {key}: {error}"))?;
        let names: BTreeSet<_> = previous.keys().chain(candidate.keys()).cloned().collect();
        let mut restored = false;
        for name in names {
            if !(locked.contains(&name)
                || normalize && aliases.contains(crate::models::legacy_pricing_model_name(&name)))
            {
                continue;
            }
            let before = previous.get(&name);
            let after = candidate.get(&name);
            if matches!((before, after), (Some(a), Some(b)) if equal(a, b)) {
                continue;
            }
            if let Some(before) = before {
                candidate.insert(name.clone(), before.clone());
            } else {
                candidate.remove(&name);
            }
            changed.insert(name);
            restored = true;
        }
        if restored {
            *value = Value::Object(candidate).to_string();
        }
    }
    Ok(changed
        .into_iter()
        .map(|name| format!("模型 {name} 的价格已锁定，已忽略本次价格修改"))
        .collect())
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn map(entries: &[(&str, &str)]) -> BTreeMap<String, String> {
        entries
            .iter()
            .map(|(k, v)| ((*k).to_owned(), (*v).to_owned()))
            .collect()
    }

    #[test]
    fn restores_locked_values_before_validation_and_requires_separate_unlock() {
        for key in [
            "ModelRatio",
            "ModelPrice",
            "CompletionRatio",
            "AudioRatio",
            "AudioCompletionRatio",
            "CacheRatio",
            "CreateCacheRatio",
            "ImageRatio",
            "billing_setting.billing_mode",
            "billing_setting.billing_expr",
        ] {
            let saved = map(&[
                (LOCK_KEY, r#"{"locked":true,"absent":true}"#),
                (key, r#"{"locked":1,"editable":2}"#),
            ]);
            let mut values = map(&[
                (LOCK_KEY, "{}"),
                (key, r#"{"locked":"invalid","editable":3,"absent":4}"#),
            ]);
            let warnings = filter(&saved, &mut values).unwrap();
            assert_eq!(warnings.len(), 2);
            assert_eq!(
                serde_json::from_str::<Value>(&values[key]).unwrap(),
                json!({"locked":1,"editable":3})
            );
            assert_eq!(values[LOCK_KEY], "{}");
        }
    }

    #[test]
    fn clearing_and_numeric_equivalence_match_go() {
        let saved = map(&[
            (LOCK_KEY, r#"{"locked":true}"#),
            ("ModelPrice", r#"{"locked":1}"#),
        ]);
        let mut values = map(&[("ModelPrice", r#"{"locked":1.0}"#)]);
        assert!(filter(&saved, &mut values).unwrap().is_empty());
        for clear in ["null", "{}"] {
            values.insert("ModelPrice".into(), clear.into());
            assert_eq!(filter(&saved, &mut values).unwrap().len(), 1);
            assert_eq!(values["ModelPrice"], r#"{"locked":1}"#);
        }
    }

    #[test]
    fn aliases_apply_only_to_go_normalized_maps() {
        let saved = map(&[(LOCK_KEY, r#"{"gemini-2.5-flash-thinking-100":true}"#)]);
        let mut values = map(&[
            ("ModelPrice", r#"{"gemini-2.5-flash-thinking-200":2}"#),
            ("CacheRatio", r#"{"gemini-2.5-flash-thinking-200":2}"#),
        ]);
        assert_eq!(filter(&saved, &mut values).unwrap().len(), 1);
        assert_eq!(values["ModelPrice"], "{}");
        assert_ne!(values["CacheRatio"], "{}");
    }

    #[test]
    fn validates_lock_and_price_types() {
        for value in [
            "",
            "null",
            "[]",
            r#"{"x":null}"#,
            r#"{"x":1}"#,
            r#"{" ":true}"#,
        ] {
            assert!(validate(LOCK_KEY, value).is_err(), "{value}");
        }
        for value in ["{}", r#"{"x":false}"#, r#"{"x":true}"#] {
            assert!(validate(LOCK_KEY, value).is_ok());
        }
        assert!(validate("ModelPrice", r#"{"x":"bad"}"#).is_err());
        assert!(validate("billing_setting.billing_expr", r#"{"x":1}"#).is_err());
        assert!(validate("ModelPrice", r#"{"x":null}"#).is_ok());
        assert!(filter(&BTreeMap::new(), &mut map(&[("ModelPrice", "[]")])).is_err());
    }
}
