const { test } = require("node:test");
const assert = require("node:assert/strict");
const { modelMetadata, referenceMetadata, isTextChatModel, toolCallingEnabled } = require("../dist/metadata");
const { balanceLabel } = require("../dist/balance");
const { catalogModels } = require("../dist/wire");
const { RESOURCE } = require("../dist/protocol");
const entry = (group = "default", model = "gpt-4o", ratio = 0.5) => ({
  id: `lmm:${Buffer.from(group).toString("base64url")}:${Buffer.from(model).toString("base64url")}`,
  group_id: Buffer.from(group).toString("base64url"), group, upstream_model: model, name: model,
  groupMultiplier: ratio,
});
test("known context is model-specific, reserves output, and labels source", () => {
  const info = modelMetadata(entry());
  assert.equal(info.maxInputTokens, 128000 - 4096);
  assert.equal(info.maxOutputTokens, 4096);
  assert.match(info.tooltip, /models.dev: openai\/gpt-4o/);
  assert.match(info.tooltip, /not gateway guarantee/);
  assert.notEqual(modelMetadata(entry("default", "claude-sonnet-4-5")).maxInputTokens, info.maxInputTokens);
});
test("groups and zero multipliers remain distinct, unknown is not free", () => {
  assert.notEqual(modelMetadata(entry()).name, modelMetadata(entry("other")).name);
  assert.match(modelMetadata(entry("default", "gpt-4o", 0)).name, /0×/);
  assert.match(modelMetadata(entry("default", "gpt-4o", null)).name, /倍率未知/);
});
test("exact wire override beats upstream override; caps cannot exceed context", () => {
  const e = entry();
  const info = modelMetadata(e, { "gpt-4o": { contextWindow: 10000, maxOutputTokens: 2000 }, [e.id]: { contextWindow: 8000, maxOutputTokens: 1000 } }, 9000, 4096);
  assert.equal(info.maxInputTokens, 7000);
  assert.equal(info.maxOutputTokens, 1000);
  assert.equal(modelMetadata(e, {}, 5000).maxInputTokens, 5000);
  assert.throws(() => modelMetadata(e, { "gpt-4o": { contextWindow: 1000, maxOutputTokens: 1000 } }));
  assert.throws(() => modelMetadata(e, {}, undefined, NaN));
});
test("unknown aliases use explicitly labeled compatibility estimates", () => {
  const info = modelMetadata(entry("default", "gpt-4o-custom-route"));
  assert.equal(info.maxInputTokens, 32768);
  assert.match(info.tooltip, /unknown model limits; compatibility estimate/);
  assert.match(modelMetadata(entry("default", "__proto__")).tooltip, /unknown/);
});
test("independent upstream input limit and provider-qualified exact IDs are honored", () => {
  assert.equal(modelMetadata(entry("default", "gpt-5")).maxInputTokens, 272000);
  assert.equal(modelMetadata(entry("default", "openai/gpt-5")).maxInputTokens, 272000);
  assert.equal(modelMetadata(entry("default", "gpt-6.1-sol")).maxInputTokens, 922000);
  assert.equal(modelMetadata(entry("default", "gpt-5"), {}, 20000).maxInputTokens, 20000);
  assert.match(modelMetadata(entry("default", "gpt-5")).tooltip, /reasoning yes; tools yes/);
  assert.match(modelMetadata(entry("default", "unknown")).tooltip, /capabilities unknown/);
});
test("conflicting provider metadata is unknown unless provider is explicit", () => {
  const first = referenceMetadata("gpt-4o");
  const models = { duplicate: [{ ...first, source: "one/duplicate" }, { ...first, source: "two/duplicate", context: 4096 }] };
  assert.equal(referenceMetadata("duplicate", models), undefined);
  assert.equal(referenceMetadata("one/duplicate", models).context, 128000);
  assert.equal(referenceMetadata("two/duplicate", models).context, 4096);
});
test("known non-chat entries stay excluded despite configuration overrides", () => {
  for (const id of ["text-embedding-3-large", "text-embedding-3-small", "gemini-embedding-001", "gemini-embedding-2", "gpt-image-2", "dall-e-3", "whisper-1", "tts-1", "rerank-v3.5", "gemini-3-pro-image", "lyria-3-clip-preview", "mistral-embed"]) {
    assert.equal(isTextChatModel(id), false, id);
    assert.throws(() => modelMetadata(entry("default", id), { [id]: { contextWindow: 8192, maxOutputTokens: 1024 } }), id);
    assert.equal(toolCallingEnabled(entry("default", id), [id]), false, id);
  }
  assert.equal(isTextChatModel("my-private-chat-alias"), true);
});
test("wire-specific tool verification does not enable another group's route", () => {
  const a = entry("first"), b = entry("second");
  assert.equal(toolCallingEnabled(a, [a.id]), true);
  assert.equal(toolCallingEnabled(b, [a.id]), false);
  assert.equal(toolCallingEnabled(b, [a.upstream_model]), true);
  assert.equal(toolCallingEnabled(a, []), false);
});
test("catalog reads per-model effective ratio or legacy group ratio, binds granted groups", () => {
  const e = entry();
  const group = { id: e.group_id, name: e.group, scope: `group:${e.group_id}`, multiplier: 2 };
  const m = { ...e, apis: ["openai-completions"], pricing: { group_multiplier: 0.25 } };
  const body = { schema_version: 1, resource: RESOURCE, groups: [group], models: [m] };
  assert.equal(catalogModels(body, group.scope)[0].groupMultiplier, 0.25);
  delete m.pricing;
  assert.equal(catalogModels(body, group.scope)[0].groupMultiplier, 2);
  m.pricing = { group_multiplier: null };
  assert.equal(catalogModels(body, group.scope)[0].groupMultiplier, null);
  m.pricing.group_multiplier = -1;
  assert.throws(() => catalogModels(body, group.scope));
  delete m.pricing;
  assert.throws(() => catalogModels({ ...body, groups: [group, group] }, group.scope));
  assert.throws(() => catalogModels(body, "group:other"));
});
test("catalog validates all rows before unsupported protocols are filtered", () => {
  const e = entry(), group = { id: e.group_id, name: e.group, scope: `group:${e.group_id}`, multiplier: 2 };
  const m = { ...e, apis: ["openai-completions"] };
  const body = { schema_version: 1, resource: RESOURCE, groups: [group], models: [m] };
  for (const invalid of [null, {}, { ...m, apis: [] }, { ...m, apis: [1] }, { ...m, apis: ["anthropic-messages"] }])
    assert.throws(() => catalogModels({ ...body, models: [m, invalid] }, group.scope));
  assert.throws(() => catalogModels({ ...body, groups: [{ ...group, name: "" }] }, group.scope));
});
const wallet = () => ({ schema_version: 2, currency: "USD", quota: 500000, quota_per_unit: 500000, balance: 1, updated_at: 1,
  authorization_limit: null, credit_unit: 1, credits_per_usd: "500000", public_credit_balance: "500000" });
test("wallet validates USD basis and never equates credits with dollars", () => {
  assert.equal(balanceLabel(wallet()), "1 USD · 500,000 平台额度");
  assert.equal(balanceLabel({ schema_version: 1, currency: "USD", quota: 500000, quota_per_unit: 500000, balance: 1, updated_at: 1, authorization_limit: null }), "1 平台额度（旧版单位）");
  for (const patch of [{ balance: 2 }, { public_credit_balance: "1" }, { credits_per_usd: "0" }, { authorization_limit: 1 }, { quota_unit: "USD" }, { updated_at: NaN }, { quota_per_unit: Number.MIN_VALUE }, { currency: "CNY" }])
    assert.throws(() => balanceLabel({ ...wallet(), ...patch }));
  assert.equal(balanceLabel({ ...wallet(), quota: -500000, public_credit_balance: "-500000", balance: -1 }), "-1 USD · -500,000 平台额度");
});
