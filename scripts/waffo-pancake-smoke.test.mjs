/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/

import assert from "node:assert/strict";
import { generateKeyPairSync } from "node:crypto";
import { after, before, mock, test } from "node:test";

import { TaxCategory, WebhookEventType } from "@waffo/pancake-ts";

import {
  WAFFO_PANCAKE_ACTIVE_PRODUCTS_QUERY,
  WAFFO_PANCAKE_STORES_QUERY,
  findMatchingTestWebhook,
  runSmoke,
  WAFFO_PANCAKE_WEBHOOK_EVENTS,
} from "./waffo-pancake-smoke.mjs";

const EXPECTED_EVENTS = {
  OrderCompleted: "order.completed",
  SubscriptionActivated: "subscription.activated",
  SubscriptionRenewed: "subscription.renewed",
  SubscriptionRecovered: "subscription.recovered",
  SubscriptionPlanChanged: "subscription.plan_changed",
  SubscriptionPlanChangeScheduled: "subscription.plan_change_scheduled",
  SubscriptionPlanChangeFailed: "subscription.plan_change_failed",
  SubscriptionCanceling: "subscription.canceling",
  SubscriptionUncanceled: "subscription.uncanceled",
  SubscriptionPastDue: "subscription.past_due",
  SubscriptionCanceled: "subscription.canceled",
  SubscriptionPaymentSucceeded: "subscription.payment_succeeded",
  RefundSucceeded: "refund.succeeded",
  RefundFailed: "refund.failed",
};
const MERCHANT_ID = `MER_${"1".repeat(22)}`;
const STORE = {
  id: `STO_${"2".repeat(22)}`,
  name: "Unit test store",
  status: "active",
};
const PRODUCT = {
  id: `PROD_${"3".repeat(22)}`,
  name: "Unit test product",
  status: "active",
};
const WEBHOOK_URL = "https://example.test/waffo";
const CHECKOUT_URL = "https://checkout.example.test/session/unit-test";
const CHECKOUT_TOKEN = "unit-test-only-secret-checkout-token";
const EXPIRES_AT = "2026-09-09T12:45:00Z";
// Ephemeral fixture only: no environment credentials, persisted key, or network.
const { privateKey } = generateKeyPairSync("rsa", {
  modulusLength: 2048,
  privateKeyEncoding: { type: "pkcs8", format: "pem" },
  publicKeyEncoding: { type: "spki", format: "pem" },
});
const ENV = { WAFFO_MERCHANT_ID: MERCHANT_ID, WAFFO_PRIVATE_KEY: privateKey };
const UNSAFE_NOTICE = {
  message: `untrusted provider echo: ${privateKey} ${CHECKOUT_TOKEN}`,
  layer: "gateway",
  aiHint: `DO_NOT_EXECUTE_PROVIDER_HINT ${CHECKOUT_TOKEN}`,
};

before(() => {
  // Fail closed even if a future refactor accidentally drops the injected fetch.
  mock.method(globalThis, "fetch", async () => {
    throw new Error("Live fetch is forbidden in smoke unit tests");
  });
});
after(() => mock.restoreAll());

function harness({
  stores = [STORE],
  products = [PRODUCT],
  webhooks = [],
  warnings = [],
  overrides = {},
} = {}) {
  const calls = [];
  const output = { log: [], warn: [], error: [] };
  const logger = Object.fromEntries(
    Object.keys(output).map((level) => [
      level,
      (message) => output[level].push(message),
    ]),
  );
  const replies = {
    stores: { data: { stores } },
    products: { data: { onetimeProducts: products } },
    webhooks: { data: { store: { storeWebhooks: webhooks } } },
    "create-store": { data: { store: STORE } },
    "create-product": { data: { product: PRODUCT } },
    "add-webhook": { data: { webhook: { id: "created-webhook" } } },
    "update-webhook": { data: { webhook: { id: "existing-webhook" } } },
    "issue-session-token": {
      data: { token: CHECKOUT_TOKEN, expiresAt: EXPIRES_AT },
    },
    "create-session": {
      data: {
        sessionId: "unit-test-session",
        checkoutUrl: CHECKOUT_URL,
        expiresAt: EXPIRES_AT,
      },
    },
    ...overrides,
  };
  const fetch = async (url, init) => {
    const { origin, pathname } = new URL(url);
    assert.equal(origin, "https://api.waffo.ai");
    assert.equal(init.method, "POST");
    const body = JSON.parse(init.body);
    let operation = pathname.split("/").at(-1);
    if (pathname === "/v1/graphql") {
      if (body.query === WAFFO_PANCAKE_STORES_QUERY) operation = "stores";
      else if (body.query === WAFFO_PANCAKE_ACTIVE_PRODUCTS_QUERY)
        operation = "products";
      else if (body.query.includes("query GetStoreWebhooks("))
        operation = "webhooks";
    }
    calls.push({
      operation,
      pathname,
      body,
      headers: new Headers(init.headers),
    });
    assert.ok(
      Object.hasOwn(replies, operation),
      `unexpected SDK call: ${pathname}`,
    );
    const reply = replies[operation];
    if (reply instanceof Error) throw reply;
    return Response.json(
      { warnings, ...reply },
      { status: reply.errors?.length ? 400 : 200 },
    );
  };
  return {
    calls,
    output,
    run: (argv = [], env = ENV) => runSmoke({ argv, env, fetch, logger }),
  };
}

function request(h, operation) {
  const matches = h.calls.filter((call) => call.operation === operation);
  assert.equal(matches.length, 1, `expected one ${operation} call`);
  return matches[0];
}

function assertNoSecrets(h) {
  const output = JSON.stringify(h.output);
  assert.ok(!output.includes(CHECKOUT_TOKEN));
  for (const line of privateKey.trim().split("\n"))
    assert.ok(!output.includes(line));
  assert.doesNotMatch(output, /#token=|DO_NOT_EXECUTE_PROVIDER_HINT/);
}

function assertOnlyTestCalls(h) {
  const allowedPaths = new Set([
    "/v1/graphql",
    "/v1/actions/store/create-store",
    "/v1/actions/onetime-product/create-product",
    "/v1/actions/store/add-webhook",
    "/v1/actions/store/update-webhook",
    "/v1/actions/auth/issue-session-token",
    "/v1/actions/checkout/create-session",
  ]);
  for (const call of h.calls) {
    assert.ok(
      allowedPaths.has(call.pathname),
      `not a managed Test operation: ${call.pathname}`,
    );
    assert.equal(call.headers.get("X-Merchant-Id"), MERCHANT_ID);
    // Merchant API-key auth derives its environment from the key. No customer()
    // or deprecated buyer() session / Bearer requests belong in this runner.
    assert.equal(call.headers.get("Authorization"), null);
    if (call.pathname === "/v1/graphql") {
      assert.equal(call.headers.get("X-Idempotency-Key"), null);
    }
  }
  assert.doesNotMatch(JSON.stringify(h.calls), /publish|subscription-product/);
}

test("managed webhook list is the full canonical v0.20.0 lifecycle/payment/refund set", () => {
  for (const [name, wireValue] of Object.entries(EXPECTED_EVENTS)) {
    assert.equal(WebhookEventType[name], wireValue, name);
  }
  assert.deepEqual(
    [...WAFFO_PANCAKE_WEBHOOK_EVENTS],
    Object.values(EXPECTED_EVENTS),
  );
  assert.equal(new Set(WAFFO_PANCAKE_WEBHOOK_EVENTS).size, 14);
  assert.ok(Object.isFrozen(WAFFO_PANCAKE_WEBHOOK_EVENTS));
  assert.equal(Object.hasOwn(WebhookEventType, "SubscriptionUpdated"), false);
  assert.ok(!WAFFO_PANCAKE_WEBHOOK_EVENTS.includes("subscription.updated"));
});

test("matching webhook requires the exact URL, HTTP channel, and boolean Test mode", () => {
  const matching = {
    id: "existing",
    channel: "http",
    url: WEBHOOK_URL,
    testMode: true,
  };
  const nonmatching = [
    null,
    { ...matching, id: "prod", testMode: false },
    { ...matching, id: "string-mode", testMode: "true" },
    { ...matching, id: "number-mode", testMode: 1 },
    { ...matching, id: "missing-mode", testMode: undefined },
    { ...matching, id: "other-channel", channel: "discord" },
    { ...matching, id: "other-url", url: "https://other.test/waffo" },
  ];
  assert.equal(findMatchingTestWebhook(nonmatching, WEBHOOK_URL), undefined);
  assert.equal(
    findMatchingTestWebhook([...nonmatching, matching], ` ${WEBHOOK_URL} `),
    matching,
  );
  assert.equal(
    findMatchingTestWebhook([matching], "https://missing.test/waffo"),
    undefined,
  );
  assert.equal(findMatchingTestWebhook([matching], "  "), undefined);
  assert.equal(findMatchingTestWebhook(undefined, WEBHOOK_URL), undefined);
});

test("catalog queries use supported root store and active one-time product fields", () => {
  assert.match(
    WAFFO_PANCAKE_STORES_QUERY,
    /^query \{ stores \{ id name status \} \}$/,
  );
  assert.doesNotMatch(WAFFO_PANCAKE_STORES_QUERY, /onetimeProducts/);
  assert.match(WAFFO_PANCAKE_ACTIVE_PRODUCTS_QUERY, /onetimeProducts\(filter:/);
  assert.match(
    WAFFO_PANCAKE_ACTIVE_PRODUCTS_QUERY,
    /storeId: \{ eq: \$storeId \}/,
  );
  assert.match(
    WAFFO_PANCAKE_ACTIVE_PRODUCTS_QUERY,
    /status: \{ eq: "active" \}/,
  );
});

test("default checkout uses the official SDK factory and never logs its token", async () => {
  const h = harness();
  assert.equal(await h.run(), 0);
  assert.deepEqual(h.calls.map((call) => call.operation).sort(), [
    "create-session",
    "issue-session-token",
    "products",
    "stores",
  ]);
  assert.deepEqual(request(h, "products").body.variables, {
    storeId: STORE.id,
  });
  const auth = request(h, "issue-session-token").body;
  assert.match(auth.buyerIdentity, /^lmm-pancake-smoke-\d+$/);
  assert.deepEqual(Object.keys(auth).sort(), ["buyerIdentity", "productId"]);
  const checkout = request(h, "create-session").body;
  assert.equal(checkout.buyerEmail, "checkout-test@example.invalid");
  assert.match(checkout.orderMerchantExternalId, /^lmm-pancake-test-\d+$/);
  assert.deepEqual(checkout.priceSnapshot, {
    amount: "1.00",
    taxCategory: TaxCategory.SaaS,
  });
  assert.equal(checkout.currency, "USD");
  assert.equal(checkout.productId, PRODUCT.id);
  assert.equal(checkout.expiresInSeconds, 45 * 60);
  assert.equal(checkout.darkMode, true);
  assert.equal(Object.hasOwn(checkout, "buyerIdentity"), false);
  const summary = JSON.parse(h.output.log.at(-1));
  assert.equal(summary.environment, "test");
  assert.equal(summary.checkout_url, CHECKOUT_URL);
  assert.equal(summary.session_id, "unit-test-session");
  assert.equal(summary.expires_at, EXPIRES_AT);
  assert.deepEqual(h.output.warn, []);
  assert.deepEqual(h.output.error, []);
  assertOnlyTestCalls(h);
  assertNoSecrets(h);
});

test("canonical identity, explicit email, external ID and PriceSnapshot reach separate SDK endpoints", async () => {
  const h = harness();
  assert.equal(
    await h.run(
      [
        "--store-id",
        STORE.id,
        "--product-id",
        PRODUCT.id,
        "--buyer-id",
        "merchant-buyer-123",
        "--buyer-email",
        "buyer@example.test",
        "--order-id",
        "local-order-123",
        "--amount",
        "2.50",
        "--return-url",
        "https://example.test/return",
        "--webhook-url",
        WEBHOOK_URL,
      ],
      { ...ENV, WAFFO_PANCAKE_ENV: " TEST " },
    ),
    0,
  );
  assert.deepEqual(request(h, "issue-session-token").body, {
    productId: PRODUCT.id,
    buyerIdentity: "merchant-buyer-123",
  });
  assert.deepEqual(request(h, "create-session").body, {
    productId: PRODUCT.id,
    currency: "USD",
    priceSnapshot: { amount: "2.50", taxCategory: TaxCategory.SaaS },
    buyerEmail: "buyer@example.test",
    orderMerchantExternalId: "local-order-123",
    successUrl: "https://example.test/return",
    darkMode: true,
    expiresInSeconds: 45 * 60,
  });
  // --webhook-url alone is informational, never permission to configure.
  assert.ok(!h.calls.some((call) => call.operation.includes("webhook")));
  assertOnlyTestCalls(h);
  assertNoSecrets(h);
});

test("empty Test catalog creates only a store and OnetimeProduct, without publish", async () => {
  const h = harness({ stores: [], warnings: [UNSAFE_NOTICE] });
  assert.equal(
    await h.run([], {
      ...ENV,
      WAFFO_PANCAKE_RETURN_URL: "https://example.test/return",
    }),
    0,
  );
  assert.deepEqual(h.calls.map((call) => call.operation).sort(), [
    "create-product",
    "create-session",
    "create-store",
    "issue-session-token",
    "stores",
  ]);
  assert.deepEqual(request(h, "create-store").body, {
    name: "LMM Forge test store",
  });
  assert.deepEqual(request(h, "create-product").body, {
    storeId: STORE.id,
    name: "LMM Forge test checkout",
    description: "Temporary Test checkout product for LMM Forge",
    prices: { USD: { amount: "1.00", taxCategory: TaxCategory.SaaS } },
    successUrl: "https://example.test/return",
  });
  for (const operation of [
    "graphql.stores",
    "stores.create",
    "onetimeProducts.create",
  ]) {
    assert.ok(
      h.output.warn.some((line) =>
        line.includes(`${operation}: 1 SDK warning(s)`),
      ),
    );
  }
  // The authenticated wrapper merges notices from token and session creation.
  assert.ok(
    h.output.warn.some((line) =>
      line.includes("checkout.authenticated.create: 2 SDK warning(s)"),
    ),
  );
  assertOnlyTestCalls(h);
  assertNoSecrets(h);
});

test("explicit webhook configuration adds the full event set, never reusing prod or non-HTTP hooks", async () => {
  const h = harness({
    webhooks: [
      { id: "prod", channel: "http", url: WEBHOOK_URL, testMode: false },
      { id: "discord", channel: "discord", url: WEBHOOK_URL, testMode: true },
    ],
    warnings: [UNSAFE_NOTICE],
  });
  assert.equal(
    await h.run(["--configure-webhook", "--webhook-url", ` ${WEBHOOK_URL} `]),
    0,
  );
  assert.deepEqual(request(h, "add-webhook").body, {
    storeId: STORE.id,
    channel: "http",
    url: WEBHOOK_URL,
    events: Object.values(EXPECTED_EVENTS),
    testMode: true,
  });
  assert.ok(!h.calls.some((call) => call.operation === "update-webhook"));
  assert.deepEqual(request(h, "webhooks").body.variables, {
    storeId: STORE.id,
  });
  for (const operation of [
    "graphql.stores",
    "graphql.onetimeProducts",
    "graphql.storeWebhooks",
    "webhooks.add",
  ]) {
    assert.ok(
      h.output.warn.some((line) =>
        line.includes(`${operation}: 1 SDK warning(s)`),
      ),
    );
  }
  assertOnlyTestCalls(h);
  assertNoSecrets(h);
});

test("matching Test HTTP webhook updates events only instead of creating a duplicate", async () => {
  const h = harness({
    webhooks: [
      { id: "prod-first", channel: "http", url: WEBHOOK_URL, testMode: false },
      {
        id: "existing-webhook",
        channel: "http",
        url: WEBHOOK_URL,
        testMode: true,
      },
    ],
    warnings: [UNSAFE_NOTICE],
  });
  assert.equal(
    await h.run(["--configure-webhook", "--webhook-url", WEBHOOK_URL]),
    0,
  );
  assert.deepEqual(request(h, "update-webhook").body, {
    id: "existing-webhook",
    events: Object.values(EXPECTED_EVENTS),
  });
  assert.ok(!h.calls.some((call) => call.operation === "add-webhook"));
  assert.ok(
    h.output.warn.some((line) =>
      line.includes("webhooks.update: 1 SDK warning(s)"),
    ),
  );
  assertOnlyTestCalls(h);
  assertNoSecrets(h);
});

test("non-Test environments fail before SDK construction or any fetch", async () => {
  for (const environment of [
    "prod",
    "production",
    "staging",
    "false",
    "test,prod",
    " ",
  ]) {
    const h = harness();
    assert.equal(
      await h.run([], { WAFFO_PANCAKE_ENV: environment }),
      1,
      environment,
    );
    assert.deepEqual(h.calls, []);
    assert.match(h.output.error.join("\n"), /test-only/);
  }
});

test("help performs no SDK operation even without credentials or with a prod environment", async () => {
  const h = harness();
  assert.equal(await h.run(["--help"], { WAFFO_PANCAKE_ENV: "prod" }), 0);
  assert.deepEqual(h.calls, []);
  assert.match(h.output.log.join("\n"), /Usage:/);
  assert.deepEqual(h.output.error, []);
  assertNoSecrets(h);
});

test("missing credentials, invalid flags, and incomplete webhook opt-in fail before catalog mutations", async () => {
  for (const [argv, env, message] of [
    [[], {}, /WAFFO_MERCHANT_ID is required/],
    [[], { WAFFO_MERCHANT_ID: MERCHANT_ID }, /WAFFO_PRIVATE_KEY is required/],
    [["--publish"], ENV, /unknown option/],
    [["--amount"], ENV, /requires a value/],
    [["--configure-webhook"], ENV, /requires --webhook-url/],
    [
      ["--configure-webhook", "--webhook-url", "  "],
      ENV,
      /requires --webhook-url/,
    ],
  ]) {
    const h = harness({ stores: [] });
    assert.equal(await h.run(argv, env), 1);
    assert.deepEqual(h.calls, []);
    assert.match(h.output.error.join("\n"), message);
  }
});

test("selected products must belong to the active one-time catalog, including environment selection", async () => {
  const h = harness();
  assert.equal(
    await h.run([], {
      ...ENV,
      WAFFO_PANCAKE_STORE_ID: STORE.id,
      WAFFO_PANCAKE_PRODUCT_ID: PRODUCT.id,
    }),
    0,
  );
  for (const [products, argv] of [
    [[], ["--product-id", PRODUCT.id]],
    [[], ["--store-id", STORE.id, "--product-id", PRODUCT.id]],
    [[{ ...PRODUCT, status: "inactive" }], ["--product-id", PRODUCT.id]],
    [[PRODUCT], ["--product-id", `PROD_${"4".repeat(22)}`]],
  ]) {
    const missing = harness({ products });
    assert.equal(await missing.run(argv), 1);
    assert.ok(missing.calls.every((call) => call.pathname === "/v1/graphql"));
    assert.match(missing.output.error.join("\n"), /active one-time product/);
  }
});

test("ambiguous catalogs are rejected rather than guessed or mutated", async () => {
  for (const options of [
    { stores: [STORE, { ...STORE, id: `STO_${"4".repeat(22)}` }] },
    { products: [PRODUCT, { ...PRODUCT, id: `PROD_${"4".repeat(22)}` }] },
  ]) {
    const h = harness(options);
    assert.equal(await h.run(), 1);
    assert.ok(h.calls.every((call) => call.pathname === "/v1/graphql"));
    assert.match(h.output.error.join("\n"), /instead of guessing/);
  }
});

test("GraphQL errors and warnings cannot leak provider text, private keys, tokens, or aiHints", async () => {
  for (const operation of ["stores", "products", "webhooks"]) {
    const h = harness({
      overrides: {
        [operation]: { errors: [UNSAFE_NOTICE], warnings: [UNSAFE_NOTICE] },
      },
    });
    assert.equal(
      await h.run(["--configure-webhook", "--webhook-url", WEBHOOK_URL]),
      1,
    );
    assert.match(h.output.error.join("\n"), /GraphQL error details omitted/);
    assert.equal(h.output.warn.length, 1);
    assert.ok(h.calls.every((call) => call.pathname === "/v1/graphql"));
    assertNoSecrets(h);
  }
});

test("SDK action failures and transport errors never print raw messages or stacks", async () => {
  for (const operation of [
    "create-store",
    "create-product",
    "add-webhook",
    "update-webhook",
    "issue-session-token",
    "create-session",
  ]) {
    const h = harness({
      stores: operation === "create-store" ? [] : [STORE],
      products: operation === "create-product" ? [] : [PRODUCT],
      webhooks:
        operation === "update-webhook"
          ? [
              {
                id: "existing-webhook",
                channel: "http",
                url: WEBHOOK_URL,
                testMode: true,
              },
            ]
          : [],
      overrides: { [operation]: { errors: [UNSAFE_NOTICE] } },
    });
    assert.equal(
      await h.run(["--configure-webhook", "--webhook-url", WEBHOOK_URL]),
      1,
    );
    assert.match(h.output.error.join("\n"), /SDK request failed \(HTTP 400\)/);
    assertNoSecrets(h);
  }
  const h = harness({
    overrides: { stores: new Error(UNSAFE_NOTICE.message) },
  });
  assert.equal(await h.run(), 1);
  assert.match(h.output.error.join("\n"), /SDK or transport failure/);
  assertNoSecrets(h);
});
