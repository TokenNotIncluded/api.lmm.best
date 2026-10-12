use super::Bridge;
use crate::{
    billing::charging::Price,
    relay::{
        ClientProtocol, RelayError, RelayFuture, Request, RequestContext, Route, RouteProvider,
        UpstreamProtocol,
    },
    routing::{
        Attempt, CredentialRef, PayloadPolicy, PriceQuote, Protocol, ReplayPermission, RouteRequest,
    },
};
use std::{collections::BTreeMap, sync::Arc};

/// Rust-host secret material. Deliberately has no Debug/Serialize implementation.
pub struct UpstreamCredential {
    pub reference: CredentialRef,
    pub secret: String,
}

#[derive(Clone)]
pub(super) struct Selected {
    pub attempt: Arc<Attempt>,
    pub price: Price,
    pub route_id: String,
}

pub(super) fn credential_map(
    values: Vec<UpstreamCredential>,
) -> Result<BTreeMap<(u64, u64), String>, RelayError> {
    let mut result = BTreeMap::new();
    for value in values {
        if value.reference.id == 0
            || value.reference.revision == 0
            || value.secret.is_empty()
            || value.secret.len() > 4096
            || result
                .insert((value.reference.id, value.reference.revision), value.secret)
                .is_some()
        {
            return Err(RelayError::Invalid(
                "invalid upstream credential references",
            ));
        }
    }
    Ok(result)
}

/// Convert only prices that charging can represent EXACTLY. 1 USD is 500000
/// credits, hence one micro-USD is half a credit. Never round each token rate.
pub(super) fn credit_price(
    quote: PriceQuote,
    model: &str,
    group: &str,
) -> Result<Price, RelayError> {
    let rates = quote.rates();
    if quote.billing_mode() == crate::routing::BillingMode::UpstreamCost {
        let (group_ppm, _) = quote.multipliers_ppm();
        let price = Price {
            upstream_cost_group_ppm: Some(group_ppm),
            version: format!("routing-{}-rule-{}", quote.version(), quote.rule_id()),
            model: model.to_owned(),
            group: group.to_owned(),
            input_per_million: 0,
            cached_per_million: 0,
            output_per_million: 0,
        };
        price.validate().map_err(super::billing::error)?;
        return Ok(price);
    }
    if rates.request != 0 || rates.cache_write != 0 {
        return Err(RelayError::Unsupported(
            "price dimensions require a compatible charging contract",
        ));
    }
    let (group_ppm, speed_ppm) = quote.multipliers_ppm();
    let convert = |rate: u64| -> Result<i64, RelayError> {
        let numerator = u128::from(rate)
            .checked_mul(u128::from(group_ppm))
            .and_then(|v| v.checked_mul(u128::from(speed_ppm)))
            .ok_or(RelayError::BillingUnavailable)?;
        const DENOMINATOR: u128 = 2_000_000_000_000;
        if numerator % DENOMINATOR != 0 {
            return Err(RelayError::Unsupported(
                "price cannot be represented exactly in integer credits",
            ));
        }
        i64::try_from(numerator / DENOMINATOR).map_err(|_| RelayError::BillingUnavailable)
    };
    let price = Price {
        upstream_cost_group_ppm: None,
        version: format!("routing-{}-rule-{}", quote.version(), quote.rule_id()),
        model: model.to_owned(),
        group: group.to_owned(),
        input_per_million: convert(rates.input)?,
        cached_per_million: convert(rates.cache_read)?,
        output_per_million: convert(rates.output)?,
    };
    price.validate().map_err(super::billing::error)?;
    Ok(price)
}

impl RouteProvider for Bridge {
    fn select<'a>(
        &'a self,
        context: &'a RequestContext,
        request: &'a Request,
    ) -> RelayFuture<'a, Route> {
        Box::pin(async move {
            let group = self
                .active
                .lock()
                .map_err(|_| RelayError::BillingUnavailable)?
                .get(&context.request_id)
                .ok_or(RelayError::Forbidden)?
                .group
                .clone();
            let protocol = match request.client {
                ClientProtocol::Chat => Protocol::OpenAiChat,
                ClientProtocol::Responses => Protocol::OpenAiResponses,
            };
            let mut plan = self
                .routing
                .route(RouteRequest {
                    model: &request.model,
                    group: &group,
                    speed: None,
                    protocol,
                    replay: ReplayPermission::Denied,
                })
                .map_err(|_| RelayError::RouteUnavailable)?;
            let price = credit_price(plan.quote(), plan.canonical_model(), &group)?;
            let attempt = plan.start().map_err(|_| RelayError::RouteUnavailable)?;
            if !attempt.is_available()
                || attempt.payload_policy() != PayloadPolicy::Adapt
                || u128::from(attempt.timeout_ms()) < self.total_timeout.as_millis()
            {
                return Err(RelayError::RouteUnavailable);
            }
            let upstream = match attempt.protocol() {
                Protocol::OpenAiChat => UpstreamProtocol::Chat,
                Protocol::OpenAiResponses => UpstreamProtocol::Responses,
                _ => {
                    return Err(RelayError::Unsupported(
                        "upstream protocol is not integrated with charging",
                    ));
                }
            };
            let reference = attempt.credential_ref();
            let secret = self
                .credentials
                .get(&(reference.id, reference.revision))
                .ok_or(RelayError::RouteUnavailable)?;
            let mut endpoint = reqwest::Url::parse(attempt.endpoint())
                .map_err(|_| RelayError::RouteUnavailable)?;
            let local = endpoint
                .host_str()
                .and_then(|host| {
                    host.trim_matches(['[', ']'])
                        .parse::<std::net::IpAddr>()
                        .ok()
                })
                .is_some_and(|ip| ip.is_loopback());
            if price.upstream_cost_group_ppm.is_some() && attempt.upstream_model() != price.model {
                return Err(RelayError::Unsupported(
                    "upstream-cost router cannot be mapped to another model",
                ));
            }
            if !local {
                return Err(RelayError::Unsupported(
                    "external providers require validated model context bounds",
                ));
            }
            // Configured base already carries the provider's /v1 prefix, if any.
            // Append path segments rather than dropping that prefix with join().
            {
                let mut segments = endpoint
                    .path_segments_mut()
                    .map_err(|_| RelayError::RouteUnavailable)?;
                segments.pop_if_empty();
                match upstream {
                    UpstreamProtocol::Chat => {
                        segments.push("chat").push("completions");
                    }
                    UpstreamProtocol::Responses => {
                        segments.push("responses");
                    }
                    _ => unreachable!(),
                }
            }
            let route_id = format!(
                "r{}-t{}-u{}",
                attempt.route_id(),
                attempt.target_id(),
                attempt.upstream_id()
            );
            let attempt = Arc::new(attempt);
            let availability = attempt.clone();
            let route = Route::new(
                route_id.clone(),
                upstream,
                endpoint.as_str(),
                attempt.upstream_model().to_owned(),
                secret,
                attempt.quote().version(),
            )?
            .with_availability_guard(Arc::new(move || availability.is_available()));
            let mut active = self
                .active
                .lock()
                .map_err(|_| RelayError::BillingUnavailable)?;
            let operation = active
                .get_mut(&context.request_id)
                .ok_or(RelayError::Forbidden)?;
            if operation.selected.is_some() {
                return Err(RelayError::Conflict);
            }
            operation.selected = Some(Selected {
                attempt,
                price,
                route_id,
            });
            Ok(route)
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::routing::{
        MULTIPLIER_ONE, PriceConfig, PriceRule, PriceSnapshot, Speed, TokenRates,
    };

    fn quote(input: u64, cache_write: u64, request: u64, multiplier: u32) -> PriceQuote {
        PriceSnapshot::compile(PriceConfig {
            version: 3,
            rules: vec![PriceRule {
                billing_mode: crate::routing::BillingMode::Tokens,
                id: 1,
                model: "test".into(),
                group: "default".into(),
                speed: Speed::Standard,
                rates: TokenRates {
                    input,
                    output: 4_000_000,
                    cache_read: 500_000,
                    cache_write,
                    request,
                },
                group_multiplier_ppm: multiplier,
                speed_multiplier_ppm: MULTIPLIER_ONE,
            }],
        })
        .unwrap()
        .quote("test", "default", Speed::Standard)
        .unwrap()
    }

    #[test]
    fn exact_integer_credits_and_multipliers_only() {
        let price =
            credit_price(quote(2_000_000, 0, 0, MULTIPLIER_ONE), "test", "default").unwrap();
        assert_eq!(
            (
                price.input_per_million,
                price.cached_per_million,
                price.output_per_million
            ),
            (1_000_000, 250_000, 2_000_000)
        );
        assert!(credit_price(quote(1, 0, 0, MULTIPLIER_ONE), "test", "default").is_err());
        assert!(credit_price(quote(2_000_000, 1, 0, MULTIPLIER_ONE), "test", "default").is_err());
        assert!(credit_price(quote(2_000_000, 0, 1, MULTIPLIER_ONE), "test", "default").is_err());
        assert!(credit_price(quote(2, 0, 0, 1), "test", "default").is_err());
        assert_eq!(
            credit_price(
                quote(2_000_000, 0, 0, MULTIPLIER_ONE * 2),
                "test",
                "default"
            )
            .unwrap()
            .input_per_million,
            2_000_000
        );
    }
}
