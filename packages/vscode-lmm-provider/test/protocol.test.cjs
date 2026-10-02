const { test } = require("node:test");
const assert = require("node:assert/strict");
const { mkdtemp, rm } = require("node:fs/promises");
const { tmpdir } = require("node:os");
const { join } = require("node:path");
const { Auth, parseToken } = require("../dist/auth");
const { catalogModels, streamCompletion } = require("../dist/wire");
const { listenCallback } = require("../dist/callback");
const { RESOURCE, ISSUER } = require("../dist/protocol");
const scope =
  "catalog:read balance:read usage:read models:invoke group:ZGVmYXVsdA";
const token = (suffix = "one") => ({
  token_type: "Bearer",
  access_token: `lmm_at_${suffix}`,
  refresh_token: `lmm_rt_${suffix}`,
  expires_in: 3600,
  scope,
});
const secret = (credential) => {
  let raw = credential && JSON.stringify(credential);
  return {
    get: async () => raw,
    store: async (k, v) => {
      raw = v;
    },
    delete: async () => {
      raw = undefined;
    },
  };
};
const expired = () => ({ ...parseToken(token()), expires: 0 });
const response = (data) =>
  new Response(JSON.stringify(data), {
    headers: { "content-type": "application/json" },
  });
const sse = (raw, chunkSize = 3) =>
  new Response(
    new ReadableStream({
      start(c) {
        const bytes = new TextEncoder().encode(raw);
        for (let i = 0; i < bytes.length; i += chunkSize)
          c.enqueue(bytes.slice(i, i + chunkSize));
        c.close();
      },
    }),
    { headers: { "content-type": "text/event-stream" } },
  );
test("refresh requires rotation, scope cannot widen or omit model group", () => {
  assert.throws(() => parseToken(token(), parseToken(token())));
  assert.throws(() =>
    parseToken(
      { ...token("two"), scope: scope + " group:dW5hdXRob3JpemVk" },
      parseToken(token()),
    ),
  );
  assert.throws(() =>
    parseToken({ ...token(), scope: scope.replace(" group:ZGVmYXVsdA", "") }),
  );
});
test("single-flight refresh stores rotated credential once", async () => {
  const dir = await mkdtemp(join(tmpdir(), "lmm-vscode-test-"));
  const store = secret(expired());
  let calls = 0;
  try {
    const auth = new Auth(
      store,
      async () => {
        calls++;
        return response(token("two"));
      },
      dir,
    );
    const [a, b] = await Promise.all([auth.token(), auth.token()]);
    assert.equal(calls, 1);
    assert.equal(a.refresh, b.refresh);
    assert.equal((await auth.current()).refresh, "lmm_rt_two");
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
test("ambiguous refresh removes old secret and never retries", async () => {
  const dir = await mkdtemp(join(tmpdir(), "lmm-vscode-test-"));
  const store = secret(expired());
  let calls = 0;
  try {
    const auth = new Auth(
      store,
      async () => {
        calls++;
        throw new Error("secret upstream body");
      },
      dir,
    );
    await assert.rejects(auth.token());
    assert.equal(await store.get(), undefined);
    await assert.rejects(auth.token());
    assert.equal(calls, 1);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
test("independent windows cannot replay a rotating refresh token", async () => {
  const dir = await mkdtemp(join(tmpdir(), "lmm-vscode-test-"));
  let calls = 0;
  try {
    const fetcher = async () => {
      calls++;
      await new Promise((r) => setTimeout(r, 10));
      return response(token("two"));
    };
    const results = await Promise.allSettled([
      new Auth(secret(expired()), fetcher, dir).token(),
      new Auth(secret(expired()), fetcher, dir).token(),
    ]);
    assert.equal(calls, 1);
    assert.equal(results.filter((r) => r.status === "fulfilled").length, 1);
    assert.equal(results.filter((r) => r.status === "rejected").length, 1);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
test("journal survives restarted instance holding stale credentials", async () => {
  const dir = await mkdtemp(join(tmpdir(), "lmm-vscode-test-"));
  let calls = 0;
  const fetcher = async () => {
    calls++;
    return response(token("two"));
  };
  try {
    await new Auth(secret(expired()), fetcher, dir).token();
    await assert.rejects(
      new Auth(secret(expired()), fetcher, dir).token(),
      /already in progress/,
    );
    assert.equal(calls, 1);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
test("catalog binds model wire ID to granted group", () => {
  const m = {
    id: "lmm:ZGVmYXVsdA:bW9kZWw",
    group_id: "ZGVmYXVsdA",
    group: "default",
    upstream_model: "model",
    name: "Model",
    apis: ["openai-completions"],
  };
  assert.equal(
    catalogModels({ schema_version: 1, resource: RESOURCE, models: [m] }, scope)
      .length,
    1,
  );
  assert.throws(() =>
    catalogModels(
      { schema_version: 1, resource: RESOURCE, models: [m] },
      scope.replace("group:ZGVmYXVsdA", "group:b3RoZXI"),
    ),
  );
  assert.throws(() =>
    catalogModels(
      {
        schema_version: 1,
        resource: RESOURCE,
        models: [{ ...m, upstream_model: "other" }],
      },
      scope,
    ),
  );
});
test("SSE parses split unicode text, multiline tool deltas and CRLF", async () => {
  const parts = [];
  const frames = [
    { choices: [{ delta: { content: "你好" }, finish_reason: null }] },
    {
      choices: [
        {
          delta: {
            tool_calls: [
              {
                index: 0,
                id: "call_1",
                function: { name: "read", arguments: '{"path":' },
              },
            ],
          },
          finish_reason: null,
        },
      ],
    },
    {
      choices: [
        {
          delta: {
            tool_calls: [{ index: 0, function: { arguments: '"x"}' } }],
          },
          finish_reason: "tool_calls",
        },
      ],
    },
  ];
  await streamCompletion(
    sse(
      frames.map((f) => "data: " + JSON.stringify(f) + "\r\n\r\n").join("") +
        "data: [DONE]\r\n\r\n",
    ),
    (p) => parts.push(p),
    new AbortController().signal,
  );
  assert.deepEqual(parts, [
    { type: "text", text: "你好" },
    { type: "tool", id: "call_1", name: "read", input: { path: "x" } },
  ]);
});
test("truncated SSE, embedded upstream error and malformed tool input fail closed", async () => {
  await assert.rejects(
    streamCompletion(
      sse('data: {"choices":[{"delta":{"content":"partial"}}]}\n\n'),
      () => {},
      new AbortController().signal,
    ),
    /before completion/,
  );
  await assert.rejects(
    streamCompletion(
      sse('data: {"error":{"message":"SECRET"}}\n\n'),
      () => {},
      new AbortController().signal,
    ),
    /stream error/,
  );
  await assert.rejects(
    streamCompletion(
      sse(
        'data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"x","arguments":"[1]"}}]},"finish_reason":"tool_calls"}]}\n\ndata: [DONE]\n\n',
      ),
      () => {},
      new AbortController().signal,
    ),
  );
});
test("SSE bounds pending multiline events", async () => {
  await assert.rejects(
    streamCompletion(
      sse(("data: " + "x".repeat(1000) + "\n").repeat(2001), 65536),
      () => {},
      new AbortController().signal,
    ),
    /too large/,
  );
});
test("callback rejects wrong issuer/state and duplicate parameters before accepting valid response", async () => {
  const callback = await listenCallback(
    ISSUER,
    "state",
    AbortSignal.timeout(10000),
    "VS Code",
  );
  try {
    const url = new URL(callback.redirectUri);
    url.search = new URLSearchParams({
      state: "wrong",
      iss: ISSUER,
      code: "abc",
    });
    assert.equal((await fetch(url)).status, 400);
    url.search = new URLSearchParams({
      state: "state",
      iss: "https://evil.example",
      code: "abc",
    });
    assert.equal((await fetch(url)).status, 400);
    url.search = new URLSearchParams({
      state: "state",
      iss: ISSUER,
      code: "abc",
    });
    url.searchParams.append("state", "state");
    assert.equal((await fetch(url)).status, 400);
    url.searchParams.delete("state");
    url.searchParams.set("state", "state");
    assert.equal((await fetch(url)).status, 200);
    assert.equal(await callback.code, "abc");
  } finally {
    callback.close();
  }
});
test("OAuth denial callback settles without leaking description", async () => {
  const callback = await listenCallback(
    ISSUER,
    "state",
    AbortSignal.timeout(10000),
    "VS Code",
  );
  try {
    const url = new URL(callback.redirectUri);
    url.search = new URLSearchParams({
      state: "state",
      iss: ISSUER,
      error: "access_denied",
      error_description: "private",
    });
    const rejected = assert.rejects(callback.code, /denied/);
    const response = await fetch(url);
    assert.equal(response.status, 200);
    assert.equal((await response.text()).includes("private"), false);
    await rejected;
  } finally {
    callback.close();
  }
});
test("complete browser OAuth verifies discovery, client, PKCE and saves credential", async () => {
  const store = secret();
  let authorization;
  let exchanges = 0;
  const auth = new Auth(store, async (url, init) => {
    assert.equal(init.redirect, "error");
    if (url.endsWith("/.well-known/oauth-authorization-server"))
      return response({
        issuer: ISSUER,
        authorization_endpoint: RESOURCE + "/authorize",
        token_endpoint: RESOURCE + "/token",
        revocation_endpoint: RESOURCE + "/revoke",
        authorization_response_iss_parameter_supported: true,
        code_challenge_methods_supported: ["S256"],
        response_types_supported: ["code"],
      });
    if (url.endsWith("/.well-known/oauth-protected-resource/api/oauth2"))
      return response({ resource: RESOURCE, authorization_servers: [ISSUER] });
    assert.equal(url, RESOURCE + "/token");
    exchanges++;
    const form = new URLSearchParams(init.body);
    assert.equal(form.get("grant_type"), "authorization_code");
    assert.equal(form.get("client_id"), "lmm-vscode");
    assert.equal(form.get("code"), "local_code");
    assert.equal(form.get("resource"), RESOURCE);
    assert.equal(
      form.get("redirect_uri"),
      authorization.searchParams.get("redirect_uri"),
    );
    assert.equal(
      require("node:crypto")
        .createHash("sha256")
        .update(form.get("code_verifier"))
        .digest("base64url"),
      authorization.searchParams.get("code_challenge"),
    );
    return response(token());
  });
  await auth.login(async (value) => {
    authorization = new URL(value);
    assert.equal(authorization.origin, ISSUER);
    assert.equal(authorization.pathname, "/api/oauth2/authorize");
    assert.equal(
      authorization.searchParams.get("code_challenge_method"),
      "S256",
    );
    const callback = new URL(authorization.searchParams.get("redirect_uri"));
    callback.search = new URLSearchParams({
      state: authorization.searchParams.get("state"),
      iss: ISSUER,
      code: "local_code",
    });
    assert.equal((await fetch(callback)).status, 200);
  }, AbortSignal.timeout(10000));
  assert.equal(exchanges, 1);
  assert.equal((await auth.current()).access, "lmm_at_one");
});
test("untrusted discovery fails before opening browser or exchanging tokens", async () => {
  let opened = false;
  const auth = new Auth(secret(), async () =>
    response({ issuer: "https://attacker.example" }),
  );
  await assert.rejects(
    auth.login(async () => {
      opened = true;
    }, AbortSignal.timeout(10000)),
  );
  assert.equal(opened, false);
  assert.equal(await auth.current(), undefined);
});
