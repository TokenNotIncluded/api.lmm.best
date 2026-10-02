import { test } from "node:test";
import assert from "node:assert/strict";
import { LmmHttp } from "../src/vendor/http.ts";
import { LmmOAuth } from "../src/vendor/oauth.ts";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

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
