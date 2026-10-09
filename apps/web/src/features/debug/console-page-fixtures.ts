/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { AxiosAdapter, InternalAxiosRequestConfig } from "axios";

import type { AdminSiteStatistics } from "@/features/dashboard/site-statistics";
import { ROLE } from "@/lib/roles";
import { useAuthStore } from "@/stores/auth-store";

import { DEBUG_WALLET_TOPUP_INFO } from "./wallet-review-fixtures";

const stamp = 1790035200;
const page = { items: [], total: 0, page: 1, page_size: 20, size: 20 };
const modelNames = ["gpt-5-mini", "claude-sonnet"];

// GetMerchantStoreConfig, GetMarketAIReviewSettings and
// ListAdminViolationFeeAppeals expose these success shapes. Empty configured
// payment methods leave only the built-in balance method; no provider is called.
const storeAndSecurityReads: Record<string, unknown> = {
  "/api/store/config": {
    fee_bps: 100,
    promotion_quota: 500000,
    minimum_unit_price_quota: 500000,
    product_test_mode_supported: true,
    store_catalogue_supported: true,
    store_collections_supported: true,
    store_access_supported: true,
    product_purchase_limits_supported: true,
    product_link_presets: [],
    linuxdo_units_per_usd: "",
    // common.FixedCreditsPerUSD and MerchantStoreCreditsPerUSD are fixed at
    // 500000; the external merchant threshold is ten times that ledger basis.
    credits_per_usd: 500000,
    external_minimum_quota: 5000000,
    disclaimer_version: "merchant-store-v1",
    disclaimer_text: `Third-party products are sold and delivered by the listed merchant. Please read the description, delivery conditions, price and merchant contact details before ordering. The official badge identifies products sold by a current platform administrator; other products are independent merchant products.

The platform provides listing review, payment records and delivery links. A listing review is not a guarantee of product quality, suitability, legality or continued availability. Contact the merchant first about product issues, and keep your order number and payment record when requesting platform assistance. The platform may pause products or investigate reports.

Digital text and activation codes may be revealed immediately after confirmed payment. Do not share your private delivery link or pickup code. Check the merchant's stated terms before buying; any refund request must be handled according to the applicable order and payment terms.

Payments credited to a merchant's platform balance cannot be withdrawn and may only be used for consumption on the platform. External merchant gateways receive the payment directly, while the platform charges the merchant a service fee in credits.

By accepting, you confirm that you have read these terms and understand that you are purchasing from the named third-party merchant. You can reopen this notice at any time from the shop.`,
    platform_payment_methods: [
      {
        provider: "balance",
        payment_type: "balance",
        name: "Platform balance",
        supported: true,
        configured: true,
      },
    ],
    platform_payment_catalog: [
      {
        provider: "balance",
        payment_type: "balance",
        name: "Platform balance",
        supported: true,
        configured: true,
      },
    ],
  },
  "/api/security/market-ai-review/settings": {
    tool_mode: "off",
    store_mode: "off",
    review_group: "default",
    review_model: "omni-moderation-latest",
    engine: "openai_moderation",
    supported_inputs: ["text"],
    categories: [
      "harassment",
      "harassment/threatening",
      "hate",
      "hate/threatening",
      "illicit",
      "illicit/violent",
      "self-harm",
      "self-harm/intent",
      "self-harm/instructions",
      "sexual",
      "sexual/minors",
      "violence",
      "violence/graphic",
    ],
  },
  "/api/security/admin/violation-fee-appeals": [],
};

// Explicit read-only fixtures, not production fallbacks. Unknown requests still
// reach the persona adapter and fail closed; no payment or administrative write
// is added here. Activated only by console_review=1 in the development entry.
const reads: Record<string, unknown> = {
  "/api/user/company-billing-profile": null,
  "/api/user/topup/info": DEBUG_WALLET_TOPUP_INFO,
  "/api/user/aff": "local-preview-referral",
  "/api/user/2fa/status": {
    enabled: false,
    locked: false,
    backup_codes_remaining: 0,
  },
  "/api/user/gift": [],
  "/api/user/oauth/bindings": [],
  "/api/user/passkey": { enabled: false, credentials: [] },
  "/api/user/checkin": { enabled: false, checked_in_today: false, records: [] },
  "/api/subscription/self": {
    billing_preference: "wallet_only",
    subscriptions: [],
    all_subscriptions: [],
  },
  "/api/subscription/self/reset-vouchers": [],
  "/api/subscription/plans": [],
  "/api/subscription/admin/plans": [],
  "/api/subscription/admin/reset/eligible": page,
  "/api/log/self/stat": { quota: 15000, rpm: 0, tpm: 0 },
  "/api/log/stat": { quota: 15000, rpm: 0, tpm: 0 },
  "/api/task/self": page,
  "/api/task/": page,
  "/api/remote-control/v1/pi/sessions": { sessions: [] },
  "/api/open-source-bounties/config": {
    rate_percent: 1,
    rate_basis_points: 100,
  },
  "/api/open-source-bounties": page,
  "/api/open-source-bounties/mine": [],
  "/api/open-source-bounties/accepted": [],
  "/api/open-source-bounties/disputes/mine": [],
  "/api/open-source-bounties/tips/received": [],
  "/api/open-source-bounties/mcp-token": {
    configured: false,
    endpoint: "/mcp",
    token: null,
  },
  "/api/public-relays": { items: [], group: "default" },
  "/api/public-relays/mine": { items: [], group: "default" },
  "/api/public-relays/config": {
    enabled: true,
    group: "default",
    commission_rate: 0,
    fee_bps: 100,
  },
  "/api/prefill_group": { default: 1 },
  "/api/channel/models": modelNames,
  "/api/group/": ["default"],
  "/api/user/groups": ["default"],
  "/api/tool-market/config": {
    enabled: true,
    fee_bps: 100,
    recipient_id: 0,
    quota_per_unit: 500000,
    web_client_id: "console-preview",
    mcp_path: "/mcp/market",
    credits_per_usd: "500000",
    capabilities: { mcp_oauth: true },
    provider_presets: [
      {
        id: "monid",
        name: "Monid",
        endpoint: "https://mcp.monid.ai/v1",
        execute_tool: "monid_run",
        inspect_tool: "monid_inspect",
        read_tools: ["monid_discover", "monid_inspect"],
        documentation: "https://docs.monid.ai",
        oauth: true,
      },
      {
        id: "agentkey",
        name: "AgentKey",
        endpoint: "https://api.agentkey.app/v1/mcp",
        execute_tool: "execute_tool",
        inspect_tool: "describe_tool",
        read_tools: ["find_tools", "list_tools", "describe_tool"],
        documentation: "https://docs.agentkey.app",
        oauth: true,
      },
    ],
  },
  "/api/tool-market": [],
  "/api/tool-market/mine/grants": [],
  "/api/tool-market/mine/installations": [],
  "/api/tool-market/mine/favorites": [],
  "/api/tool-market/mine/services": [],
  "/api/tool-market/mine/calls": [],
  "/api/tool-market/mine/tokens": [],
  "/api/tool-market/mine/oauth-clients": [],
  "/api/tool-market/mine/income": [],
  "/api/tool-market/mine/budgets": [],
  "/api/rankings": {
    models: [],
    vendors: [],
    top_movers: [],
    top_droppers: [],
    models_history: { points: [], models: [], buckets: 0 },
    vendor_share_history: { points: [], vendors: [], buckets: 0 },
  },
  "/api/assistant/models": modelNames,
  "/api/assistant/weekly-discount": null,
  "/api/assistant/support/eligibility": {
    eligible: false,
    reason: "Local preview",
  },
  "/api/assistant/support/self": [],
  "/api/channel/ops": { retry_times: 0 },
  "/api/channel": page,
  "/api/models/": page,
  "/api/vendors/": page,
  "/api/deployments/settings": { enabled: false },
  "/api/deployments/": page,
  "/api/user/": page,
  "/api/redemption/": page,
  "/api/discount-code/": page,
  "/api/red-packet/admin": [],
  "/api/system-info/instances": [],
  "/api/system-task/list": [],
  "/api/system-task/current": null,
  "/api/custom-oauth-provider/": [],
  "/api/option/channel_affinity_cache": { entries: [], total: 0, size: 0 },
  "/api/security/admin/moderation/models": {
    group: "default",
    models: ["omni-moderation-latest", "omni-moderation-2024-09-26"],
  },
  "/api/security/policy": {
    policy_version: "preview",
    reference_effective_date: "",
    reference_url: "",
    alignment: "",
    moderation: {
      enabled: false,
      assistant_enabled: false,
      engine: "openai_moderation",
      async: true,
      group_policies: {},
      supported_inputs: ["text"],
      notice_only: true,
    },
  },
  "/api/security/stats": {
    moderation: {
      pending: 0,
      running: 0,
      completed: 0,
      failed: 0,
      cancelled: 0,
      flagged: 0,
      fined: 0,
      charged_quota: 0,
    },
  },
  "/api/security/admin/moderation-reviews": {
    rows: [],
    total: 0,
    page: 1,
    page_size: 20,
  },
  "/api/security/admin/moderation-stats": {
    pending: 0,
    running: 0,
    completed: 0,
    failed: 0,
    cancelled: 0,
    flagged: 0,
    fined: 0,
    charged_quota: 0,
  },
  "/api/assistant/admin/registration-events": [],
  "/api/option/hero-sms": {
    enabled: false,
    email_enabled: false,
    sms_enabled: false,
    api_key_configured: false,
    pending_work: false,
    currency: "USD",
    currency_code: 840,
    price_multiplier: 1,
  },
  "/api/performance/logs": [],
  "/api/performance/stats": {
    enabled: false,
    total: 0,
    requests: 0,
    cache: {},
    system: {},
  },
  "/api/scripts/repository": {
    repository_url: "",
    branch: "main",
    github_key_set: false,
  },
  "/api/scripts": [],
  "/api/hero-sms/email/products": { items: [] },
  "/api/hero-sms/email/activations": page,
  "/api/hero-sms/email/activations/current": null,
  "/api/drawing/self/settings": { enabled: false },
  "/api/option/pricing": {
    schema_version: 2,
    currency: "USD",
    storage_basis: "legacy_pricing_unit",
    revision: "a".repeat(64),
    credits_per_usd: 500000,
    legacy_pricing_units_per_usd: 500000,
    model_ratio_usd_per_million: 2,
    tool_price_defaults: {},
    values: {
      ModelRatio: '{"gpt-5-mini":0.25}',
      CompletionRatio: '{"gpt-5-mini":2}',
      ModelPrice: "{}",
      CacheRatio: "{}",
      CreateCacheRatio: "{}",
      ImageRatio: "{}",
      AudioRatio: "{}",
      AudioCompletionRatio: "{}",
      "billing_setting.billing_mode": "{}",
      "billing_setting.billing_expr": "{}",
      ModelPriceLock: "{}",
      "tool_price_setting.prices": "{}",
    },
  },
  "/api/option/": [
    { key: "SystemName", value: "LMM Best" },
    { key: "QuotaPerUnit", value: "500000" },
  ],
  "/api/ai-directory/ads": { items: [], has_more: false, next_offset: 0 },
  "/api/ai-directory/ads/mine": { items: [] },
  "/api/ai-directory": { links: null },
};

// Synthetic amounts for the local-only console gallery, never production data.
const overviewStatistics = {
  as_of: stamp,
  credits_per_usd: 500000,
  total_used_credits: "896123456",
  total_balance_credits: "1024567890",
  recharge: {
    currencies: [
      {
        currency: "CNY",
        gross_amount_micros: "1480500000",
        refunded_amount_micros: "36000000",
        net_amount_micros: "1444500000",
        orders: 36,
      },
      {
        currency: "USD",
        gross_amount_micros: "283450000",
        refunded_amount_micros: "8250000",
        net_amount_micros: "275200000",
        orders: 12,
      },
    ],
    virtual_units: [
      {
        currency: "LDC",
        gross_amount_micros: "31250000",
        refunded_amount_micros: "0",
        net_amount_micros: "31250000",
        orders: 3,
      },
    ],
    confirmed_orders: 51,
    unconfirmed_orders: 4,
    invalid_orders: 1,
  },
} satisfies AdminSiteStatistics;

export function consolePageFixture(
  config: InternalAxiosRequestConfig,
): unknown {
  if ((config.method ?? "get").toUpperCase() !== "GET") return undefined;
  const url = new URL(config.url ?? "", window.location.origin);
  if (url.origin !== window.location.origin) return undefined;
  const user = useAuthStore.getState().auth.user;
  const path = url.pathname;
  if (path === "/api/ratio_sync/service_tiers") {
    if (url.username || url.password || (user?.role ?? 0) < ROLE.SUPER_ADMIN) {
      return undefined;
    }
    // An unsynchronized, disabled installation. Never fetch provider prices or
    // grant accelerated access while reviewing console pages.
    return {
      success: true,
      data: {
        policy: {
          enabled: false,
          fast_markup: 1.2,
          ultrafast_markup: 1.2,
          fast_groups: [],
          ultrafast_groups: [],
        },
        catalog: {
          source: "",
          fetched_at: "0001-01-01T00:00:00Z",
          sha256: "",
          models: {},
        },
        fresh: false,
        max_age_hours: 24,
        groups: { default: 1 },
      },
    };
  }
  if (path === "/api/finance/site-statistics") {
    if (
      url.username ||
      url.password ||
      (user?.role !== ROLE.ADMIN && user?.role !== ROLE.SUPER_ADMIN)
    ) {
      return undefined;
    }
    return { success: true, data: structuredClone(overviewStatistics) };
  }
  if (Object.hasOwn(storeAndSecurityReads, path)) {
    if (url.username || url.password) return undefined;
    // Both security GET routes require AdminAuth on the real router. Falling
    // through preserves the persona adapter's fail-closed behavior for users.
    if (path !== "/api/store/config" && (user?.role ?? 0) < ROLE.ADMIN) {
      return undefined;
    }
    return {
      success: true,
      message: "",
      data: structuredClone(storeAndSecurityReads[path]),
    };
  }
  if (
    path === "/api/ai-directory/ads" &&
    new URLSearchParams(window.location.search).get("ads_preview") === "1"
  ) {
    const now = Math.floor(Date.now() / 1000);
    return {
      success: true,
      data: {
        items: [
          {
            id: 1,
            name: "Example Studio",
            url: "https://example.com",
            summary: "A sample promoted creative workspace.",
            description: "Synthetic preview placement for layout review.",
            bid_cents: 500,
            charged_quota: 2500000,
            status: "active",
            paid_at: now - 60,
            expires_at: now + 30 * 86400,
            hidden_at: 0,
            refunded_at: 0,
          },
          {
            id: 2,
            name: "Research Notes",
            url: "https://example.org",
            summary: "A sample promoted research resource.",
            description: "",
            bid_cents: 150,
            charged_quota: 750000,
            status: "active",
            paid_at: now - 30,
            expires_at: now + 30 * 86400,
            hidden_at: 0,
            refunded_at: 0,
          },
        ],
        has_more: false,
        next_offset: 2,
      },
    };
  }
  if (path === "/api/ai-directory/ads/quote") {
    const bidCents = Number(url.searchParams.get("bid_cents"));
    if (!Number.isInteger(bidCents) || bidCents < 100 || bidCents > 1_000_000) {
      return undefined;
    }
    return {
      success: true,
      data: {
        bid_cents: bidCents,
        quota: bidCents * 5000,
        currency: "USD",
        duration_days: 30,
        min_bid_cents: 100,
        max_bid_cents: 1_000_000,
      },
    };
  }
  if (path === "/api/todos") {
    const category = url.searchParams.get("category") || "all";
    return {
      success: true,
      data: {
        ...page,
        category,
        page_size: 50,
        unread_count: 0,
        total_unread_count: 0,
        unread_by_category: {},
        categories: [],
      },
    };
  }
  if (path === "/api/pricing" || path === "/api/assistant/pricing") {
    return {
      success: true,
      data: [
        {
          id: 1,
          model_name: "gpt-5-mini",
          quota_type: 0,
          model_ratio: 0.5,
          completion_ratio: 2,
          enable_groups: ["default"],
          supported_endpoint_types: ["openai"],
        },
        {
          id: 2,
          model_name: "image-2",
          quota_type: 1,
          model_ratio: 1,
          completion_ratio: 1,
          model_price: 0.1,
          enable_groups: ["default"],
          supported_endpoint_types: ["image-generation"],
        },
      ],
      vendors: [],
      group_ratio: { default: 1 },
      usable_group: { default: { desc: "Preview", ratio: 1 } },
      supported_endpoint: {},
      auto_groups: [],
    };
  }
  if (path === "/api/pricing/runtime") {
    return { success: true, data: { models: [] } };
  }
  if (path === "/api/token/") {
    const key = {
      id: 8001,
      name: "开发环境 · Preview",
      key: "PREVIEW_NOT_A_SECRET",
      status: 1,
      remain_quota: 500000,
      used_quota: 125000,
      unlimited_quota: false,
      expired_time: -1,
      created_time: stamp - 86400,
      accessed_time: stamp,
      group: "default",
      model_limits_enabled: false,
      model_limits: "",
      allow_ips: "",
      creation_source: "manual",
    };
    return { success: true, data: { ...page, items: [key], total: 1 } };
  }
  if (path === "/api/user/sessions") {
    return {
      success: true,
      session_auto_logout: true,
      data: [
        {
          sid: "preview-session",
          current: true,
          login_method: "oauth",
          ip: "127.0.0.1",
          user_agent: "Local browser preview",
          created_at: stamp - 3600,
          last_active_at: stamp,
          expires_at: stamp + 86400,
        },
      ],
    };
  }
  if (["/api/data/self", "/api/data", "/api/data/users"].includes(path)) {
    const bound = (key: string, fallback: number) => {
      const params: unknown = config.params;
      const value =
        (params instanceof URLSearchParams
          ? params.get(key)
          : params && typeof params === "object"
            ? (params as Record<string, unknown>)[key]
            : undefined) ?? url.searchParams.get(key);
      return value === null || value === undefined ? fallback : Number(value);
    };
    const start = bound("start_timestamp", Number.NEGATIVE_INFINITY);
    const end = bound("end_timestamp", Number.POSITIVE_INFINITY);
    return {
      success: true,
      data: Array.from({ length: 7 }, (_, index) => ({
        id: index + 1,
        user_id: user?.id ?? 1002,
        username: "preview",
        model_name: modelNames[index % 2],
        created_at: stamp - (6 - index) * 86400,
        token_used: 2400 + index * 380,
        count: 8 + index * 2,
        quota: 3000 + index * 80,
      })).filter((row) => row.created_at >= start && row.created_at <= end),
    };
  }
  if (["/api/data/flow/self", "/api/data/flow"].includes(path)) {
    return {
      success: true,
      data: [
        {
          user_id: user?.id ?? 1002,
          username: "preview",
          node_name: "local-preview",
          use_group: "default",
          token_id: 1,
          token_name: "Preview key",
          channel_id: 1,
          channel_name: "Preview",
          model_name: modelNames[0],
          token_used: 3600,
          count: 12,
          quota: 600,
        },
      ],
    };
  }
  if (Object.hasOwn(reads, path)) {
    return { success: true, data: structuredClone(reads[path]) };
  }
  return undefined;
}

export function withConsolePageFixtures(fallback: AxiosAdapter): AxiosAdapter {
  return async (config) => {
    const data = consolePageFixture(config);
    if (data === undefined) return fallback(config);
    return {
      data,
      status: 200,
      statusText: "Local fixture",
      headers: {},
      config,
    };
  };
}
