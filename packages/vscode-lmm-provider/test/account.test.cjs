const { test } = require("node:test");
const assert = require("node:assert/strict");
const Module = require("node:module");
const item = { show() {}, dispose() { this.disposed = true; } };
const load = Module._load;
Module._load = function(id, ...args) {
  return id === "vscode" ? { window: { createStatusBarItem: () => item }, StatusBarAlignment: { Right: 2 } } : load.call(this, id, ...args);
};
const { AccountStatus } = require("../dist/account");
Module._load = load;
const wallet = { schema_version: 2, currency: "USD", quota: 500000, quota_per_unit: 500000, balance: 1, updated_at: 1,
  authorization_limit: null, credit_unit: 1, credits_per_usd: "500000", public_credit_balance: "500000" };
const response = () => new Response(JSON.stringify(wallet), { headers: { "content-type": "application/json" } });
test("account refresh is scope-gated, failures do not show old amounts, sign-out defeats late responses", async () => {
  const original = global.fetch;
  let scope = "balance:read", loggedIn = true, calls = 0;
  const account = new AccountStatus({ current: async () => loggedIn ? {} : undefined, token: async () => ({ access: "private", scope }) });
  try {
    global.fetch = async (url, options) => { calls++; assert.match(url, /\/api\/oauth2\/balance$/); assert.equal(options.headers.authorization, "Bearer private"); return response(); };
    await account.refresh();
    assert.match(item.text, /1 USD/);
    assert.equal(item.command, "lmm.refreshAccount");
    scope = "catalog:read";
    await account.refresh();
    assert.equal(calls, 1);
    assert.match(item.text, /未授权/);
    scope = "balance:read";
    global.fetch = async () => { throw new Error("private upstream response"); };
    await account.refresh();
    assert.match(item.text, /余额不可用/);
    assert.doesNotMatch(item.tooltip, /private/);
    let release, entered;
    const started = new Promise(r => { entered = r; });
    global.fetch = () => { entered(); return new Promise(r => { release = r; }); };
    const pending = account.refresh();
    await started;
    loggedIn = false;
    account.clear();
    release(response());
    await pending;
    assert.match(item.text, /未登录/);
    await account.refresh();
    assert.match(item.text, /未登录/);
  } finally { account.dispose(); global.fetch = original; }
});
