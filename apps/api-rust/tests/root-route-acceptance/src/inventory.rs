use std::collections::{BTreeMap, BTreeSet};

const EXPECTED_ROUTE_COUNT: usize = 353;
const LEGACY_ROUTES: &str = include_str!("../../fixtures/routes/legacy-go-routes.tsv");
const MIGRATION_PLAN: &str = include_str!("../../fixtures/routes/migration-plan.tsv");
const PLAN_HEADER: &str = "method\tpath\tlegacy_handler\tdomain\tauth_scope\tdata_access\tstreaming\tpriority\tplanned_rust_module\tjob_dependency";

#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub enum AuthClass {
    Public,
    PublicOrUser,
    User,
    UserOrToken,
    Token,
    Admin,
    Root,
    Webhook,
}

impl AuthClass {
    fn parse(value: &str) -> Result<Self, String> {
        match value {
            "public" => Ok(Self::Public),
            "public-or-user" => Ok(Self::PublicOrUser),
            "user" => Ok(Self::User),
            "user-or-token" => Ok(Self::UserOrToken),
            "token" => Ok(Self::Token),
            "admin" => Ok(Self::Admin),
            "root" => Ok(Self::Root),
            "webhook" => Ok(Self::Webhook),
            _ => Err(format!("unsupported migration auth class: {value}")),
        }
    }

    #[cfg_attr(any(not(feature = "runtime"), test), allow(dead_code))]
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::Public => "public",
            Self::PublicOrUser => "public-or-user",
            Self::User => "user",
            Self::UserOrToken => "user-or-token",
            Self::Token => "token",
            Self::Admin => "admin",
            Self::Root => "root",
            Self::Webhook => "webhook",
        }
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RouteCase {
    pub method: String,
    pub path: String,
    pub handler: String,
    pub auth: AuthClass,
}

pub fn load_routes() -> Result<Vec<RouteCase>, String> {
    let mut baseline = BTreeMap::new();
    let mut ordered_keys = Vec::new();
    for (index, line) in LEGACY_ROUTES.lines().enumerate() {
        let columns = line.split('\t').collect::<Vec<_>>();
        if columns.len() != 3 {
            return Err(format!(
                "legacy route line {} has {} columns instead of 3",
                index + 1,
                columns.len()
            ));
        }
        validate_method_path(columns[0], columns[1], "legacy route", index + 1)?;
        let key = (columns[0].to_owned(), columns[1].to_owned());
        if baseline
            .insert(key.clone(), columns[2].to_owned())
            .is_some()
        {
            return Err(format!(
                "duplicate frozen route at line {}: {} {}",
                index + 1,
                columns[0],
                columns[1]
            ));
        }
        ordered_keys.push(key);
    }
    if baseline.len() != EXPECTED_ROUTE_COUNT {
        return Err(format!(
            "frozen route count is {}, expected {EXPECTED_ROUTE_COUNT}",
            baseline.len()
        ));
    }

    let mut plan_lines = MIGRATION_PLAN.lines();
    let header = plan_lines
        .next()
        .ok_or_else(|| "migration plan is empty".to_owned())?;
    if header != PLAN_HEADER {
        return Err(format!("migration plan header drifted: {header}"));
    }

    let mut planned = BTreeMap::new();
    for (index, line) in plan_lines.enumerate() {
        let line_number = index + 2;
        let columns = line.split('\t').collect::<Vec<_>>();
        if columns.len() != 10 {
            return Err(format!(
                "migration plan line {line_number} has {} columns instead of 10",
                columns.len()
            ));
        }
        validate_method_path(columns[0], columns[1], "migration plan", line_number)?;
        let key = (columns[0].to_owned(), columns[1].to_owned());
        let auth = AuthClass::parse(columns[4])?;
        if planned
            .insert(key.clone(), (columns[2].to_owned(), auth))
            .is_some()
        {
            return Err(format!(
                "duplicate migration route at line {line_number}: {} {}",
                columns[0], columns[1]
            ));
        }
    }

    if planned.len() != EXPECTED_ROUTE_COUNT {
        return Err(format!(
            "migration plan count is {}, expected {EXPECTED_ROUTE_COUNT}",
            planned.len()
        ));
    }

    let mut auth_counts = BTreeMap::new();
    let mut routes = Vec::with_capacity(EXPECTED_ROUTE_COUNT);
    for (method, path) in ordered_keys {
        let handler = baseline
            .get(&(method.clone(), path.clone()))
            .expect("ordered frozen key exists");
        let (planned_handler, auth) = planned
            .get(&(method.clone(), path.clone()))
            .ok_or_else(|| format!("migration plan is missing {method} {path}"))?;
        if handler != planned_handler {
            return Err(format!(
                "legacy handler drift for {method} {path}: baseline={handler} plan={planned_handler}"
            ));
        }
        *auth_counts.entry(*auth).or_insert(0usize) += 1;
        routes.push(RouteCase {
            method,
            path,
            handler: handler.clone(),
            auth: *auth,
        });
    }

    let expected_auth_counts = BTreeMap::from([
        (AuthClass::Public, 21usize),
        (AuthClass::PublicOrUser, 14usize),
        (AuthClass::User, 80usize),
        (AuthClass::UserOrToken, 40usize),
        (AuthClass::Token, 47usize),
        (AuthClass::Admin, 141usize),
        (AuthClass::Root, 2usize),
        (AuthClass::Webhook, 8usize),
    ]);
    if auth_counts != expected_auth_counts {
        return Err(format!(
            "migration auth-class counts drifted: actual={auth_counts:?} expected={expected_auth_counts:?}"
        ));
    }

    Ok(routes)
}

fn validate_method_path(method: &str, path: &str, source: &str, line: usize) -> Result<(), String> {
    if !matches!(method, "GET" | "POST" | "PUT" | "DELETE" | "PATCH") {
        return Err(format!("{source} line {line} has invalid method {method}"));
    }
    if !path.starts_with('/') || path.contains('{') || path.contains('}') {
        return Err(format!("{source} line {line} has invalid path {path}"));
    }
    Ok(())
}

pub fn concrete_path(pattern: &str) -> String {
    pattern
        .split('/')
        .map(|segment| match segment.strip_prefix(':') {
            Some(name) => parameter_value(name),
            None => match segment.strip_prefix('*') {
                Some(name) => wildcard_value(name),
                None => segment.to_owned(),
            },
        })
        .collect::<Vec<_>>()
        .join("/")
}

pub fn axum_path(pattern: &str) -> String {
    pattern
        .split('/')
        .map(|segment| match segment.strip_prefix(':') {
            Some(name) => format!("{{{name}}}"),
            None => match segment.strip_prefix('*') {
                Some(name) => format!("{{*{name}}}"),
                None => segment.to_owned(),
            },
        })
        .collect::<Vec<_>>()
        .join("/")
}

fn parameter_value(name: &str) -> String {
    match name {
        "id" | "container_id" | "provider_id" | "task_id" | "video_id" => "424242".to_owned(),
        "mode" => "acceptance-mode".to_owned(),
        "action" => "acceptance-action".to_owned(),
        "binding_type" => "acceptance-binding".to_owned(),
        "env" => "acceptance".to_owned(),
        "flow_token" => "acceptance-flow".to_owned(),
        "model" => "acceptance-model".to_owned(),
        "node_name" => "acceptance-node".to_owned(),
        "provider" => "acceptance-provider".to_owned(),
        "sid" => "acceptance-sid".to_owned(),
        other => format!("acceptance-{other}"),
    }
}

fn wildcard_value(name: &str) -> String {
    format!("acceptance-{name}/tail")
}

pub fn wrong_method(routes: &[RouteCase], concrete: &str) -> Result<&'static str, String> {
    let methods = routes
        .iter()
        .filter(|route| pattern_matches(&route.path, concrete))
        .map(|route| route.method.as_str())
        .collect::<BTreeSet<_>>();
    ["PATCH", "DELETE", "PUT", "POST", "GET", "TRACE"]
        .into_iter()
        .find(|method| !methods.contains(method))
        .ok_or_else(|| format!("no unused HTTP method for concrete path {concrete}"))
}

fn pattern_matches(pattern: &str, concrete: &str) -> bool {
    let pattern = pattern.split('/').collect::<Vec<_>>();
    let concrete = concrete.split('/').collect::<Vec<_>>();
    let mut pattern_index = 0usize;
    let mut concrete_index = 0usize;
    while pattern_index < pattern.len() {
        let segment = pattern[pattern_index];
        if segment.starts_with('*') {
            return concrete_index < concrete.len();
        }
        let Some(candidate) = concrete.get(concrete_index) else {
            return false;
        };
        if !segment.starts_with(':') && segment != *candidate {
            return false;
        }
        pattern_index += 1;
        concrete_index += 1;
    }
    concrete_index == concrete.len()
}

#[cfg(test)]
mod tests {
    use super::{AuthClass, axum_path, concrete_path, load_routes, pattern_matches, wrong_method};

    #[test]
    fn frozen_inventory_and_auth_classes_cover_exactly_353_routes() {
        let routes = load_routes().expect("frozen route inventory is valid");
        assert_eq!(routes.len(), 353);
        assert_eq!(
            routes
                .iter()
                .filter(|route| route.auth == AuthClass::Admin)
                .count(),
            141
        );
        assert_eq!(
            routes
                .iter()
                .filter(|route| route.auth == AuthClass::Root)
                .count(),
            2
        );
        assert_eq!(
            routes
                .iter()
                .filter(|route| route.auth == AuthClass::User)
                .count(),
            80
        );
        assert_eq!(
            routes
                .iter()
                .filter(|route| route.auth == AuthClass::Token)
                .count(),
            47
        );
    }

    #[test]
    fn frozen_go_auth_exceptions_remain_exact() {
        let routes = load_routes().expect("frozen route inventory is valid");
        let expected = [
            ("GET", "/api/mj/self", AuthClass::User),
            ("GET", "/api/models", AuthClass::User),
            ("GET", "/api/ratio_config", AuthClass::Public),
            ("GET", "/api/ratio_sync/channels", AuthClass::Root),
            ("POST", "/api/ratio_sync/fetch", AuthClass::Root),
            ("GET", "/api/task/self", AuthClass::User),
            ("GET", "/api/usage/token/", AuthClass::Token),
            ("GET", "/api/user/groups", AuthClass::Public),
            ("POST", "/api/user/topup/complete", AuthClass::Admin),
            ("GET", "/dashboard/billing/subscription", AuthClass::Token),
            ("GET", "/dashboard/billing/usage", AuthClass::Token),
            ("POST", "/pg/chat/completions", AuthClass::User),
        ];

        for (method, path, auth) in expected {
            assert_eq!(
                routes
                    .iter()
                    .find(|route| route.method == method && route.path == path)
                    .unwrap_or_else(|| panic!("missing frozen route {method} {path}"))
                    .auth,
                auth,
                "frozen Go auth scope drifted for {method} {path}",
            );
        }
    }

    #[test]
    fn dynamic_and_wildcard_paths_become_safe_concrete_requests() {
        assert_eq!(
            concrete_path("/:mode/mj/task/:id/fetch"),
            "/acceptance-mode/mj/task/424242/fetch"
        );
        assert_eq!(
            concrete_path("/v1/models/*path"),
            "/v1/models/acceptance-path/tail"
        );
        assert_eq!(
            axum_path("/:mode/mj/task/:id/fetch"),
            "/{mode}/mj/task/{id}/fetch"
        );
        assert_eq!(axum_path("/v1/models/*path"), "/v1/models/{*path}");
    }

    #[test]
    fn wrong_method_accounts_for_overlapping_static_and_dynamic_shapes() {
        let routes = load_routes().expect("frozen route inventory is valid");
        let method = wrong_method(&routes, "/api/models/search")
            .expect("overlapping route has an unused method");
        assert!(!matches!(method, "GET" | "DELETE"));
        assert!(pattern_matches("/api/models/:id", "/api/models/search"));
        assert!(!pattern_matches("/api/models/:id", "/api/models/42/tail"));
    }
}
