import { createServer } from "node:http";
import { timingSafeEqual } from "node:crypto";
import { once } from "node:events";
import { requireValue } from "./vendor/protocol.ts";
import type { LmmHttp } from "./vendor/http.ts";
import type { LmmCredential } from "./vendor/protocol.ts";
import type { Model } from "./catalog.ts";

function equal(actual: string | undefined, expected: string) {
  const a = Buffer.from(actual ?? "");
  const b = Buffer.from(expected);
  return a.length === b.length && timingSafeEqual(a, b);
}
export async function startBridge(options: {
  http: LmmHttp;
  models: Model[];
  key: string;
  port: number;
  credential(): Promise<LmmCredential>;
}) {
  requireValue(
    options.key.length >= 32,
    "A strong local bridge key is required.",
  );
  const models = new Map(options.models.map((model) => [model.id, model]));
  let authority = "";
  const server = createServer(
    { maxHeaderSize: 16384 },
    async (request, response) => {
      const fail = (status: number, message: string) => {
        if (!response.headersSent)
          response.writeHead(status, {
            "content-type": "application/json",
            "cache-control": "no-store",
          });
        response.end(JSON.stringify({ error: { message } }));
      };
      // Disallow browser traffic and DNS rebinding; never enable CORS.
      if (
        request.headers.host !== authority ||
        request.socket.remoteAddress !== "127.0.0.1" ||
        request.headers.origin ||
        !equal(request.headers.authorization, `Bearer ${options.key}`)
      ) {
        fail(401, "Local bridge authorization required.");
        return;
      }
      if (request.method === "GET" && request.url === "/v1/models") {
        response.writeHead(200, { "content-type": "application/json" });
        response.end(
          JSON.stringify({
            object: "list",
            data: options.models.map((model) => ({
              id: model.id,
              object: "model",
              owned_by: "lmm",
            })),
          }),
        );
        return;
      }
      const api =
        request.url === "/v1/chat/completions"
          ? "openai-completions"
          : request.url === "/v1/responses"
            ? "openai-responses"
            : undefined;
      if (request.method !== "POST" || !api) {
        fail(404, "Unsupported bridge endpoint.");
        return;
      }
      const controller = new AbortController();
      response.once("close", () => controller.abort());
      const signal = AbortSignal.any([
        controller.signal,
        AbortSignal.timeout(300000),
      ]);
      let upstream: Response | undefined;
      try {
        requireValue(
          request.headers["content-type"]?.split(";")[0].trim() ===
            "application/json",
        );
        const chunks: Buffer[] = [];
        let bytes = 0;
        for await (const chunk of request) {
          bytes += chunk.length;
          requireValue(
            bytes <= 8_000_000,
            "Request exceeded the bridge size limit.",
          );
          chunks.push(chunk);
        }
        const body = JSON.parse(Buffer.concat(chunks).toString("utf8"));
        requireValue(
          body &&
            typeof body === "object" &&
            !Array.isArray(body) &&
            typeof body.model === "string",
        );
        const model = models.get(body.model);
        requireValue(
          model && model.apis.includes(api),
          "Model is unavailable for this protocol or outside the granted catalog.",
        );
        const auth = await options.credential();
        requireValue(
          auth.scope.split(" ").includes(`group:${model.group}`) &&
            auth.scope.split(" ").includes("models:invoke"),
        );
        upstream = await options.http.fetch(
          `${options.http.issuer}${request.url}`,
          {
            method: "POST",
            signal,
            redirect: "error",
            credentials: "omit",
            headers: {
              "content-type": "application/json",
              authorization: `Bearer ${auth.access}`,
              "x-lmm-group": model.group,
            },
            body: JSON.stringify({ ...body, model: model.upstream }),
          },
        );
        // Never retry inference: ambiguous transport failures may already be billed.
        if (!upstream.ok) {
          await upstream.body?.cancel();
          fail(
            upstream.status,
            `LMM inference failed (HTTP ${upstream.status}).`,
          );
          return;
        }
        requireValue(upstream.body);
        response.writeHead(upstream.status, {
          "content-type":
            upstream.headers.get("content-type") ?? "application/json",
          "cache-control": "no-store",
          "x-accel-buffering": "no",
        });
        const reader = upstream.body.getReader();
        try {
          while (true) {
            const { done, value } = await reader.read();
            if (done) break;
            if (!response.write(value))
              await once(response, "drain", { signal });
          }
        } finally {
          await reader.cancel().catch(() => {});
          reader.releaseLock();
        }
        response.end();
      } catch {
        if (response.headersSent) response.destroy();
        else
          fail(
            400,
            "LMM request failed. Check model configuration or sign in again.",
          );
      } finally {
        await upstream?.body?.cancel().catch(() => {});
      }
    },
  );
  server.requestTimeout = 300000;
  server.headersTimeout = 10000;
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(options.port, "127.0.0.1", () => {
      server.removeListener("error", reject);
      resolve();
    });
  });
  const address = server.address();
  requireValue(address && typeof address !== "string");
  authority = `127.0.0.1:${address.port}`;
  return {
    port: address.port,
    close: () => {
      server.close();
      server.closeAllConnections();
    },
  };
}
