import { test } from "node:test";
import assert from "node:assert/strict";
import { LmmHttp } from "../src/vendor/http.ts";
import { LmmOAuth } from "../src/vendor/oauth.ts";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createHash } from "node:crypto";

const issuer = "https://api.lmm.best";
const resource = `${issuer}/api/oauth2`;
const scope =
  "catalog:read balance:read usage:read models:invoke group:ZGVmYXVsdA";
const old = {
  type: "oauth" as const,
  access: "lmm_at_before",
  refresh: "lmm_rt_before",
  expires: Date.now(),
  lmm_issuer: issuer,
  lmm_resource: resource,
  lmm_session: "test",
  scope,
};

function authorizationMetadata(clients: unknown = ["lmm-zed"]) {
  return {
    issuer,
    authorization_endpoint: `${resource}/authorize`,
    token_endpoint: `${resource}/token`,
    revocation_endpoint: `${resource}/revoke`,
    code_challenge_methods_supported: ["S256"],
    response_types_supported: ["code"],
    authorization_response_iss_parameter_supported: true,
    lmm_client_ids_supported: clients,
  };
}

function jsonResponse(value: unknown) {
  return new Response(JSON.stringify(value), {
    headers: { "content-type": "application/json" },
  });
}

test("login checks Zed registration before opening a browser or exchanging tokens", async () => {
  for (const clients of [
    undefined,
    null,
    "lmm-zed",
    { "lmm-zed": true },
    [],
    ["pi", "lmm-vscode"],
    ["lmm-zed", 42],
  ]) {
    let notifications = 0;
    const paths: string[] = [];
    const metadata = authorizationMetadata(clients);
    if (clients === undefined) delete metadata.lmm_client_ids_supported;
    const http = new LmmHttp({
      fetch: async (input, init) => {
        assert.notEqual(init?.method, "POST");
        const path = new URL(String(input)).pathname;
        paths.push(path);
        assert.ok(path.startsWith("/.well-known/"));
        return jsonResponse(
          path.endsWith("oauth-authorization-server")
            ? metadata
            : { resource, authorization_servers: [issuer] },
        );
      },
    });
    await assert.rejects(
      new LmmOAuth(http, 1000).login({
        notify: () => notifications++,
      }),
      /not enabled Zed sign-in yet.*lmm-zed login/,
    );
    assert.equal(notifications, 0);
    assert.deepEqual(paths.sort(), [
      "/.well-known/oauth-authorization-server",
      "/.well-known/oauth-protected-resource/api/oauth2",
    ]);
  }
});

test("advertised Zed registration does not bypass OAuth metadata validation", async () => {
  for (const [patch, message] of [
    [{ issuer: "https://evil.example" }, /issuer or endpoint/],
    [{ token_endpoint: "https://evil.example/token" }, /issuer or endpoint/],
    [{ code_challenge_methods_supported: ["plain"] }, /S256 PKCE/],
    [{ response_types_supported: ["token"] }, /authorization-code/],
    [{ authorization_response_iss_parameter_supported: false }, /issuer-bound/],
  ] as const) {
    const http = new LmmHttp({
      fetch: async (input) =>
        jsonResponse(
          String(input).endsWith("oauth-authorization-server")
            ? { ...authorizationMetadata(), ...patch }
            : { resource, authorization_servers: [issuer] },
        ),
    });
    await assert.rejects(
      new LmmOAuth(http).discover(AbortSignal.timeout(1000)),
      message,
    );
  }
  const http = new LmmHttp({
    fetch: async (input) =>
      jsonResponse(
        String(input).endsWith("oauth-authorization-server")
          ? authorizationMetadata()
          : { resource: `${resource}/other`, authorization_servers: [issuer] },
      ),
  });
  await assert.rejects(
    new LmmOAuth(http).discover(AbortSignal.timeout(1000)),
    /protected-resource metadata/,
  );
});

test("Zed login preserves its client identity through real loopback callback and PKCE", async () => {
  const controller = new AbortController();
  let redirectUri = "";
  let challenge = "";
  let tokenCalls = 0;
  const http = new LmmHttp({
    fetch: async (input, init) => {
      const path = new URL(String(input)).pathname;
      if (path === "/.well-known/oauth-authorization-server")
        return jsonResponse(
          authorizationMetadata(["pi", "lmm-zed", "lmm-vscode"]),
        );
      if (path === "/.well-known/oauth-protected-resource/api/oauth2")
        return jsonResponse({ resource, authorization_servers: [issuer] });
      assert.equal(path, "/api/oauth2/token");
      assert.equal(init?.method, "POST");
      tokenCalls++;
      const fields = new URLSearchParams(String(init?.body));
      assert.equal(fields.get("client_id"), "lmm-zed");
      assert.equal(fields.get("grant_type"), "authorization_code");
      assert.equal(fields.get("resource"), resource);
      assert.equal(fields.get("code"), "authorization_code_fixture");
      assert.equal(fields.get("redirect_uri"), redirectUri);
      const verifier = fields.get("code_verifier");
      assert.ok(verifier);
      assert.match(verifier, /^[A-Za-z0-9_-]{43}$/);
      assert.equal(
        createHash("sha256").update(verifier).digest("base64url"),
        challenge,
      );
      return jsonResponse({
        token_type: "Bearer",
        access_token: "lmm_at_after",
        refresh_token: "lmm_rt_after",
        expires_in: 3600,
        scope,
      });
    },
  });
  let browserComplete!: () => void;
  let browserFailed!: (error: unknown) => void;
  const browser = new Promise<void>((resolve, reject) => {
    browserComplete = resolve;
    browserFailed = reject;
  });
  try {
    const login = new LmmOAuth(http, 5000).login({
      signal: controller.signal,
      notify: (event) => {
        void (async () => {
          assert.equal(event.type, "auth_url");
          assert.ok(event.url);
          const authorization = new URL(event.url);
          assert.equal(authorization.origin, issuer);
          assert.equal(authorization.pathname, "/api/oauth2/authorize");
          assert.equal(authorization.searchParams.get("client_id"), "lmm-zed");
          assert.equal(authorization.searchParams.get("resource"), resource);
          assert.equal(authorization.searchParams.get("response_type"), "code");
          assert.equal(
            authorization.searchParams.get("code_challenge_method"),
            "S256",
          );
          assert.match(event.instructions ?? "", /return to Zed/);
          assert.doesNotMatch(event.instructions ?? "", /Pi/);
          redirectUri = authorization.searchParams.get("redirect_uri") ?? "";
          challenge = authorization.searchParams.get("code_challenge") ?? "";
          assert.match(challenge, /^[A-Za-z0-9_-]{43}$/);
          const callback = new URL(redirectUri);
          assert.equal(callback.hostname, "127.0.0.1");
          assert.equal(callback.pathname, "/oauth/lmm/callback");
          callback.searchParams.set("iss", issuer);
          callback.searchParams.set("code", "authorization_code_fixture");
          callback.searchParams.set("state", "wrong_state");
          const invalid = await fetch(callback);
          assert.equal(invalid.status, 400);
          await invalid.body?.cancel();
          assert.equal(tokenCalls, 0);
          callback.searchParams.set(
            "state",
            authorization.searchParams.get("state") ?? "",
          );
          const accepted = await fetch(callback);
          assert.equal(accepted.status, 200);
          const message = await accepted.text();
          assert.match(message, /Return to Zed/);
          assert.doesNotMatch(message, /Pi/);
        })().then(browserComplete, browserFailed);
      },
    });
    const [credential] = await Promise.all([login, browser]);
    assert.equal(credential.access, "lmm_at_after");
    assert.equal(credential.refresh, "lmm_rt_after");
    assert.equal(tokenCalls, 1);
  } finally {
    controller.abort();
  }
});

test("discovery rejects issuer substitution", async () => {
  const http = new LmmHttp({
    fetch: async (input) =>
      new Response(
        JSON.stringify(
          String(input).includes("authorization-server")
            ? { issuer: "https://evil.example" }
            : { resource, authorization_servers: [issuer] },
        ),
        { headers: { "content-type": "application/json" } },
      ),
  });
  await assert.rejects(
    new LmmOAuth(http).discover(AbortSignal.timeout(1000)),
    /issuer or endpoint/,
  );
});

test("refresh uses native Zed client, rotates credentials and fences replay", async () => {
  const directory = await mkdtemp(join(tmpdir(), "lmm-zed-refresh-"));
  let calls = 0;
  const http = new LmmHttp({
    fetch: async (input, init) => {
      calls++;
      assert.equal(input, `${resource}/token`);
      const fields = new URLSearchParams(String(init?.body));
      assert.equal(fields.get("client_id"), "lmm-zed");
      assert.equal(fields.get("resource"), resource);
      assert.equal(fields.get("refresh_token"), "lmm_rt_before");
      return new Response(
        JSON.stringify({
          token_type: "Bearer",
          access_token: "lmm_at_after",
          refresh_token: "lmm_rt_after",
          expires_in: 3600,
          scope,
        }),
        { headers: { "content-type": "application/json" } },
      );
    },
  });
  try {
    const oauth = new LmmOAuth(http, 180000, directory, "lmm-zed", "Zed");
    const result = await oauth.refresh(old, AbortSignal.timeout(1000));
    assert.equal(result.access, "lmm_at_after");
    assert.equal(result.refresh, "lmm_rt_after");
    await assert.rejects(
      oauth.refresh(old, AbortSignal.timeout(1000)),
      /already attempted/,
    );
    assert.equal(calls, 1);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("refresh refuses widened scopes and nonrotating token", async () => {
  for (const body of [
    { scope: scope + " group:dmlw", refresh_token: "lmm_rt_after" },
    { scope, refresh_token: old.refresh },
  ]) {
    const http = new LmmHttp({
      fetch: async () =>
        new Response(
          JSON.stringify({
            token_type: "Bearer",
            access_token: "lmm_at_after",
            expires_in: 3600,
            ...body,
          }),
          { headers: { "content-type": "application/json" } },
        ),
    });
    await assert.rejects(
      new LmmOAuth(http, 180000, undefined, "lmm-zed", "Zed").exchangeRefresh(
        old,
        AbortSignal.timeout(1000),
      ),
      /widen|rotate/,
    );
  }
});
