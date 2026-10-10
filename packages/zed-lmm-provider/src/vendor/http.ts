import { LmmError, boundedSignal, object, requireValue } from "./protocol.ts";

export interface HttpOptions {
  issuer?: string;
  fetch?: typeof globalThis.fetch;
  /** Local mock servers only; never set by the extension or any environment flag. */
  allowLoopbackHttpForTests?: boolean;
  timeoutMs?: number;
}

export class LmmHttp {
  readonly issuer: string;
  readonly resource: string;
  readonly fetch: typeof globalThis.fetch;
  readonly timeoutMs: number;

  constructor(options: HttpOptions = {}) {
    const raw = options.issuer ?? "https://api.lmm.best";
    const url = new URL(raw);
    requireValue(
      !url.username &&
        !url.password &&
        !url.search &&
        !url.hash &&
        url.pathname === "/",
      "LMM issuer must be a trusted HTTPS origin without a path or credentials.",
    );
    requireValue(
      url.protocol === "https:" ||
        (options.allowLoopbackHttpForTests === true &&
          url.protocol === "http:" &&
          url.hostname === "127.0.0.1"),
      "LMM requires HTTPS (except explicit local test fixtures).",
    );
    this.issuer = url.origin;
    this.resource = `${this.issuer}/api/oauth2`;
    this.fetch = options.fetch ?? globalThis.fetch;
    this.timeoutMs = options.timeoutMs ?? 15_000;
  }

  async request(
    path: string,
    init: RequestInit = {},
    maxBytes = 2_000_000,
  ): Promise<Record<string, unknown>> {
    const signal = boundedSignal(init.signal ?? undefined, this.timeoutMs);
    try {
      const response = await this.fetch(`${this.issuer}${path}`, {
        ...init,
        signal,
        redirect: "error",
        credentials: "omit",
        cache: "no-store",
        headers: { accept: "application/json", ...init.headers },
      });
      if (!response.ok) {
        let errorCode = "";
        if (
          response.headers.get("content-type")?.split(";")[0]?.trim() ===
            "application/json" &&
          response.body
        ) {
          try {
            const body = object(JSON.parse(await response.text()));
            if (
              typeof body.error === "object" &&
              body.error !== null &&
              !Array.isArray(body.error) &&
              typeof (body.error as Record<string, unknown>).code === "string"
            ) {
              errorCode = (body.error as Record<string, unknown>)
                .code as string;
            } else if (typeof body.code === "string") {
              errorCode = body.code;
            }
          } catch {
            // Keep protocol errors generic when an upstream body is malformed.
          }
        } else {
          await response.body?.cancel();
        }
        if (errorCode === "IP_ACCESS_ROUTE_REJECTED") {
          throw new LmmError(
            "ip_policy",
            "LMM request was blocked by the current IP access policy. Change network or ask the administrator to allow this IP.",
          );
        }
        if (response.status === 401 || response.status === 403) {
          throw new LmmError(
            "unauthorized",
            "LMM authorization is unavailable or was revoked. Use /login; no API-key fallback is allowed.",
          );
        }
        if (response.status === 429) {
          throw new LmmError(
            "rate_limited",
            "LMM rate limit reached. Wait a moment and retry.",
          );
        }
        if (response.status >= 500) {
          throw new LmmError(
            "upstream_unavailable",
            `LMM service is temporarily unavailable (HTTP ${response.status}). Try again shortly.`,
          );
        }
        throw new LmmError(
          "http_error",
          `LMM HTTP request failed (${response.status}).`,
        );
      }
      if (response.status === 204 || path === "/api/oauth2/revoke") {
        await response.body?.cancel();
        return {};
      }
      requireValue(
        response.headers.get("content-type")?.split(";")[0]?.trim() ===
          "application/json",
      );
      requireValue(response.body);
      const reader = response.body.getReader();
      const chunks: Uint8Array[] = [];
      let bytes = 0;
      try {
        while (true) {
          signal.throwIfAborted();
          const { value, done } = await reader.read();
          if (done) break;
          bytes += value.byteLength;
          requireValue(
            bytes <= maxBytes,
            "LMM response exceeded its size limit.",
          );
          chunks.push(value);
        }
      } finally {
        await reader.cancel().catch(() => {});
        reader.releaseLock();
      }
      return object(JSON.parse(Buffer.concat(chunks).toString("utf8")));
    } catch (error) {
      if (error instanceof LmmError) throw error;
      if (signal.aborted)
        throw new LmmError("aborted", "LMM request cancelled or timed out.");
      throw new LmmError(
        "transport_error",
        "LMM network request failed. Check your connection and retry.",
      );
    }
  }

  form(
    path: string,
    fields: Record<string, string>,
    signal?: AbortSignal,
  ): Promise<Record<string, unknown>> {
    return this.request(
      path,
      {
        method: "POST",
        signal,
        headers: { "content-type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams(fields),
      },
      64_000,
    );
  }

  bearer(
    path: string,
    access: string,
    signal?: AbortSignal,
  ): Promise<Record<string, unknown>> {
    return this.request(path, {
      headers: { authorization: `Bearer ${access}` },
      signal,
    });
  }
}
