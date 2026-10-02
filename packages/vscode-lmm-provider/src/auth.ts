import { createHash, randomBytes } from "node:crypto";
import { mkdir } from "node:fs/promises";
import { join } from "node:path";
import { listenCallback } from "./callback";
import {
  assert,
  CLIENT,
  ISSUER,
  LmmError,
  record,
  RESOURCE,
  SCOPES,
} from "./protocol";
export interface SecretStore {
  get(key: string): Thenable<string | undefined> | Promise<string | undefined>;
  store(key: string, value: string): Thenable<void> | Promise<void>;
  delete(key: string): Thenable<void> | Promise<void>;
}
export interface Credential {
  access: string;
  refresh: string;
  expires: number;
  scope: string;
  resource: string;
}
const KEY = "lmm.oauth.v1";
export function parseToken(
  body: unknown,
  previous?: Credential,
  startedAt = Date.now(),
): Credential {
  const b = record(body);
  assert(
    b.token_type === "Bearer" &&
      typeof b.access_token === "string" &&
      /^lmm_at_[A-Za-z0-9_-]+$/.test(b.access_token),
  );
  assert(
    typeof b.refresh_token === "string" &&
      /^lmm_rt_[A-Za-z0-9_-]+$/.test(b.refresh_token),
  );
  assert(
    Number.isSafeInteger(b.expires_in) &&
      b.expires_in > 0 &&
      b.expires_in <= 86400,
  );
  const scope = b.scope ?? previous?.scope;
  assert(typeof scope === "string");
  const scopes = scope.split(" ");
  assert(
    new Set(scopes).size === scopes.length &&
      SCOPES.every((s) => scopes.includes(s)),
  );
  assert(
    scopes.some((s) => /^group:[A-Za-z0-9_-]+$/.test(s)),
    "LMM did not grant a model group.",
  );
  assert(
    scopes.every((s) => SCOPES.includes(s) || /^group:[A-Za-z0-9_-]+$/.test(s)),
  );
  if (previous)
    assert(
      b.refresh_token !== previous.refresh &&
        scopes.every((s) => previous.scope.split(" ").includes(s)),
      "LMM refresh response was invalid. Sign in again.",
    );
  return {
    access: b.access_token,
    refresh: b.refresh_token,
    expires: startedAt + b.expires_in * 1000,
    scope,
    resource: RESOURCE,
  };
}
export async function jsonRequest(
  path: string,
  init: RequestInit = {},
  fetcher = fetch,
): Promise<Record<string, any>> {
  const response = await fetcher(`${ISSUER}${path}`, {
    ...init,
    redirect: "error",
    credentials: "omit",
    cache: "no-store",
    signal: AbortSignal.any([
      ...(init.signal ? [init.signal] : []),
      AbortSignal.timeout(30000),
    ]),
  });
  if (!response.ok) {
    await response.body?.cancel();
    throw new LmmError(
      "http",
      `LMM request failed (HTTP ${response.status}).${response.status === 401 ? " Sign in again." : ""}`,
    );
  }
  if (response.status === 204 || path.endsWith("/revoke")) {
    await response.body?.cancel();
    return {};
  }
  assert(
    response.headers.get("content-type")?.includes("application/json") &&
      response.body,
  );
  const reader = response.body.getReader();
  let text = "";
  const decoder = new TextDecoder();
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      text += decoder.decode(value, { stream: true });
      assert(text.length <= 4000000, "LMM response is too large.");
    }
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
  return record(JSON.parse(text + decoder.decode()));
}
export class Auth {
  private pending?: Promise<Credential>;
  private transaction: Promise<unknown> = Promise.resolve();
  constructor(
    private readonly secrets: SecretStore,
    private readonly fetcher = fetch,
    private readonly storageDirectory?: string,
  ) {}
  private exclusive<T>(fn: () => Promise<T>): Promise<T> {
    const operation = this.transaction.then(fn, fn);
    this.transaction = operation.catch(() => {});
    return operation;
  }
  private form(
    path: string,
    fields: Record<string, string>,
    signal?: AbortSignal,
  ) {
    return jsonRequest(
      path,
      {
        method: "POST",
        headers: { "content-type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams(fields),
        signal,
      },
      this.fetcher,
    );
  }
  async current(): Promise<Credential | undefined> {
    const raw = await this.secrets.get(KEY);
    if (!raw) return undefined;
    const c = record(JSON.parse(raw));
    assert(
      c.resource === RESOURCE &&
        typeof c.access === "string" &&
        /^lmm_at_[A-Za-z0-9_-]+$/.test(c.access) &&
        typeof c.refresh === "string" &&
        /^lmm_rt_[A-Za-z0-9_-]+$/.test(c.refresh) &&
        typeof c.scope === "string" &&
        Number.isFinite(c.expires),
      "Stored LMM login is invalid. Sign in again.",
    );
    return c as Credential;
  }
  async login(
    open: (url: string) => Promise<void>,
    signal: AbortSignal,
  ): Promise<void> {
    return this.exclusive(async () => {
      const [metadata, protectedResource] = await Promise.all([
        jsonRequest(
          "/.well-known/oauth-authorization-server",
          { signal },
          this.fetcher,
        ),
        jsonRequest(
          "/.well-known/oauth-protected-resource/api/oauth2",
          { signal },
          this.fetcher,
        ),
      ]);
      assert(
        metadata.issuer === ISSUER &&
          metadata.authorization_endpoint === `${RESOURCE}/authorize` &&
          metadata.token_endpoint === `${RESOURCE}/token` &&
          metadata.revocation_endpoint === `${RESOURCE}/revoke` &&
          metadata.authorization_response_iss_parameter_supported === true &&
          metadata.code_challenge_methods_supported?.includes("S256") &&
          metadata.response_types_supported?.includes("code"),
      );
      assert(
        protectedResource.resource === RESOURCE &&
          protectedResource.authorization_servers?.length === 1 &&
          protectedResource.authorization_servers[0] === ISSUER,
      );
      const verifier = randomBytes(32).toString("base64url"),
        state = randomBytes(32).toString("base64url");
      const callback = await listenCallback(ISSUER, state, signal, "VS Code");
      try {
        const url = new URL(`${RESOURCE}/authorize`);
        url.search = new URLSearchParams({
          client_id: CLIENT,
          response_type: "code",
          redirect_uri: callback.redirectUri,
          resource: RESOURCE,
          scope: SCOPES.join(" "),
          state,
          code_challenge: createHash("sha256")
            .update(verifier)
            .digest("base64url"),
          code_challenge_method: "S256",
        }).toString();
        await open(url.href);
        const code = await callback.code;
        const started = Date.now();
        const c = parseToken(
          await this.form(
            "/api/oauth2/token",
            {
              grant_type: "authorization_code",
              client_id: CLIENT,
              code,
              redirect_uri: callback.redirectUri,
              code_verifier: verifier,
              resource: RESOURCE,
            },
            signal,
          ),
          undefined,
          started,
        );
        await this.secrets.store(KEY, JSON.stringify(c));
      } finally {
        callback.close();
      }
    });
  }
  async token(signal?: AbortSignal): Promise<Credential> {
    if (this.pending) return this.pending;
    this.pending = this.exclusive(async () => {
      const c = await this.current();
      assert(c, "Sign in with LMM: Sign In first.");
      if (c.expires > Date.now() + 60000) return c;
      // Durable tombstone before consuming a rotating refresh token. A crash or
      // ambiguous HTTP outcome requires login, never a replay of the old token.
      assert(
        this.storageDirectory,
        "LMM refresh storage is unavailable. Sign in again.",
      );
      const directory = join(this.storageDirectory, "refresh-journal");
      await mkdir(directory, { recursive: true, mode: 0o700 });
      try {
        await mkdir(
          join(directory, createHash("sha256").update(c.refresh).digest("hex")),
          { mode: 0o700 },
        );
      } catch {
        throw new LmmError(
          "refresh_consumed",
          "LMM refresh is already in progress or was interrupted. Retry after it finishes, or sign in again.",
        );
      }
      // The hashed consumed-token marker remains across crashes and windows.
      await this.secrets.delete(KEY);
      const started = Date.now();
      const next = parseToken(
        await this.form(
          "/api/oauth2/token",
          {
            grant_type: "refresh_token",
            client_id: CLIENT,
            refresh_token: c.refresh,
            resource: RESOURCE,
          },
          signal,
        ),
        c,
        started,
      );
      await this.secrets.store(KEY, JSON.stringify(next));
      return next;
    });
    try {
      return await this.pending;
    } finally {
      this.pending = undefined;
    }
  }
  async logout(signal?: AbortSignal): Promise<void> {
    return this.exclusive(async () => {
      const c = await this.current();
      await this.secrets.delete(KEY);
      if (c)
        await this.form(
          "/api/oauth2/revoke",
          {
            client_id: CLIENT,
            token: c.access,
            token_type_hint: "access_token",
          },
          signal,
        );
    });
  }
}
