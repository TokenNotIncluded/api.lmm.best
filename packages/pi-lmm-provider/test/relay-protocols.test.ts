import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { test } from "node:test";
import type { AssistantMessageEvent, ProviderAuthInteraction } from "@earendil-works/pi-ai";
import { LmmIntegration } from "../src/provider.ts";
import { LmmHttp } from "../src/http.ts";
import { LmmOAuth } from "../src/oauth.ts";

const issuer = "https://api.lmm.best";
const group = "ZGVmYXVsdA";
const scope = `catalog:read balance:read models:invoke group:${group}`;
const json = (body: unknown) =>
  new Response(JSON.stringify(body), { headers: { "content-type": "application/json" } });
const sse = (events: { type: string; [key: string]: unknown }[]) =>
  new Response(events.map((e) => `event: ${e.type}\ndata: ${JSON.stringify(e)}\n\n`).join(""), {
    headers: { "content-type": "text/event-stream" },
  });
const usage = {
  input_tokens: 3,
  output_tokens: 1,
  total_tokens: 4,
  input_tokens_details: { cached_tokens: 0 },
  output_tokens_details: { reasoning_tokens: 0 },
};
const message = {
  id: "msg_1",
  type: "message",
  role: "assistant",
  status: "completed",
  content: [{ type: "output_text", text: "hello", annotations: [] }],
};

for (const protocol of ["openai-responses", "anthropic-messages"] as const) {
  test(
    `native ${protocol} consumes SSE with OAuth headers and refreshes balance`,
    { timeout: 15000 },
    async () => {
      const modelName = protocol === "openai-responses" ? "gpt-4o" : "claude-haiku-4-5-20251001";
      const id = `lmm:${group}:${Buffer.from(modelName).toString("base64url")}`;
      let balanceReads = 0;
      let finish!: () => void;
      const balanceRefreshed = new Promise<void>((resolve) => {
        finish = resolve;
      });
      let sent = false;
      const requestFetch: typeof fetch = async (input, init) => {
        const url = input instanceof Request ? input.url : String(input);
        if (url.endsWith("/catalog"))
          return json({
            schema_version: 1,
            resource: `${issuer}/api/oauth2`,
            updated_at: 1789240000,
            groups: [{ id: group, name: "default", scope: `group:${group}`, multiplier: 1 }],
            models: [
              {
                id,
                group_id: group,
                group: "default",
                upstream_model: modelName,
                name: modelName,
                apis: [protocol],
                pricing: {
                  currency: "USD",
                  unit: "million_tokens",
                  price_basis: "configured_base_rates",
                  group_multiplier: 1,
                  trust_multiplier: 1,
                  input: 1,
                  output: 2,
                  cache_read: 0.25,
                  cache_write: 1.25,
                  request: null,
                  final_cost_depends_on_usage: true,
                  updated_at: 1789240000,
                },
                native_cost: { input: 1, output: 2, cacheRead: 0.25, cacheWrite: 1.25 },
              },
            ],
          });
        if (url.endsWith("/balance")) {
          if (++balanceReads === 2) finish();
          return json({
            schema_version: 1,
            currency: "platform_credit",
            balance: 1,
            quota: 500000,
            quota_per_unit: 500000,
            updated_at: 1789240000,
            authorization_limit: null,
          });
        }
        assert.equal(
          url,
          issuer + (protocol === "openai-responses" ? "/v1/responses" : "/v1/messages"),
        );
        const headers = new Headers(
          init?.headers ?? (input instanceof Request ? input.headers : undefined),
        );
        assert.equal(headers.get("authorization"), "Bearer lmm_at_protocol_fixture");
        assert.equal(headers.get("x-lmm-group"), group);
        assert.equal(headers.get("x-api-key"), null);
        const body = JSON.parse(
          String(init?.body ?? (input instanceof Request ? await input.clone().text() : "")),
        );
        assert.equal(body.model, modelName);
        assert.equal(body.stream, true);
        sent = true;
        if (protocol === "openai-responses")
          return sse([
            {
              type: "response.created",
              response: { id: "resp_1", status: "in_progress", output: [] },
            },
            {
              type: "response.output_item.added",
              output_index: 0,
              item: { ...message, status: "in_progress", content: [] },
            },
            {
              type: "response.content_part.added",
              output_index: 0,
              content_index: 0,
              item_id: "msg_1",
              part: { type: "output_text", text: "", annotations: [] },
            },
            {
              type: "response.output_text.delta",
              output_index: 0,
              content_index: 0,
              item_id: "msg_1",
              delta: "hello",
            },
            { type: "response.output_item.done", output_index: 0, item: message },
            {
              type: "response.completed",
              response: {
                id: "resp_1",
                status: "completed",
                model: modelName,
                output: [message],
                usage,
              },
            },
          ]);
        return sse([
          {
            type: "message_start",
            message: {
              id: "msg_1",
              type: "message",
              role: "assistant",
              model: modelName,
              content: [],
              stop_reason: null,
              stop_sequence: null,
              usage: { input_tokens: 3, output_tokens: 0 },
            },
          },
          { type: "content_block_start", index: 0, content_block: { type: "text", text: "" } },
          { type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "hello" } },
          { type: "content_block_stop", index: 0 },
          {
            type: "message_delta",
            delta: { stop_reason: "end_turn", stop_sequence: null },
            usage: { output_tokens: 1 },
          },
          { type: "message_stop" },
        ]);
      };
      const integration = new LmmIntegration({ fetch: requestFetch });
      try {
        const auth = await integration.provider.auth.oauth!.toAuth({
          type: "oauth",
          access: "lmm_at_protocol_fixture",
          refresh: "fixture_refresh",
          expires: Date.now() + 60000,
          lmm_issuer: issuer,
          lmm_resource: `${issuer}/api/oauth2`,
          lmm_session: "protocol-session",
          scope,
        });
        const selected = integration.provider.getModels().find((m) => m.id === `default / ${modelName}`);
        assert.ok(selected, "known vendor model must be admitted without a capability override");
        const events: AssistantMessageEvent[] = [];
        for await (const e of integration.provider.streamSimple!(
          selected,
          { messages: [{ role: "user", content: "hello", timestamp: Date.now() }] },
          { headers: auth.headers, maxTokens: 32 },
        ))
          events.push(e);
        const done = events.find((e) => e.type === "done");
        assert.ok(done?.type === "done", JSON.stringify(events));
        assert.equal(done.message.stopReason, "stop");
        assert.equal(done.message.model, `default / ${modelName}`);
        assert.equal(integration.modelForLegacyId(id)?.id, selected.id);
        assert.deepEqual(
          done.message.content.filter((c) => c.type === "text").map((c) => c.text),
          ["hello"],
        );
        assert.ok(events.some((e) => e.type === "text_delta"));
        assert.equal(sent, true);
        await balanceRefreshed;
      } finally {
        integration.dispose();
      }
    },
  );
}

test(
  "OAuth login completes a real loopback callback and verifies PKCE exchange",
  { timeout: 15000 },
  async () => {
    let authorization: URL | undefined;
    let receipt: Promise<{ status: number; text: string }> | undefined;
    let callbackUrl = "";
    const requestFetch: typeof fetch = async (input, init) => {
      const url = String(input);
      if (url.endsWith("/oauth-authorization-server"))
        return json({
          issuer,
          authorization_endpoint: `${issuer}/api/oauth2/authorize`,
          token_endpoint: `${issuer}/api/oauth2/token`,
          revocation_endpoint: `${issuer}/api/oauth2/revoke`,
          code_challenge_methods_supported: ["S256"],
          response_types_supported: ["code"],
          authorization_response_iss_parameter_supported: true,
        });
      if (url.endsWith("/oauth-protected-resource/api/oauth2"))
        return json({ resource: `${issuer}/api/oauth2`, authorization_servers: [issuer] });
      assert.equal(url, `${issuer}/api/oauth2/token`);
      const body = new URLSearchParams(String(init?.body));
      assert.equal(body.get("client_id"), "lmm-pi");
      assert.equal(body.get("resource"), `${issuer}/api/oauth2`);
      assert.equal(body.get("code"), "fixture_code");
      assert.equal(body.get("redirect_uri"), authorization!.searchParams.get("redirect_uri"));
      assert.equal(
        createHash("sha256").update(body.get("code_verifier")!).digest("base64url"),
        authorization!.searchParams.get("code_challenge"),
      );
      return json({
        token_type: "Bearer",
        access_token: "lmm_at_login_fixture",
        refresh_token: "lmm_rt_fixture",
        expires_in: 3600,
        scope,
      });
    };
    const oauth = new LmmOAuth(new LmmHttp({ fetch: requestFetch }));
    const interaction: ProviderAuthInteraction = {
      signal: new AbortController().signal,
      notify(event) {
        if (event.type !== "auth_url") return;
        authorization = new URL(event.url);
        const callback = new URL(authorization.searchParams.get("redirect_uri")!);
        callback.search = new URLSearchParams({
          code: "fixture_code",
          state: authorization.searchParams.get("state")!,
          iss: issuer,
        }).toString();
        callbackUrl = callback.href;
        receipt = fetch(callbackUrl)
          .then(async (r) => ({ status: r.status, text: await r.text() }))
          .catch((error) => ({ status: 0, text: String(error) }));
      },
      prompt: async () => {
        throw new Error("unexpected prompt");
      },
    };
    const credentials = await oauth.login(interaction);
    assert.equal(credentials.access, "lmm_at_login_fixture");
    assert.ok(receipt);
    const response = await receipt;
    assert.equal(response.status, 200, response.text);
    assert.match(response.text, /Authorization received/);
    await assert.rejects(fetch(callbackUrl));
  },
);
