import { test } from "node:test";
import assert from "node:assert/strict";
import { startBridge } from "../src/bridge.ts";
import { LmmHttp } from "../src/vendor/http.ts";
import {
  parseCatalog,
  zedSettings,
  defaultConfiguration,
} from "../src/catalog.ts";
import { Store } from "../src/store.ts";
import { RefreshJournal } from "../src/vendor/refresh-journal.ts";
import { mkdtemp, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const group = Buffer.from("default").toString("base64url");
const id = `lmm:${group}:${Buffer.from("real-model").toString("base64url")}`;
const models = [
  {
    id,
    name: "Real model",
    group,
    upstream: "real-model",
    apis: ["openai-completions", "openai-responses"],
  },
];
const auth = {
  type: "oauth" as const,
  access: "lmm_at_test",
  refresh: "lmm_rt_test",
  expires: Date.now() + 3600000,
  lmm_issuer: "https://api.lmm.best",
  lmm_resource: "https://api.lmm.best/api/oauth2",
  lmm_session: "test",
  scope: `catalog:read balance:read usage:read models:invoke group:${group}`,
};

test("bridge restricts auth/browser traffic/model groups, maps upstream model and streams without retry", async () => {
  let calls = 0;
  let wire: Record<string, unknown> = {};
  let headers = new Headers();
  const http = new LmmHttp({
    fetch: async (input, init) => {
      calls++;
      assert.equal(input, "https://api.lmm.best/v1/chat/completions");
      wire = JSON.parse(String(init?.body));
      headers = new Headers(init?.headers);
      return new Response('data: {"choices":[]}\n\ndata: [DONE]\n\n', {
        headers: { "content-type": "text/event-stream" },
      });
    },
  });
  const key = "a".repeat(43);
  const bridge = await startBridge({
    http,
    models,
    key,
    port: 0,
    credential: async () => auth,
  });
  const url = `http://127.0.0.1:${bridge.port}/v1/chat/completions`;
  const request = (body: unknown, extra: Record<string, string> = {}) =>
    fetch(url, {
      method: "POST",
      headers: {
        authorization: `Bearer ${key}`,
        "content-type": "application/json",
        ...extra,
      },
      body: JSON.stringify(body),
    });
  try {
    assert.equal(
      (await request({ model: id }, { authorization: "Bearer wrong" })).status,
      401,
    );
    assert.equal(
      (await request({ model: id }, { origin: "https://evil.example" })).status,
      401,
    );
    assert.equal((await request({ model: "other" })).status, 400);
    assert.equal(calls, 0);
    const result = await request({
      model: id,
      messages: [{ role: "user", content: "test" }],
      stream: true,
    });
    assert.equal(result.status, 200);
    assert.equal(
      await result.text(),
      'data: {"choices":[]}\n\ndata: [DONE]\n\n',
    );
    assert.equal(calls, 1);
    assert.equal(wire.model, "real-model");
    assert.equal(headers.get("authorization"), "Bearer lmm_at_test");
    assert.equal(headers.get("x-lmm-group"), group);
    assert.equal(headers.get("cookie"), null);
  } finally {
    bridge.close();
  }
});

test("ambiguous inference failure never retries and hides upstream errors", async () => {
  let calls = 0;
  const http = new LmmHttp({
    fetch: async () => {
      calls++;
      return new Response("lmm_at_secret", { status: 500 });
    },
  });
  const key = "b".repeat(43);
  const bridge = await startBridge({
    http,
    models,
    key,
    port: 0,
    credential: async () => auth,
  });
  try {
    const response = await fetch(
      `http://127.0.0.1:${bridge.port}/v1/responses`,
      {
        method: "POST",
        headers: {
          authorization: `Bearer ${key}`,
          "content-type": "application/json",
        },
        body: JSON.stringify({ model: id, input: "test" }),
      },
    );
    assert.equal(response.status, 500);
    assert.ok(!(await response.text()).includes("secret"));
    assert.equal(calls, 1);
  } finally {
    bridge.close();
  }
});

test("settings admits only catalog models with explicit metadata; Responses-only protocol works", () => {
  assert.throws(() =>
    zedSettings(models, [{ id: "wrong", max_tokens: 100 }], 7391),
  );
  assert.throws(() => zedSettings(models, [{ id, max_tokens: 0 }], 7391));
  const settings = zedSettings(
    [{ ...models[0], apis: ["openai-responses"] }],
    [{ id, max_tokens: 10000, capabilities: { tools: true } }],
    7391,
  );
  assert.equal(
    settings.language_models.openai_compatible.lmm.available_models[0]
      .capabilities.chat_completions,
    false,
  );
  assert.equal(
    settings.language_models.openai_compatible.lmm.available_models[0]
      .capabilities.images,
    false,
  );
  assert.throws(() =>
    parseCatalog(
      {
        schema_version: 1,
        resource: auth.lmm_resource,
        groups: [{ id: group, name: "default", scope: `group:${group}` }],
        models: [],
      },
      auth.lmm_resource,
      "catalog:read",
    ),
  );
});

test("credential persistence is private and refresh tokens are durably fenced", async () => {
  const root = await mkdtemp(join(tmpdir(), "lmm-zed-test-"));
  const directory = join(root, "private");
  const store = new Store(directory, auth.lmm_issuer);
  try {
    await store.write(auth);
    assert.deepEqual(await store.read(), auth);
    assert.equal(
      (await stat(join(directory, "oauth.json"))).mode & 0o777,
      0o600,
    );
    const journal = new RefreshJournal(join(directory, "journal"));
    await journal.begin(auth.lmm_issuer, auth.refresh);
    await assert.rejects(
      journal.begin(auth.lmm_issuer, auth.refresh),
      /already attempted/,
    );
    await store.remove();
    await assert.rejects(store.read());
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("automatic settings includes all OpenAI catalog models with documented fallback", () => {
  const config = defaultConfiguration([
    ...models,
    { ...models[0], id: "unsupported", apis: ["anthropic-messages"] },
  ]);
  assert.equal(config.length, 1);
  assert.equal(config[0].max_tokens, 32768);
  assert.equal(config[0].max_output_tokens, 4096);
  assert.equal(
    zedSettings(models, config, 7391).language_models.openai_compatible.lmm
      .available_models.length,
    1,
  );
});

test("configure preserves JSONC comments, other settings, existing model metadata and backs up original", async () => {
  const { configureSettings } = await import("../src/settings.ts");
  const { writeFile, readFile } = await import("node:fs/promises");
  const { parse } = await import("jsonc-parser");
  const root = await mkdtemp(join(tmpdir(), "lmm-zed-settings-"));
  const path = join(root, "settings.json");
  const source = `{\n // Keep my editor settings\n "font_size": 17,\n "language_models": {"openai_compatible":{"other":{"api_url":"https://example.com"},"lmm":{"available_models":[{"name":"${id}","max_tokens":9999,"capabilities":{"tools":true}}]}}},\n}\n`;
  try {
    await writeFile(path, source);
    const provider = zedSettings(models, defaultConfiguration(models), 7391)
      .language_models.openai_compatible.lmm;
    const backup = await configureSettings(path, provider, true);
    assert.ok(backup);
    assert.equal(await readFile(backup, "utf8"), source);
    const updated = await readFile(path, "utf8");
    assert.ok(updated.includes("// Keep my editor settings"));
    const parsed = parse(updated);
    assert.equal(parsed.font_size, 17);
    assert.equal(
      parsed.language_models.openai_compatible.other.api_url,
      "https://example.com",
    );
    assert.equal(
      parsed.language_models.openai_compatible.lmm.available_models[0]
        .max_tokens,
      9999,
    );
    assert.equal(
      parsed.language_models.openai_compatible.lmm.available_models[0]
        .capabilities.tools,
      true,
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
