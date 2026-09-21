#!/usr/bin/env node

import {
  TaxCategory,
  WaffoPancake,
  WaffoPancakeError,
  WebhookEventType,
} from "@waffo/pancake-ts";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

const DEFAULT_PRODUCT_NAME = "LMM Forge test checkout";
const DEFAULT_STORE_NAME = "LMM Forge test store";
const DEFAULT_AMOUNT = "1.00";
const TEST_CARD = "4576750000000110";

// Match the Go v0.11 domain handlers using the TS v0.20.0 event names.
// Period/status belong to lifecycle events, not subscription.payment_succeeded.
// Managing these subscriptions does not make this one-time smoke a recurring
// checkout or validate the backend's lifecycle/payment reconciliation.
export const WAFFO_PANCAKE_WEBHOOK_EVENTS = Object.freeze([
  WebhookEventType.OrderCompleted,
  WebhookEventType.SubscriptionActivated,
  WebhookEventType.SubscriptionRenewed,
  WebhookEventType.SubscriptionRecovered,
  WebhookEventType.SubscriptionPlanChanged,
  WebhookEventType.SubscriptionPlanChangeScheduled,
  WebhookEventType.SubscriptionPlanChangeFailed,
  WebhookEventType.SubscriptionCanceling,
  WebhookEventType.SubscriptionUncanceled,
  WebhookEventType.SubscriptionPastDue,
  WebhookEventType.SubscriptionCanceled,
  WebhookEventType.SubscriptionPaymentSucceeded,
  WebhookEventType.RefundSucceeded,
  WebhookEventType.RefundFailed,
]);

export const WAFFO_PANCAKE_STORES_QUERY = "query { stores { id name status } }";
export const WAFFO_PANCAKE_ACTIVE_PRODUCTS_QUERY = `query ($storeId: String!) {
  onetimeProducts(filter: { storeId: { eq: $storeId }, status: { eq: "active" } }) {
    id name status
  }
}`;

class SmokeError extends Error {}

function fail(message) {
  throw new SmokeError(message);
}

function usage(logger) {
  logger.log(`Usage: bun run waffo:pancake:smoke -- [options]

Creates a Waffo Pancake test checkout using the server-side SDK. The Waffo
API key must be a Test key; the SDK binds the environment to the key.

Required environment:
  WAFFO_MERCHANT_ID        Merchant ID from Dashboard → API & Development
  WAFFO_PRIVATE_KEY        RSA private key downloaded with the Test API key

Optional environment:
  WAFFO_PANCAKE_ENV        Must be "test" (default: test)
  WAFFO_PANCAKE_STORE_ID   Existing test store ID
  WAFFO_PANCAKE_PRODUCT_ID  Existing active test product ID
  WAFFO_PANCAKE_RETURN_URL Successful checkout return URL

Options:
  --store-id ID             Select an existing store
  --product-id ID           Select an existing active one-time product
  --amount USD              Test price snapshot (default: ${DEFAULT_AMOUNT})
  --buyer-email EMAIL       Pre-fill buyer email
  --buyer-id ID             Stable merchant-side buyer identity
  --order-id ID             Business order reference
  --return-url URL          Override the checkout return URL
  --webhook-url URL         Register this HTTP endpoint for test webhooks
  --configure-webhook       Add --webhook-url to the selected store
  --help                    Show this help

The script never prints WAFFO_PRIVATE_KEY or the short-lived checkout token.
The printed URL omits the authenticated buyer token fragment. SDK notices are
reported as counts only; their untrusted message/aiHint text is never printed.
After opening the checkout URL, use test card ${TEST_CARD} with any future
expiry and three-digit CVC. Project endpoint:
  https://api.lmm.best/api/waffo-pancake/webhook/test
`);
}

function parseArgs(argv) {
  const args = {};
  const valueFlags = new Set([
    "--store-id",
    "--product-id",
    "--amount",
    "--buyer-email",
    "--buyer-id",
    "--order-id",
    "--return-url",
    "--webhook-url",
  ]);
  const booleanFlags = new Set(["--configure-webhook", "--help"]);
  for (let i = 0; i < argv.length; i += 1) {
    const flag = argv[i];
    if (!valueFlags.has(flag) && !booleanFlags.has(flag))
      fail(`unknown option: ${flag}`);
    const key = flag.slice(2).replaceAll("-", "_");
    if (booleanFlags.has(flag)) {
      args[key] = true;
      continue;
    }
    const value = argv[i + 1];
    if (!value || value.startsWith("--")) fail(`${flag} requires a value`);
    args[key] = value;
    i += 1;
  }
  return args;
}

function requiredEnv(env, name) {
  const value = env[name]?.trim();
  if (!value)
    fail(
      `${name} is required; do not paste the private key into source control`,
    );
  return value;
}

function summarizeError(error) {
  if (error instanceof SmokeError) return error.message;
  // SDK/transport errors can echo credentials, response bodies, or JWTs that
  // were never returned to this caller. Do not attempt regex-only redaction.
  if (error instanceof WaffoPancakeError) {
    const status = Number.isInteger(error.status)
      ? ` (HTTP ${error.status})`
      : "";
    return `SDK request failed${status}; details omitted to protect credentials`;
  }
  return "SDK or transport failure; details omitted to protect credentials";
}

function reportWarnings(result, operation, logger) {
  if (!Array.isArray(result.warnings) || result.warnings.length === 0) return;
  // Notice.message and aiHint are untrusted free text, not executable guidance.
  // Counts and a local operation label surface advisories without leaking data.
  logger.warn(
    `Waffo Pancake ${operation}: ${result.warnings.length} SDK warning(s); message/aiHint omitted to protect credentials`,
  );
}

async function queryStores(client, logger) {
  const result = await client.graphql.query({
    query: WAFFO_PANCAKE_STORES_QUERY,
  });
  reportWarnings(result, "graphql.stores", logger);
  if (result.errors?.length) {
    fail(
      "unable to list Waffo stores; GraphQL error details omitted to protect credentials",
    );
  }
  const stores = result.data?.stores ?? [];
  for (const store of stores) {
    const products = await client.graphql.query({
      query: WAFFO_PANCAKE_ACTIVE_PRODUCTS_QUERY,
      variables: { storeId: store.id },
    });
    reportWarnings(products, "graphql.onetimeProducts", logger);
    if (products.errors?.length) {
      fail(
        "unable to list Waffo products; GraphQL error details omitted to protect credentials",
      );
    }
    store.onetimeProducts = products.data?.onetimeProducts ?? [];
  }
  return stores;
}

async function queryStoreWebhooks(client, storeId, logger) {
  const result = await client.graphql.query({
    query: `query GetStoreWebhooks($storeId: String!) {
      store(id: $storeId) {
        storeWebhooks { id channel url events testMode }
      }
    }`,
    variables: { storeId },
  });
  reportWarnings(result, "graphql.storeWebhooks", logger);
  if (result.errors?.length) {
    fail(
      "unable to list Waffo webhooks; GraphQL error details omitted to protect credentials",
    );
  }
  return result.data?.store?.storeWebhooks ?? [];
}

export function findMatchingTestWebhook(webhooks, webhookUrl) {
  const expectedURL = webhookUrl?.trim();
  if (!expectedURL) return undefined;
  return (webhooks ?? []).find(
    (webhook) =>
      webhook?.channel === "http" &&
      webhook?.testMode === true &&
      webhook?.url === expectedURL,
  );
}

async function ensureTestCatalog(client, args, env, logger) {
  const requestedStore = args.store_id || env.WAFFO_PANCAKE_STORE_ID?.trim();
  const requestedProduct =
    args.product_id || env.WAFFO_PANCAKE_PRODUCT_ID?.trim();
  const stores = await queryStores(client, logger);

  if (requestedProduct) {
    const store = requestedStore
      ? stores.find((item) => item.id === requestedStore)
      : stores.find((item) =>
          item.onetimeProducts?.some(
            (product) => product.id === requestedProduct,
          ),
        );
    if (!store && requestedStore)
      fail(`store ${requestedStore} was not found for the Test key`);
    if (
      !store?.onetimeProducts?.some(
        (product) =>
          product.id === requestedProduct &&
          product.status?.toLowerCase() === "active",
      )
    )
      fail(
        "selected product was not found as an active one-time product for the Test key and store",
      );
    return { storeId: store.id, productId: requestedProduct };
  }

  let store;
  if (requestedStore) {
    store = stores.find((item) => item.id === requestedStore);
    if (!store) fail(`store ${requestedStore} was not found for the Test key`);
  } else if (stores.length === 0) {
    const created = await client.stores.create({ name: DEFAULT_STORE_NAME });
    reportWarnings(created, "stores.create", logger);
    store = created.store;
    logger.log(`Created Test store: ${store.id}`);
  } else if (stores.length === 1) {
    store = stores[0];
  } else {
    fail(
      `merchant has ${stores.length} stores; pass --store-id instead of guessing`,
    );
  }

  const activeProducts = (store.onetimeProducts ?? []).filter(
    (product) => product.status?.toLowerCase() === "active",
  );
  if (activeProducts.length === 1)
    return { storeId: store.id, productId: activeProducts[0].id };
  if (activeProducts.length > 1) {
    fail(
      `store ${store.id} has multiple active products; pass --product-id instead of guessing`,
    );
  }

  const created = await client.onetimeProducts.create({
    storeId: store.id,
    name: DEFAULT_PRODUCT_NAME,
    description: "Temporary Test checkout product for LMM Forge",
    prices: { USD: { amount: DEFAULT_AMOUNT, taxCategory: TaxCategory.SaaS } },
    successUrl:
      args.return_url || env.WAFFO_PANCAKE_RETURN_URL?.trim() || undefined,
  });
  reportWarnings(created, "onetimeProducts.create", logger);
  logger.log(`Created active Test product: ${created.product.id}`);
  return { storeId: store.id, productId: created.product.id };
}

async function configureWebhook(client, storeId, webhookUrl, logger) {
  if (!storeId) fail("--configure-webhook requires a resolvable store ID");
  if (!webhookUrl) fail("--configure-webhook requires --webhook-url");
  const existing = findMatchingTestWebhook(
    await queryStoreWebhooks(client, storeId, logger),
    webhookUrl,
  );
  if (existing) {
    const result = await client.webhooks.update({
      id: existing.id,
      events: WAFFO_PANCAKE_WEBHOOK_EVENTS,
    });
    reportWarnings(result, "webhooks.update", logger);
    logger.log(
      `Updated existing Test webhook ${existing.id} for ${webhookUrl}`,
    );
    return;
  }
  const result = await client.webhooks.add({
    storeId,
    channel: "http",
    url: webhookUrl,
    events: WAFFO_PANCAKE_WEBHOOK_EVENTS,
    testMode: true,
  });
  reportWarnings(result, "webhooks.add", logger);
  logger.log(`Registered Test webhook ${result.webhook.id} for ${webhookUrl}`);
}

async function main({ argv, env, fetch, logger }) {
  const args = parseArgs(argv);
  if (args.help) {
    usage(logger);
    return;
  }
  const environment = (env.WAFFO_PANCAKE_ENV || "test").trim().toLowerCase();
  if (environment !== "test")
    fail(
      "this smoke runner is test-only; use a Test API key and WAFFO_PANCAKE_ENV=test",
    );
  args.webhook_url = args.webhook_url?.trim();
  if (args.configure_webhook && !args.webhook_url)
    fail("--configure-webhook requires --webhook-url");

  const client = new WaffoPancake({
    merchantId: requiredEnv(env, "WAFFO_MERCHANT_ID"),
    privateKey: requiredEnv(env, "WAFFO_PRIVATE_KEY"),
    fetch,
  });
  const catalog = await ensureTestCatalog(client, args, env, logger);
  if (args.configure_webhook)
    await configureWebhook(client, catalog.storeId, args.webhook_url, logger);

  const orderId = args.order_id || `lmm-pancake-test-${Date.now()}`;
  const buyerIdentity = args.buyer_id || `lmm-pancake-smoke-${Date.now()}`;
  const checkout = await client.checkout.authenticated.create({
    productId: catalog.productId,
    currency: "USD",
    priceSnapshot: {
      amount: args.amount || DEFAULT_AMOUNT,
      taxCategory: TaxCategory.SaaS,
    },
    buyerIdentity,
    buyerEmail: args.buyer_email || "checkout-test@example.invalid",
    orderMerchantExternalId: orderId,
    successUrl:
      args.return_url || env.WAFFO_PANCAKE_RETURN_URL?.trim() || undefined,
    darkMode: true,
    expiresInSeconds: 45 * 60,
  });
  reportWarnings(checkout, "checkout.authenticated.create", logger);
  // The authenticated SDK result appends #token=<JWT>. The session URL alone
  // may be logged; the buyer authentication fragment must stay out of output.
  const checkoutUrl = new URL(checkout.checkoutUrl);
  checkoutUrl.hash = "";

  logger.log(
    JSON.stringify(
      {
        environment: "test",
        store_id: catalog.storeId,
        product_id: catalog.productId,
        order_id: orderId,
        session_id: checkout.sessionId,
        expires_at: checkout.expiresAt,
        checkout_url: checkoutUrl.href,
        test_card: TEST_CARD,
        webhook_endpoint:
          args.webhook_url ||
          "https://api.lmm.best/api/waffo-pancake/webhook/test",
        next: "Complete the hosted checkout, then confirm order.completed reached the webhook and the local order is settled.",
      },
      null,
      2,
    ),
  );
}

// Dependency injection is only a unit-test seam; CLI defaults still use the
// official SDK and its transport. Importing this module never starts a smoke.
export async function runSmoke({
  argv = process.argv.slice(2),
  env = process.env,
  fetch,
  logger = console,
} = {}) {
  try {
    await main({ argv, env, fetch, logger });
    return 0;
  } catch (error) {
    logger.error(`Waffo Pancake smoke failed: ${summarizeError(error)}`);
    return 1;
  }
}

const invokedPath = process.argv[1]
  ? pathToFileURL(resolve(process.argv[1])).href
  : "";
if (import.meta.url === invokedPath) {
  process.exitCode = await runSmoke();
}
