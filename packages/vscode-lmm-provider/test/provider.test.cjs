const { test } = require("node:test");
const assert = require("node:assert/strict");
const Module = require("node:module");
const settings = {};
class TextPart { constructor(value) { this.value = value; } }
class ToolCallPart {}
class ToolResultPart {}
const vscode = {
  EventEmitter: class { constructor() { this.event = () => ({ dispose() {} }); } fire() {} },
  workspace: { getConfiguration: () => ({
    get: (key, fallback) => Object.hasOwn(settings, key) ? settings[key] : fallback,
    inspect: key => ({ globalValue: settings[key] }),
  }) },
  LanguageModelTextPart: TextPart, LanguageModelToolCallPart: ToolCallPart,
  LanguageModelToolResultPart: ToolResultPart,
  LanguageModelChatMessageRole: { User: 1, Assistant: 2 },
  LanguageModelChatToolMode: { Required: 2 },
};
const load = Module._load;
Module._load = function(id, ...args) { return id === "vscode" ? vscode : load.call(this, id, ...args); };
const { Provider } = require("../dist/extension");
Module._load = load;
const { RESOURCE } = require("../dist/protocol");
const group_id = Buffer.from("default").toString("base64url");
const makeEntry = upstream_model => ({ id: `lmm:${group_id}:${Buffer.from(upstream_model).toString("base64url")}`,
  group_id, group: "default", upstream_model, name: upstream_model, apis: ["openai-completions"] });
const token = { isCancellationRequested: false, onCancellationRequested: () => ({ dispose() {} }) };
const message = { role: 1, content: [new TextPart("Hello")], name: undefined };
test("generation revalidates tool permission and budgets instead of trusting stale picker metadata", async () => {
  const original = global.fetch;
  let entry = makeEntry("gpt-4o"), requests = [];
  const provider = new Provider({ current: async () => ({}), token: async () => ({ access: "private", scope: `group:${group_id}` }) },
    async () => { throw new Error("must not open login"); }, { refresh: async () => {} });
  global.fetch = async (url, options) => {
    if (url.endsWith("/catalog")) return new Response(JSON.stringify({ schema_version: 1, resource: RESOURCE, models: [entry] }), { headers: { "content-type": "application/json" } });
    assert.ok(url.endsWith("/v1/chat/completions"));
    requests.push(JSON.parse(options.body));
    return new Response('data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n', { headers: { "content-type": "text/event-stream" } });
  };
  try {
    settings.toolModels = [entry.id];
    const [stale] = await provider.provideLanguageModelChatInformation({ silent: true }, token);
    assert.equal(stale.capabilities.toolCalling, true);
    settings.toolModels = [];
    await assert.rejects(provider.provideLanguageModelChatResponse(stale, [message], { tools: [{ name: "test", description: "test", inputSchema: {} }] }, { report() {} }, token), /Enable this verified model/);
    assert.equal(requests.length, 0);
    const [refreshed] = await provider.provideLanguageModelChatInformation({ silent: true }, token);
    assert.equal(refreshed.capabilities.toolCalling, false);
    settings.maxOutputTokens = 1;
    await provider.provideLanguageModelChatResponse(stale, [message], {}, { report() {} }, token);
    assert.equal(requests[0].max_tokens, 1);
    settings.maxInputTokens = 1;
    await assert.rejects(provider.provideLanguageModelChatResponse(stale, [message], {}, { report() {} }, token), /current LMM input budget/);
    assert.equal(requests.length, 1);
    delete settings.maxInputTokens;
    entry = makeEntry("text-embedding-3-small");
    settings.modelLimits = { [entry.id]: { contextWindow: 8192, maxOutputTokens: 1024 } };
    settings.toolModels = [entry.id];
    assert.deepEqual(await provider.provideLanguageModelChatInformation({ silent: true }, token), []);
  } finally {
    global.fetch = original;
    for (const key of Object.keys(settings)) delete settings[key];
  }
});
