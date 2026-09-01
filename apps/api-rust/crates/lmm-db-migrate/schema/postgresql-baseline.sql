CREATE TABLE public.abilities (
    "group" character varying(64) NOT NULL,
    model character varying(255) NOT NULL,
    channel_id bigint NOT NULL,
    enabled boolean,
    priority bigint DEFAULT 0,
    weight bigint DEFAULT 0,
    tag text
);
CREATE TABLE public.auth_flows (
    id bigint NOT NULL,
    token_hash character(64) NOT NULL,
    purpose character varying(32) NOT NULL,
    provider character varying(64),
    intent character varying(16),
    user_id bigint,
    session_id character varying(64),
    payload text,
    created_at timestamp with time zone,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone
);
CREATE SEQUENCE public.auth_flows_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.auth_flows_id_seq OWNED BY public.auth_flows.id;
CREATE TABLE public.authz_roles (
    id bigint NOT NULL,
    key character varying(64) NOT NULL,
    name character varying(100) NOT NULL,
    description text,
    built_in boolean,
    enabled boolean,
    sort bigint,
    created_at bigint,
    updated_at bigint
);
CREATE SEQUENCE public.authz_roles_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.authz_roles_id_seq OWNED BY public.authz_roles.id;
CREATE TABLE public.casbin_rule (
    id bigint NOT NULL,
    ptype character varying(100),
    v0 character varying(100),
    v1 character varying(100),
    v2 character varying(100),
    v3 character varying(100),
    v4 character varying(100),
    v5 character varying(100)
);
CREATE SEQUENCE public.casbin_rule_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.casbin_rule_id_seq OWNED BY public.casbin_rule.id;
CREATE TABLE public.channels (
    id bigint NOT NULL,
    type bigint DEFAULT 0,
    key text NOT NULL,
    open_ai_organization text,
    test_model text,
    status bigint DEFAULT 1,
    name text,
    weight bigint DEFAULT 0,
    created_time bigint,
    test_time bigint,
    response_time bigint,
    base_url text DEFAULT ''::text,
    other text,
    balance numeric,
    balance_updated_time bigint,
    models text,
    "group" character varying(64) DEFAULT 'default'::character varying,
    used_quota bigint DEFAULT 0,
    model_mapping text,
    status_code_mapping character varying(1024) DEFAULT ''::character varying,
    priority bigint DEFAULT 0,
    auto_ban bigint DEFAULT 1,
    other_info text,
    tag text,
    setting text,
    param_override text,
    header_override text,
    remark character varying(255),
    channel_info json,
    settings text
);
CREATE SEQUENCE public.channels_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.channels_id_seq OWNED BY public.channels.id;
CREATE TABLE public.checkins (
    id bigint NOT NULL,
    user_id bigint NOT NULL,
    checkin_date character varying(10) NOT NULL,
    quota_awarded bigint NOT NULL,
    created_at bigint
);
CREATE SEQUENCE public.checkins_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.checkins_id_seq OWNED BY public.checkins.id;
CREATE TABLE public.custom_oauth_providers (
    id bigint NOT NULL,
    name character varying(64) NOT NULL,
    slug character varying(64) NOT NULL,
    icon character varying(128) DEFAULT ''::character varying,
    enabled boolean DEFAULT false,
    client_id character varying(256),
    client_secret character varying(512),
    authorization_endpoint character varying(512),
    token_endpoint character varying(512),
    user_info_endpoint character varying(512),
    scopes character varying(256) DEFAULT 'openid profile email'::character varying,
    user_id_field character varying(128) DEFAULT 'sub'::character varying,
    username_field character varying(128) DEFAULT 'preferred_username'::character varying,
    display_name_field character varying(128) DEFAULT 'name'::character varying,
    email_field character varying(128) DEFAULT 'email'::character varying,
    well_known character varying(512),
    auth_style bigint DEFAULT 0,
    access_policy text,
    access_denied_message character varying(512),
    created_at timestamp with time zone,
    updated_at timestamp with time zone
);
CREATE SEQUENCE public.custom_oauth_providers_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.custom_oauth_providers_id_seq OWNED BY public.custom_oauth_providers.id;
CREATE TABLE public.external_identity_claims (
    id bigint NOT NULL,
    provider character varying(32) NOT NULL,
    subject character varying(128) NOT NULL,
    user_id bigint NOT NULL,
    created_at timestamp with time zone
);
CREATE SEQUENCE public.external_identity_claims_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.external_identity_claims_id_seq OWNED BY public.external_identity_claims.id;
CREATE TABLE public.logs (
    id bigint NOT NULL,
    user_id bigint,
    created_at bigint,
    type bigint,
    content text,
    username text DEFAULT ''::text,
    token_name text DEFAULT ''::text,
    model_name text DEFAULT ''::text,
    quota bigint DEFAULT 0,
    prompt_tokens bigint DEFAULT 0,
    completion_tokens bigint DEFAULT 0,
    use_time bigint DEFAULT 0,
    is_stream boolean,
    channel_id bigint,
    channel_name text,
    token_id bigint DEFAULT 0,
    "group" text,
    ip text DEFAULT ''::text,
    request_id character varying(64) DEFAULT ''::character varying,
    upstream_request_id character varying(128) DEFAULT ''::character varying,
    other text
);
CREATE SEQUENCE public.logs_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.logs_id_seq OWNED BY public.logs.id;
CREATE TABLE public.midjourneys (
    id bigint NOT NULL,
    code bigint,
    user_id bigint,
    action character varying(40),
    mj_id text,
    prompt text,
    prompt_en text,
    description text,
    state text,
    submit_time bigint,
    start_time bigint,
    finish_time bigint,
    image_url text,
    video_url text,
    video_urls text,
    status character varying(20),
    progress character varying(30),
    fail_reason text,
    channel_id bigint,
    quota bigint,
    buttons text,
    properties text
);
CREATE SEQUENCE public.midjourneys_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.midjourneys_id_seq OWNED BY public.midjourneys.id;
CREATE TABLE public.models (
    id bigint NOT NULL,
    model_name character varying(128) NOT NULL,
    description text,
    icon character varying(128),
    tags character varying(255),
    vendor_id bigint,
    endpoints text,
    status bigint DEFAULT 1,
    sync_official bigint DEFAULT 1,
    created_time bigint,
    updated_time bigint,
    deleted_at timestamp with time zone,
    name_rule bigint DEFAULT 0
);
CREATE SEQUENCE public.models_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.models_id_seq OWNED BY public.models.id;
CREATE TABLE public.options (
    key text NOT NULL,
    value text
);
CREATE TABLE public.passkey_credentials (
    id bigint NOT NULL,
    user_id bigint NOT NULL,
    credential_id character varying(512) NOT NULL,
    public_key text NOT NULL,
    attestation_type character varying(255),
    aa_guid character varying(512),
    sign_count bigint DEFAULT 0,
    clone_warning boolean,
    user_present boolean,
    user_verified boolean,
    backup_eligible boolean,
    backup_state boolean,
    transports text,
    attachment character varying(32),
    last_used_at timestamp with time zone,
    created_at timestamp with time zone,
    updated_at timestamp with time zone,
    deleted_at timestamp with time zone
);
CREATE SEQUENCE public.passkey_credentials_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.passkey_credentials_id_seq OWNED BY public.passkey_credentials.id;
CREATE TABLE public.perf_metrics (
    id bigint NOT NULL,
    model_name character varying(128),
    "group" character varying(64),
    bucket_ts bigint,
    request_count bigint DEFAULT 0,
    success_count bigint DEFAULT 0,
    total_latency_ms bigint DEFAULT 0,
    ttft_sum_ms bigint DEFAULT 0,
    ttft_count bigint DEFAULT 0,
    output_tokens bigint DEFAULT 0,
    generation_ms bigint DEFAULT 0
);
CREATE SEQUENCE public.perf_metrics_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.perf_metrics_id_seq OWNED BY public.perf_metrics.id;
CREATE TABLE public.prefill_groups (
    id bigint NOT NULL,
    name character varying(64) NOT NULL,
    type character varying(32) NOT NULL,
    items json,
    description character varying(255),
    created_time bigint,
    updated_time bigint,
    deleted_at timestamp with time zone
);
CREATE SEQUENCE public.prefill_groups_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.prefill_groups_id_seq OWNED BY public.prefill_groups.id;
CREATE TABLE public.quota_data (
    id bigint NOT NULL,
    user_id bigint,
    username character varying(64) DEFAULT ''::character varying,
    model_name character varying(64) DEFAULT ''::character varying,
    created_at bigint,
    use_group character varying(64) DEFAULT ''::character varying,
    token_id bigint DEFAULT 0,
    channel_id bigint DEFAULT 0,
    node_name character varying(64) DEFAULT ''::character varying,
    token_used bigint DEFAULT 0,
    count bigint DEFAULT 0,
    quota bigint DEFAULT 0
);
CREATE SEQUENCE public.quota_data_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.quota_data_id_seq OWNED BY public.quota_data.id;
CREATE TABLE public.redemptions (
    id bigint NOT NULL,
    user_id bigint,
    key character(32),
    status bigint DEFAULT 1,
    name text,
    quota bigint DEFAULT 100,
    created_time bigint,
    redeemed_time bigint,
    used_user_id bigint,
    deleted_at timestamp with time zone,
    expired_time bigint
);
CREATE SEQUENCE public.redemptions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.redemptions_id_seq OWNED BY public.redemptions.id;
CREATE TABLE public.setups (
    id bigint NOT NULL,
    version character varying(50) NOT NULL,
    initialized_at bigint NOT NULL
);
CREATE SEQUENCE public.setups_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.setups_id_seq OWNED BY public.setups.id;
CREATE TABLE public.subscription_orders (
    id bigint NOT NULL,
    user_id bigint,
    plan_id bigint,
    money numeric,
    trade_no character varying(255),
    payment_method character varying(50),
    payment_provider character varying(50) DEFAULT ''::character varying,
    status text,
    create_time bigint,
    complete_time bigint,
    provider_payload text
);
CREATE SEQUENCE public.subscription_orders_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.subscription_orders_id_seq OWNED BY public.subscription_orders.id;
CREATE TABLE public.subscription_plans (
    id bigint NOT NULL,
    title character varying(128) NOT NULL,
    subtitle character varying(255) DEFAULT ''::character varying,
    price_amount numeric(10,6) DEFAULT 0.000000 NOT NULL,
    currency character varying(8) DEFAULT 'USD'::character varying NOT NULL,
    duration_unit character varying(16) DEFAULT 'month'::character varying NOT NULL,
    duration_value bigint DEFAULT 1 NOT NULL,
    custom_seconds bigint DEFAULT 0 NOT NULL,
    enabled boolean DEFAULT true,
    sort_order bigint DEFAULT 0,
    allow_balance_pay boolean,
    allow_wallet_overflow boolean,
    stripe_price_id character varying(128) DEFAULT ''::character varying,
    creem_product_id character varying(128) DEFAULT ''::character varying,
    waffo_pancake_product_id character varying(128) DEFAULT ''::character varying,
    waffo_pancake_product_type character varying(16) DEFAULT 'subscription'::character varying NOT NULL,
    max_purchase_per_user bigint DEFAULT 0,
    upgrade_group character varying(64) DEFAULT ''::character varying,
    downgrade_group character varying(64) DEFAULT ''::character varying,
    total_amount bigint DEFAULT 0 NOT NULL,
    quota_reset_period character varying(16) DEFAULT 'never'::character varying,
    quota_reset_custom_seconds bigint DEFAULT 0,
    created_at bigint,
    updated_at bigint
);
CREATE SEQUENCE public.subscription_plans_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.subscription_plans_id_seq OWNED BY public.subscription_plans.id;
CREATE TABLE public.subscription_pre_consume_records (
    id bigint NOT NULL,
    request_id character varying(64),
    user_id bigint,
    user_subscription_id bigint,
    pre_consumed bigint DEFAULT 0 NOT NULL,
    status character varying(32),
    created_at bigint,
    updated_at bigint
);
CREATE SEQUENCE public.subscription_pre_consume_records_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.subscription_pre_consume_records_id_seq OWNED BY public.subscription_pre_consume_records.id;
CREATE TABLE public.system_instances (
    node_name character varying(128) NOT NULL,
    info text,
    started_at bigint,
    last_seen_at bigint,
    created_at bigint,
    updated_at bigint
);
CREATE TABLE public.system_task_locks (
    type character varying(64) NOT NULL,
    task_id character varying(64),
    locked_by character varying(128),
    locked_until bigint,
    updated_at bigint
);
CREATE TABLE public.system_tasks (
    id bigint NOT NULL,
    task_id character varying(64),
    type character varying(64),
    status character varying(32),
    active_key character varying(64),
    payload text,
    state text,
    result text,
    error text,
    locked_by character varying(128),
    created_at bigint,
    updated_at bigint
);
CREATE SEQUENCE public.system_tasks_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.system_tasks_id_seq OWNED BY public.system_tasks.id;
CREATE TABLE public.tasks (
    id bigint NOT NULL,
    created_at bigint,
    updated_at bigint,
    task_id character varying(191),
    platform character varying(30),
    user_id bigint,
    "group" character varying(50),
    channel_id bigint,
    quota bigint,
    action character varying(40),
    status character varying(20),
    fail_reason text,
    submit_time bigint,
    start_time bigint,
    finish_time bigint,
    progress character varying(20),
    properties json,
    private_data json,
    data json
);
CREATE SEQUENCE public.tasks_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.tasks_id_seq OWNED BY public.tasks.id;
CREATE TABLE public.tokens (
    id bigint NOT NULL,
    user_id bigint,
    key character varying(128),
    status bigint DEFAULT 1,
    name text,
    created_time bigint,
    accessed_time bigint,
    expired_time bigint DEFAULT '-1'::integer,
    remain_quota bigint DEFAULT 0,
    unlimited_quota boolean,
    model_limits_enabled boolean,
    model_limits text,
    allow_ips text DEFAULT ''::text,
    used_quota bigint DEFAULT 0,
    "group" text DEFAULT ''::text,
    cross_group_retry boolean,
    auto_groups text,
    deleted_at timestamp with time zone
);
CREATE SEQUENCE public.tokens_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.tokens_id_seq OWNED BY public.tokens.id;
CREATE TABLE public.top_ups (
    id bigint NOT NULL,
    user_id bigint,
    amount bigint,
    money numeric,
    trade_no character varying(255),
    payment_method character varying(50),
    payment_provider character varying(50) DEFAULT ''::character varying,
    create_time bigint,
    complete_time bigint,
    status text
);
CREATE SEQUENCE public.top_ups_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.top_ups_id_seq OWNED BY public.top_ups.id;
CREATE TABLE public.two_fa_backup_codes (
    id bigint NOT NULL,
    user_id bigint NOT NULL,
    code_hash character varying(255) NOT NULL,
    is_used boolean,
    used_at timestamp with time zone,
    created_at timestamp with time zone,
    deleted_at timestamp with time zone
);
CREATE SEQUENCE public.two_fa_backup_codes_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.two_fa_backup_codes_id_seq OWNED BY public.two_fa_backup_codes.id;
CREATE TABLE public.two_fas (
    id bigint NOT NULL,
    user_id bigint NOT NULL,
    secret character varying(255) NOT NULL,
    is_enabled boolean,
    failed_attempts bigint DEFAULT 0,
    locked_until timestamp with time zone,
    last_used_at timestamp with time zone,
    created_at timestamp with time zone,
    updated_at timestamp with time zone,
    deleted_at timestamp with time zone
);
CREATE SEQUENCE public.two_fas_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.two_fas_id_seq OWNED BY public.two_fas.id;
CREATE TABLE public.user_oauth_bindings (
    id bigint NOT NULL,
    user_id bigint NOT NULL,
    provider_id bigint NOT NULL,
    provider_user_id character varying(256) NOT NULL,
    created_at timestamp with time zone
);
CREATE SEQUENCE public.user_oauth_bindings_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.user_oauth_bindings_id_seq OWNED BY public.user_oauth_bindings.id;
CREATE TABLE public.user_sessions (
    sid character varying(64) NOT NULL,
    user_id bigint NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    user_auth_version bigint NOT NULL,
    status character varying(16) NOT NULL,
    refresh_hash character(64) NOT NULL,
    previous_refresh_hash character varying(64),
    previous_valid_until bigint DEFAULT 0 NOT NULL,
    login_method character varying(32) NOT NULL,
    ip character varying(64),
    user_agent text,
    created_at bigint,
    last_active_at bigint NOT NULL,
    expires_at bigint NOT NULL,
    revoked_at bigint DEFAULT 0 NOT NULL,
    revoked_reason character varying(64)
);
CREATE TABLE public.user_subscriptions (
    id bigint NOT NULL,
    user_id bigint,
    plan_id bigint,
    amount_total bigint DEFAULT 0 NOT NULL,
    amount_used bigint DEFAULT 0 NOT NULL,
    start_time bigint,
    end_time bigint,
    status character varying(32),
    source character varying(32) DEFAULT 'order'::character varying,
    last_reset_time bigint DEFAULT 0,
    next_reset_time bigint DEFAULT 0,
    upgrade_group character varying(64) DEFAULT ''::character varying,
    prev_user_group character varying(64) DEFAULT ''::character varying,
    downgrade_group character varying(64) DEFAULT ''::character varying,
    allow_wallet_overflow boolean,
    created_at bigint,
    updated_at bigint
);
CREATE SEQUENCE public.user_subscriptions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.user_subscriptions_id_seq OWNED BY public.user_subscriptions.id;
CREATE TABLE public.users (
    id bigint NOT NULL,
    username text,
    password text NOT NULL,
    display_name text,
    role bigint DEFAULT 1,
    status bigint DEFAULT 1,
    email text,
    github_id text,
    discord_id text,
    oidc_id text,
    wechat_id text,
    telegram_id text,
    access_token character(32),
    quota bigint DEFAULT 0,
    used_quota bigint DEFAULT 0,
    request_count bigint DEFAULT 0,
    "group" character varying(64) DEFAULT 'default'::character varying,
    aff_code character varying(32),
    aff_count bigint DEFAULT 0,
    aff_quota bigint DEFAULT 0,
    aff_history bigint DEFAULT 0,
    inviter_id bigint,
    deleted_at timestamp with time zone,
    linux_do_id text,
    setting text,
    remark character varying(255),
    stripe_customer character varying(64),
    created_at bigint,
    last_login_at bigint DEFAULT 0,
    console_activated_at bigint DEFAULT 0 NOT NULL,
    auth_version bigint DEFAULT 1 NOT NULL
);
CREATE SEQUENCE public.users_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.users_id_seq OWNED BY public.users.id;
CREATE TABLE public.vendors (
    id bigint NOT NULL,
    name character varying(128) NOT NULL,
    description text,
    icon character varying(128),
    status bigint DEFAULT 1,
    created_time bigint,
    updated_time bigint,
    deleted_at timestamp with time zone
);
CREATE SEQUENCE public.vendors_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.vendors_id_seq OWNED BY public.vendors.id;
ALTER TABLE ONLY public.auth_flows ALTER COLUMN id SET DEFAULT nextval('public.auth_flows_id_seq'::regclass);
ALTER TABLE ONLY public.authz_roles ALTER COLUMN id SET DEFAULT nextval('public.authz_roles_id_seq'::regclass);
ALTER TABLE ONLY public.casbin_rule ALTER COLUMN id SET DEFAULT nextval('public.casbin_rule_id_seq'::regclass);
ALTER TABLE ONLY public.channels ALTER COLUMN id SET DEFAULT nextval('public.channels_id_seq'::regclass);
ALTER TABLE ONLY public.checkins ALTER COLUMN id SET DEFAULT nextval('public.checkins_id_seq'::regclass);
ALTER TABLE ONLY public.custom_oauth_providers ALTER COLUMN id SET DEFAULT nextval('public.custom_oauth_providers_id_seq'::regclass);
ALTER TABLE ONLY public.external_identity_claims ALTER COLUMN id SET DEFAULT nextval('public.external_identity_claims_id_seq'::regclass);
ALTER TABLE ONLY public.logs ALTER COLUMN id SET DEFAULT nextval('public.logs_id_seq'::regclass);
ALTER TABLE ONLY public.midjourneys ALTER COLUMN id SET DEFAULT nextval('public.midjourneys_id_seq'::regclass);
ALTER TABLE ONLY public.models ALTER COLUMN id SET DEFAULT nextval('public.models_id_seq'::regclass);
ALTER TABLE ONLY public.passkey_credentials ALTER COLUMN id SET DEFAULT nextval('public.passkey_credentials_id_seq'::regclass);
ALTER TABLE ONLY public.perf_metrics ALTER COLUMN id SET DEFAULT nextval('public.perf_metrics_id_seq'::regclass);
ALTER TABLE ONLY public.prefill_groups ALTER COLUMN id SET DEFAULT nextval('public.prefill_groups_id_seq'::regclass);
ALTER TABLE ONLY public.quota_data ALTER COLUMN id SET DEFAULT nextval('public.quota_data_id_seq'::regclass);
ALTER TABLE ONLY public.redemptions ALTER COLUMN id SET DEFAULT nextval('public.redemptions_id_seq'::regclass);
ALTER TABLE ONLY public.setups ALTER COLUMN id SET DEFAULT nextval('public.setups_id_seq'::regclass);
ALTER TABLE ONLY public.subscription_orders ALTER COLUMN id SET DEFAULT nextval('public.subscription_orders_id_seq'::regclass);
ALTER TABLE ONLY public.subscription_plans ALTER COLUMN id SET DEFAULT nextval('public.subscription_plans_id_seq'::regclass);
ALTER TABLE ONLY public.subscription_pre_consume_records ALTER COLUMN id SET DEFAULT nextval('public.subscription_pre_consume_records_id_seq'::regclass);
ALTER TABLE ONLY public.system_tasks ALTER COLUMN id SET DEFAULT nextval('public.system_tasks_id_seq'::regclass);
ALTER TABLE ONLY public.tasks ALTER COLUMN id SET DEFAULT nextval('public.tasks_id_seq'::regclass);
ALTER TABLE ONLY public.tokens ALTER COLUMN id SET DEFAULT nextval('public.tokens_id_seq'::regclass);
ALTER TABLE ONLY public.top_ups ALTER COLUMN id SET DEFAULT nextval('public.top_ups_id_seq'::regclass);
ALTER TABLE ONLY public.two_fa_backup_codes ALTER COLUMN id SET DEFAULT nextval('public.two_fa_backup_codes_id_seq'::regclass);
ALTER TABLE ONLY public.two_fas ALTER COLUMN id SET DEFAULT nextval('public.two_fas_id_seq'::regclass);
ALTER TABLE ONLY public.user_oauth_bindings ALTER COLUMN id SET DEFAULT nextval('public.user_oauth_bindings_id_seq'::regclass);
ALTER TABLE ONLY public.user_subscriptions ALTER COLUMN id SET DEFAULT nextval('public.user_subscriptions_id_seq'::regclass);
ALTER TABLE ONLY public.users ALTER COLUMN id SET DEFAULT nextval('public.users_id_seq'::regclass);
ALTER TABLE ONLY public.vendors ALTER COLUMN id SET DEFAULT nextval('public.vendors_id_seq'::regclass);
ALTER TABLE ONLY public.abilities
    ADD CONSTRAINT abilities_pkey PRIMARY KEY ("group", model, channel_id);
ALTER TABLE ONLY public.auth_flows
    ADD CONSTRAINT auth_flows_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.authz_roles
    ADD CONSTRAINT authz_roles_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.casbin_rule
    ADD CONSTRAINT casbin_rule_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.channels
    ADD CONSTRAINT channels_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.checkins
    ADD CONSTRAINT checkins_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.custom_oauth_providers
    ADD CONSTRAINT custom_oauth_providers_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.external_identity_claims
    ADD CONSTRAINT external_identity_claims_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.logs
    ADD CONSTRAINT logs_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.midjourneys
    ADD CONSTRAINT midjourneys_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.models
    ADD CONSTRAINT models_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.options
    ADD CONSTRAINT options_pkey PRIMARY KEY (key);
ALTER TABLE ONLY public.passkey_credentials
    ADD CONSTRAINT passkey_credentials_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.perf_metrics
    ADD CONSTRAINT perf_metrics_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.prefill_groups
    ADD CONSTRAINT prefill_groups_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.quota_data
    ADD CONSTRAINT quota_data_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.redemptions
    ADD CONSTRAINT redemptions_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.setups
    ADD CONSTRAINT setups_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.subscription_orders
    ADD CONSTRAINT subscription_orders_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.subscription_orders
    ADD CONSTRAINT subscription_orders_trade_no_key UNIQUE (trade_no);
ALTER TABLE ONLY public.subscription_plans
    ADD CONSTRAINT subscription_plans_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.subscription_pre_consume_records
    ADD CONSTRAINT subscription_pre_consume_records_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.system_instances
    ADD CONSTRAINT system_instances_pkey PRIMARY KEY (node_name);
ALTER TABLE ONLY public.system_task_locks
    ADD CONSTRAINT system_task_locks_pkey PRIMARY KEY (type);
ALTER TABLE ONLY public.system_tasks
    ADD CONSTRAINT system_tasks_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.tokens
    ADD CONSTRAINT tokens_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.top_ups
    ADD CONSTRAINT top_ups_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.top_ups
    ADD CONSTRAINT top_ups_trade_no_key UNIQUE (trade_no);
ALTER TABLE ONLY public.two_fa_backup_codes
    ADD CONSTRAINT two_fa_backup_codes_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.two_fas
    ADD CONSTRAINT two_fas_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.two_fas
    ADD CONSTRAINT two_fas_user_id_key UNIQUE (user_id);
ALTER TABLE ONLY public.user_oauth_bindings
    ADD CONSTRAINT user_oauth_bindings_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.user_sessions
    ADD CONSTRAINT user_sessions_pkey PRIMARY KEY (sid);
ALTER TABLE ONLY public.user_subscriptions
    ADD CONSTRAINT user_subscriptions_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_username_key UNIQUE (username);
ALTER TABLE ONLY public.vendors
    ADD CONSTRAINT vendors_pkey PRIMARY KEY (id);
CREATE INDEX idx_abilities_channel_id ON public.abilities USING btree (channel_id);
CREATE INDEX idx_abilities_priority ON public.abilities USING btree (priority);
CREATE INDEX idx_abilities_tag ON public.abilities USING btree (tag);
CREATE INDEX idx_abilities_weight ON public.abilities USING btree (weight);
CREATE INDEX idx_auth_flow_purpose_expiry ON public.auth_flows USING btree (purpose, expires_at);
CREATE INDEX idx_auth_flows_consumed_at ON public.auth_flows USING btree (consumed_at);
CREATE INDEX idx_auth_flows_session_id ON public.auth_flows USING btree (session_id);
CREATE UNIQUE INDEX idx_auth_flows_token_hash ON public.auth_flows USING btree (token_hash);
CREATE INDEX idx_auth_flows_user_id ON public.auth_flows USING btree (user_id);
CREATE UNIQUE INDEX idx_authz_roles_key ON public.authz_roles USING btree (key);
CREATE INDEX idx_casbin_rule ON public.casbin_rule USING btree (ptype, v0, v1, v2, v3, v4, v5);
CREATE UNIQUE INDEX idx_casbin_rule_unique ON public.casbin_rule USING btree (ptype, v0, v1, v2, v3, v4, v5);
CREATE INDEX idx_channels_name ON public.channels USING btree (name);
CREATE INDEX idx_channels_tag ON public.channels USING btree (tag);
CREATE INDEX idx_created_at_id ON public.logs USING btree (created_at, id);
CREATE INDEX idx_created_at_type ON public.logs USING btree (created_at, type);
CREATE UNIQUE INDEX idx_custom_oauth_providers_slug ON public.custom_oauth_providers USING btree (slug);
CREATE INDEX idx_external_identity_claims_user_id ON public.external_identity_claims USING btree (user_id);
CREATE UNIQUE INDEX idx_external_identity_subject ON public.external_identity_claims USING btree (provider, subject);
CREATE UNIQUE INDEX idx_external_identity_user ON public.external_identity_claims USING btree (provider, user_id);
CREATE INDEX idx_logs_channel_id ON public.logs USING btree (channel_id);
CREATE INDEX idx_logs_group ON public.logs USING btree ("group");
CREATE INDEX idx_logs_ip ON public.logs USING btree (ip);
CREATE INDEX idx_logs_model_name ON public.logs USING btree (model_name);
CREATE INDEX idx_logs_request_id ON public.logs USING btree (request_id);
CREATE INDEX idx_logs_token_id ON public.logs USING btree (token_id);
CREATE INDEX idx_logs_token_name ON public.logs USING btree (token_name);
CREATE INDEX idx_logs_upstream_request_id ON public.logs USING btree (upstream_request_id);
CREATE INDEX idx_logs_user_id ON public.logs USING btree (user_id);
CREATE INDEX idx_logs_username ON public.logs USING btree (username);
CREATE INDEX idx_midjourneys_action ON public.midjourneys USING btree (action);
CREATE INDEX idx_midjourneys_finish_time ON public.midjourneys USING btree (finish_time);
CREATE INDEX idx_midjourneys_mj_id ON public.midjourneys USING btree (mj_id);
CREATE INDEX idx_midjourneys_progress ON public.midjourneys USING btree (progress);
CREATE INDEX idx_midjourneys_start_time ON public.midjourneys USING btree (start_time);
CREATE INDEX idx_midjourneys_status ON public.midjourneys USING btree (status);
CREATE INDEX idx_midjourneys_submit_time ON public.midjourneys USING btree (submit_time);
CREATE INDEX idx_midjourneys_user_id ON public.midjourneys USING btree (user_id);
CREATE INDEX idx_models_deleted_at ON public.models USING btree (deleted_at);
CREATE INDEX idx_models_vendor_id ON public.models USING btree (vendor_id);
CREATE UNIQUE INDEX idx_passkey_credentials_credential_id ON public.passkey_credentials USING btree (credential_id);
CREATE INDEX idx_passkey_credentials_deleted_at ON public.passkey_credentials USING btree (deleted_at);
CREATE UNIQUE INDEX idx_passkey_credentials_user_id ON public.passkey_credentials USING btree (user_id);
CREATE INDEX idx_perf_bucket_ts ON public.perf_metrics USING btree (bucket_ts);
CREATE UNIQUE INDEX idx_perf_model_group_bucket ON public.perf_metrics USING btree (model_name, "group", bucket_ts);
CREATE INDEX idx_prefill_groups_deleted_at ON public.prefill_groups USING btree (deleted_at);
CREATE INDEX idx_prefill_groups_type ON public.prefill_groups USING btree (type);
CREATE INDEX idx_qdt_created_at ON public.quota_data USING btree (created_at);
CREATE INDEX idx_qdt_model_user_name ON public.quota_data USING btree (model_name, username);
CREATE INDEX idx_quota_data_channel_id ON public.quota_data USING btree (channel_id);
CREATE INDEX idx_quota_data_node_name ON public.quota_data USING btree (node_name);
CREATE INDEX idx_quota_data_token_id ON public.quota_data USING btree (token_id);
CREATE INDEX idx_quota_data_use_group ON public.quota_data USING btree (use_group);
CREATE INDEX idx_quota_data_user_id ON public.quota_data USING btree (user_id);
CREATE INDEX idx_redemptions_deleted_at ON public.redemptions USING btree (deleted_at);
CREATE UNIQUE INDEX idx_redemptions_key ON public.redemptions USING btree (key);
CREATE INDEX idx_redemptions_name ON public.redemptions USING btree (name);
CREATE INDEX idx_subscription_orders_plan_id ON public.subscription_orders USING btree (plan_id);
CREATE INDEX idx_subscription_orders_trade_no ON public.subscription_orders USING btree (trade_no);
CREATE INDEX idx_subscription_orders_user_id ON public.subscription_orders USING btree (user_id);
CREATE UNIQUE INDEX idx_subscription_pre_consume_records_request_id ON public.subscription_pre_consume_records USING btree (request_id);
CREATE INDEX idx_subscription_pre_consume_records_status ON public.subscription_pre_consume_records USING btree (status);
CREATE INDEX idx_subscription_pre_consume_records_updated_at ON public.subscription_pre_consume_records USING btree (updated_at);
CREATE INDEX idx_subscription_pre_consume_records_user_id ON public.subscription_pre_consume_records USING btree (user_id);
CREATE INDEX idx_subscription_pre_consume_records_user_subscription_id ON public.subscription_pre_consume_records USING btree (user_subscription_id);
CREATE INDEX idx_system_instances_created_at ON public.system_instances USING btree (created_at);
CREATE INDEX idx_system_instances_last_seen_at ON public.system_instances USING btree (last_seen_at);
CREATE INDEX idx_system_instances_started_at ON public.system_instances USING btree (started_at);
CREATE INDEX idx_system_instances_updated_at ON public.system_instances USING btree (updated_at);
CREATE INDEX idx_system_task_locks_locked_by ON public.system_task_locks USING btree (locked_by);
CREATE INDEX idx_system_task_locks_locked_until ON public.system_task_locks USING btree (locked_until);
CREATE INDEX idx_system_task_locks_task_id ON public.system_task_locks USING btree (task_id);
CREATE INDEX idx_system_task_locks_updated_at ON public.system_task_locks USING btree (updated_at);
CREATE UNIQUE INDEX idx_system_tasks_active_key ON public.system_tasks USING btree (active_key);
CREATE INDEX idx_system_tasks_created_at ON public.system_tasks USING btree (created_at);
CREATE INDEX idx_system_tasks_locked_by ON public.system_tasks USING btree (locked_by);
CREATE INDEX idx_system_tasks_status ON public.system_tasks USING btree (status);
CREATE UNIQUE INDEX idx_system_tasks_task_id ON public.system_tasks USING btree (task_id);
CREATE INDEX idx_system_tasks_type ON public.system_tasks USING btree (type);
CREATE INDEX idx_system_tasks_updated_at ON public.system_tasks USING btree (updated_at);
CREATE INDEX idx_tasks_action ON public.tasks USING btree (action);
CREATE INDEX idx_tasks_channel_id ON public.tasks USING btree (channel_id);
CREATE INDEX idx_tasks_created_at ON public.tasks USING btree (created_at);
CREATE INDEX idx_tasks_finish_time ON public.tasks USING btree (finish_time);
CREATE INDEX idx_tasks_platform ON public.tasks USING btree (platform);
CREATE INDEX idx_tasks_progress ON public.tasks USING btree (progress);
CREATE INDEX idx_tasks_start_time ON public.tasks USING btree (start_time);
CREATE INDEX idx_tasks_status ON public.tasks USING btree (status);
CREATE INDEX idx_tasks_submit_time ON public.tasks USING btree (submit_time);
CREATE INDEX idx_tasks_task_id ON public.tasks USING btree (task_id);
CREATE INDEX idx_tasks_user_id ON public.tasks USING btree (user_id);
CREATE INDEX idx_tokens_deleted_at ON public.tokens USING btree (deleted_at);
CREATE UNIQUE INDEX idx_tokens_key ON public.tokens USING btree (key);
CREATE INDEX idx_tokens_name ON public.tokens USING btree (name);
CREATE INDEX idx_tokens_user_id ON public.tokens USING btree (user_id);
CREATE INDEX idx_top_ups_trade_no ON public.top_ups USING btree (trade_no);
CREATE INDEX idx_top_ups_user_id ON public.top_ups USING btree (user_id);
CREATE INDEX idx_two_fa_backup_codes_deleted_at ON public.two_fa_backup_codes USING btree (deleted_at);
CREATE INDEX idx_two_fa_backup_codes_user_id ON public.two_fa_backup_codes USING btree (user_id);
CREATE INDEX idx_two_fas_deleted_at ON public.two_fas USING btree (deleted_at);
CREATE INDEX idx_two_fas_user_id ON public.two_fas USING btree (user_id);
CREATE UNIQUE INDEX idx_user_checkin_date ON public.checkins USING btree (user_id, checkin_date);
CREATE INDEX idx_user_id_id ON public.logs USING btree (user_id, id);
CREATE INDEX idx_user_sessions_expires_at ON public.user_sessions USING btree (expires_at);
CREATE INDEX idx_user_sessions_status_revoked ON public.user_sessions USING btree (status, revoked_at);
CREATE INDEX idx_user_sessions_user_created ON public.user_sessions USING btree (user_id, created_at);
CREATE INDEX idx_user_sessions_user_status_expiry ON public.user_sessions USING btree (user_id, status, expires_at);
CREATE INDEX idx_user_sub_active ON public.user_subscriptions USING btree (user_id, status, end_time);
CREATE INDEX idx_user_subscriptions_end_time ON public.user_subscriptions USING btree (end_time);
CREATE INDEX idx_user_subscriptions_next_reset_time ON public.user_subscriptions USING btree (next_reset_time);
CREATE INDEX idx_user_subscriptions_plan_id ON public.user_subscriptions USING btree (plan_id);
CREATE INDEX idx_user_subscriptions_status ON public.user_subscriptions USING btree (status);
CREATE INDEX idx_user_subscriptions_user_id ON public.user_subscriptions USING btree (user_id);
CREATE UNIQUE INDEX idx_users_access_token ON public.users USING btree (access_token);
CREATE UNIQUE INDEX idx_users_aff_code ON public.users USING btree (aff_code);
CREATE INDEX idx_users_deleted_at ON public.users USING btree (deleted_at);
CREATE INDEX idx_users_discord_id ON public.users USING btree (discord_id);
CREATE INDEX idx_users_display_name ON public.users USING btree (display_name);
CREATE INDEX idx_users_email ON public.users USING btree (email);
CREATE INDEX idx_users_git_hub_id ON public.users USING btree (github_id);
CREATE INDEX idx_users_inviter_id ON public.users USING btree (inviter_id);
CREATE INDEX idx_users_linux_do_id ON public.users USING btree (linux_do_id);
CREATE INDEX idx_users_oidc_id ON public.users USING btree (oidc_id);
CREATE INDEX idx_users_stripe_customer ON public.users USING btree (stripe_customer);
CREATE INDEX idx_users_telegram_id ON public.users USING btree (telegram_id);
CREATE INDEX idx_users_username ON public.users USING btree (username);
CREATE INDEX idx_users_we_chat_id ON public.users USING btree (wechat_id);
CREATE INDEX idx_vendors_deleted_at ON public.vendors USING btree (deleted_at);
CREATE INDEX index_username_model_name ON public.logs USING btree (model_name, username);
CREATE UNIQUE INDEX uk_model_name_delete_at ON public.models USING btree (model_name, deleted_at);
CREATE UNIQUE INDEX uk_prefill_name ON public.prefill_groups USING btree (name) WHERE (deleted_at IS NULL);
CREATE UNIQUE INDEX uk_vendor_name_delete_at ON public.vendors USING btree (name, deleted_at);
CREATE UNIQUE INDEX ux_provider_userid ON public.user_oauth_bindings USING btree (provider_id, provider_user_id);
CREATE UNIQUE INDEX ux_user_provider ON public.user_oauth_bindings USING btree (user_id, provider_id);
