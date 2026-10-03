//! Provider-origin model declarations are diagnostics, never routing or pricing inputs.

use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub(super) struct ResponseModelObservation {
    pub requested_model: String,
    pub upstream_model: String,
    pub returned_model: String,
}

impl ResponseModelObservation {
    pub fn new(requested_model: String, upstream_model: String) -> Self {
        Self {
            requested_model,
            upstream_model,
            returned_model: String::new(),
        }
    }

    pub fn mismatch(&self) -> bool {
        !self.returned_model.trim().is_empty() && !self.matches(&self.returned_model)
    }

    fn matches(&self, returned: &str) -> bool {
        let returned = canonical_model(returned);
        if returned.is_empty() {
            return false;
        }
        [&self.requested_model, &self.upstream_model]
            .into_iter()
            .map(|expected| canonical_model(expected))
            .filter(|expected| !expected.is_empty())
            .any(|expected| {
                returned == expected
                    || returned
                        .strip_prefix(&expected)
                        .is_some_and(compatible_variant_suffix)
            })
    }

    pub fn observe(&mut self, returned: &str) {
        if returned.trim().is_empty() || self.mismatch() {
            return;
        }
        // Keep the first informative compatible alias. A genuine mismatch can
        // replace it, and then no later matching or different event erases it.
        if self.matches(returned)
            && !self.returned_model.is_empty()
            && self.returned_model != self.requested_model
            && self.returned_model != self.upstream_model
        {
            return;
        }
        self.returned_model = returned.to_owned();
    }

    pub fn useful(&self) -> bool {
        !self.returned_model.trim().is_empty()
            && !(self.returned_model == self.requested_model
                && (self.upstream_model.is_empty() || self.upstream_model == self.requested_model))
    }
}

fn canonical_model(model: &str) -> String {
    model
        .trim()
        .to_lowercase()
        .rsplit('/')
        .next()
        .unwrap_or_default()
        .to_owned()
}

fn compatible_variant_suffix(suffix: &str) -> bool {
    match suffix {
        "-latest" | "-preview" => true,
        _ => suffix.strip_prefix('-').is_some_and(|suffix| {
            date(suffix)
                || suffix.strip_prefix("preview-").is_some_and(|date| {
                    self::date(date)
                        || separated_digits(date, &[2, 2])
                        || separated_digits(date, &[2, 4])
                })
        }),
    }
}

fn date(value: &str) -> bool {
    (value.len() == 8 && value.bytes().all(|byte| byte.is_ascii_digit()))
        || separated_digits(value, &[4, 2, 2])
}

fn separated_digits(value: &str, widths: &[usize]) -> bool {
    let mut parts = value.split('-');
    widths.iter().all(|width| {
        parts.next().is_some_and(|part| {
            part.len() == *width && part.bytes().all(|byte| byte.is_ascii_digit())
        })
    }) && parts.next().is_none()
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[derive(Deserialize)]
    struct CompatibilityCase {
        name: String,
        requested: String,
        selected: String,
        returned: String,
        mismatch: bool,
    }

    #[test]
    fn compatibility_matches_the_shared_go_and_web_contract() {
        let cases: Vec<CompatibilityCase> = serde_json::from_str(include_str!(
            "../../../../api-go/relay/common/testdata/response_model_compatibility.json"
        ))
        .unwrap();
        assert!(
            cases.len() >= 20,
            "the shared compatibility fixture must remain complete"
        );
        for case in cases {
            let mut observation = ResponseModelObservation::new(case.requested, case.selected);
            observation.observe(&case.returned);
            assert_eq!(observation.mismatch(), case.mismatch, "{}", case.name);
            let stored = serde_json::to_value(&observation).unwrap();
            assert_eq!(stored.as_object().unwrap().len(), 3);
            assert!(stored.get("mismatch").is_none());
        }
    }

    #[test]
    fn first_alias_and_first_genuine_mismatch_survive_later_events() {
        let mut observation = ResponseModelObservation::new("requested".into(), "mapped".into());
        for returned in [
            "mapped",
            "vendor/REQUESTED-preview",
            "",
            "requested",
            "mapped-latest",
        ] {
            observation.observe(returned);
        }
        assert_eq!(observation.returned_model, "vendor/REQUESTED-preview");
        assert!(!observation.mismatch());
        for returned in ["other", "requested", "", "another", "vendor/mapped"] {
            observation.observe(returned);
        }
        assert_eq!(observation.returned_model, "other");
        assert!(observation.mismatch());
        assert_eq!(observation.requested_model, "requested");
        assert_eq!(observation.upstream_model, "mapped");
    }

    #[test]
    fn only_useful_observations_become_metadata_and_mismatch_is_not_stored() {
        for (requested, selected, returned, useful) in [
            ("requested", "requested", "requested", false),
            ("requested", "", "requested", false),
            ("requested", "requested", "", false),
            ("requested", "requested", " \t", false),
            ("requested", "requested", "REQUESTED", true),
            ("requested", "requested", "vendor/requested", true),
            ("requested", "mapped", "requested", true),
            ("requested", "mapped", "mapped", true),
            ("requested", "mapped", "other", true),
        ] {
            let mut observation = ResponseModelObservation::new(requested.into(), selected.into());
            observation.observe(returned);
            assert_eq!(
                observation.useful(),
                useful,
                "{requested}/{selected}/{returned}"
            );
        }
        // A stale historical flag has no influence on the current comparison.
        let observation: ResponseModelObservation = serde_json::from_value(json!({
            "requested_model":"requested", "upstream_model":"mapped",
            "returned_model":"vendor/mapped", "mismatch":true
        }))
        .unwrap();
        assert!(!observation.mismatch());
        assert!(
            serde_json::to_value(observation)
                .unwrap()
                .get("mismatch")
                .is_none()
        );
    }
}
