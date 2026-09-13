import { createHash } from 'node:crypto';
import type { Credential, Provider, RefreshModelsContext } from '@earendil-works/pi-ai';
import { balanceStatus, parseBalance, type Balance } from './balance.ts';
import { admitCatalog, parseCatalog, priceReport, type Admission, type CapabilityResolver } from './catalog.ts';
import { resolveKnownCapabilities } from './capabilities.ts';
import { LmmHttp, type HttpOptions } from './http.ts';
import { LmmOAuth } from './oauth.ts';
import { PROVIDER_ID, LmmError, boundedSignal, credential, type LmmCredential } from './protocol.ts';
import { createRelay } from './stream.ts';

function fingerprint(access: string): string { return createHash('sha256').update(access).digest('hex'); }
interface Snapshot {
  session: string;
  accessHash: string;
  expires: number;
  admissions: Admission[];
  balance?: Balance;
  balanceStale: boolean;
}
export interface LmmProviderOptions extends HttpOptions {
  /** Test/review seam, not a new wire contract, flag, or production capability default. */
  capabilities?: CapabilityResolver;
  loginTimeoutMs?: number;
  refreshJournalDirectory?: string;
  onStatus?: (status: string | undefined) => void;
}

export class LmmIntegration {
  readonly http: LmmHttp;
  readonly oauth: LmmOAuth;
  readonly provider: Provider;
  private readonly capabilities?: CapabilityResolver;
  private readonly notify: (status: string | undefined) => void;
  private readonly shutdown = new AbortController();
  private current?: Snapshot;
  private pendingLogin?: Snapshot;
  private readonly revoked = new Set<string>();
  private balancePending?: { hash: string; task: Promise<void> };
  private authSnapshotPending?: { hash: string; task: Promise<void> };
  private epoch = 0;

  constructor(options: LmmProviderOptions = {}) {
    this.http = new LmmHttp(options);
    this.oauth = new LmmOAuth(this.http, options.loginTimeoutMs, options.refreshJournalDirectory);
    this.capabilities = options.capabilities ?? resolveKnownCapabilities;
    this.notify = options.onStatus ?? (() => {});
    const relay = createRelay(this.http, {
      lookup: (id, access) => this.lookup(id, access),
      onFinish: (access) => this.refreshBalance(access),
    });
    this.provider = {
      id: PROVIDER_ID, name: 'LMM', baseUrl: this.http.issuer,
      auth: {
        oauth: {
          name: 'LMM browser login',
          login: async (interaction) => {
            this.clear();
            const epoch = this.epoch;
            const issued = await this.oauth.login({ ...interaction, signal: AbortSignal.any([interaction.signal, this.shutdown.signal]) });
            // Native Pi persists the credential, then does a network-disabled refresh.
            // Stage (do not publish) the authenticated catalog now for that post-commit refresh.
            try {
              const snapshot = await this.fetchSnapshot(issued, boundedSignal(interaction.signal));
              if (epoch === this.epoch) this.pendingLogin = snapshot;
            } catch {
              // Retain a valid login even if read-only discovery is temporarily unavailable.
              interaction.notify({ type: 'progress', message: 'LMM login succeeded, but its catalog or balance is unavailable. Use /lmm-prices to retry discovery; unknown models are not registered.' });
            }
            interaction.notify({ type: 'progress', message: 'LMM uses native /model. Entries without verified capabilities or truthful native pricing remain read-only. Automatic refresh is fenced by a durable local journal; /logout is local only.' });
            return issued;
          },
          refresh: async (value, signal) => this.oauth.refresh(value, AbortSignal.any([signal, this.shutdown.signal])),
          toAuth: async (value) => {
            const current = credential(value, this.http.issuer);
            if (this.revoked.has(current.lmm_session)) throw new LmmError('revoked', 'This LMM grant was revoked. Use /logout and then /login.');
            await this.refreshSnapshotForAuth(current);
            // No API-key field: especially important for the Anthropic SDK adapter.
            return { headers: { authorization: `Bearer ${current.access}` }, baseUrl: this.http.issuer };
          },
        },
      },
      getModels: () => structuredClone(this.current?.admissions.flatMap(({ model }) => model ? [model] : []) ?? []),
      filterModels: (models, stored) => {
        const current = this.maybeCredential(stored);
        return current && this.current?.session === current.lmm_session && this.current.accessHash === fingerprint(current.access) && this.current.expires > Date.now()
          ? models.filter((model) => this.current?.admissions.some((item) => item.model?.id === model.id)) : [];
      },
      refreshModels: (context) => this.refreshModels(context),
      stream: relay.stream,
      streamSimple: relay.streamSimple,
    };
  }

  private maybeCredential(value: Credential | undefined): LmmCredential | undefined {
    try {
      const result = credential(value, this.http.issuer);
      return this.revoked.has(result.lmm_session) ? undefined : result;
    } catch { return undefined; }
  }

  private clear(): void {
    this.epoch++;
    this.current = undefined;
    this.pendingLogin = undefined;
    this.balancePending = undefined;
    this.authSnapshotPending = undefined;
    this.notify(undefined);
  }

  private status(): void {
    if (!this.current) return this.notify(undefined);
    const admitted = this.current.admissions.filter((item) => item.model).length;
    this.notify(`${balanceStatus(this.current.balance, this.current.balanceStale)}${admitted === 0 ? ' · models gated' : ''}`);
  }

  private async fetchSnapshot(value: LmmCredential, signal: AbortSignal): Promise<Snapshot> {
    const scope = new Set(value.scope.split(' '));
    const snapshot: Snapshot = {
      session: value.lmm_session, accessHash: fingerprint(value.access), expires: value.expires,
      admissions: [], balanceStale: false,
    };
    const catalog = await this.http.bearer('/api/oauth2/catalog', value.access, signal);
    const parsed = parseCatalog(catalog, this.http.resource, value.scope);
    if (scope.has('models:invoke')) snapshot.admissions = admitCatalog(parsed, this.http.issuer, this.capabilities);
    else snapshot.admissions = parsed.models.map((entry) => ({ entry, reason: 'models:invoke was not granted; read-only.' }));
    if (scope.has('balance:read')) {
      try { snapshot.balance = parseBalance(await this.http.bearer('/api/oauth2/balance', value.access, signal)); }
      catch { snapshot.balanceStale = true; }
    }
    return snapshot;
  }

  private async refreshSnapshotForAuth(value: LmmCredential): Promise<void> {
    const hash = fingerprint(value.access);
    if (this.current?.session === value.lmm_session && this.current.accessHash === hash && this.current.expires > Date.now()) return;
    if (this.authSnapshotPending?.hash === hash) return this.authSnapshotPending.task;
    const epoch = ++this.epoch;
    const task = (async () => {
      try {
        const snapshot = await this.fetchSnapshot(value, boundedSignal(this.shutdown.signal));
        if (epoch === this.epoch && !this.shutdown.signal.aborted) {
          this.current = snapshot;
          this.pendingLogin = undefined;
          this.status();
        }
      } catch { /* toAuth still returns the new bearer; lookup remains fail-closed. */ }
    })();
    this.authSnapshotPending = { hash, task };
    try { await task; } finally { if (this.authSnapshotPending?.task === task) this.authSnapshotPending = undefined; }
  }

  private async refreshModels(context: RefreshModelsContext): Promise<void> {
    const value = this.maybeCredential(context.credential);
    if (!value) {
      await context.publish({ persist: null, update: () => this.clear() });
      return;
    }
    if (!context.allowNetwork) {
      const hash = fingerprint(value.access);
      const snapshot = this.pendingLogin?.session === value.lmm_session && this.pendingLogin.accessHash === hash ? this.pendingLogin :
        this.current?.session === value.lmm_session && this.current.accessHash === hash ? this.current : undefined;
      await context.publish({ persist: null, update: () => {
        if (this.current?.session !== value.lmm_session) this.epoch++;
        this.current = snapshot;
        this.pendingLogin = undefined;
        this.status();
      } });
      return;
    }
    const signal = AbortSignal.any([context.signal, this.shutdown.signal]);
    this.epoch++;
    const epoch = this.epoch;
    const snapshot = await this.fetchSnapshot(value, signal);
    await context.publish({ persist: null, update: () => {
      if (epoch !== this.epoch || this.shutdown.signal.aborted) return;
      this.current = snapshot;
      this.pendingLogin = undefined;
      this.status();
    } });
  }

  private lookup(modelId: string, access: string): Admission | undefined {
    if (!this.current || this.current.expires <= Date.now() || this.current.accessHash !== fingerprint(access)) return undefined;
    return this.current.admissions.find((item) => item.model?.id === modelId || item.entry.id === modelId);
  }

  modelForLegacyId(id: string) {
    const model = this.current?.admissions.find((item) => item.entry.id === id)?.model;
    return model ? structuredClone(model) : undefined;
  }

  async refreshBalance(access: string, signal?: AbortSignal): Promise<void> {
    const hash = fingerprint(access);
    const snapshot = this.current;
    if (!snapshot || snapshot.accessHash !== hash) return;
    if (this.balancePending?.hash === hash) return this.balancePending.task;
    const epoch = this.epoch;
    const task = (async () => {
      try {
        const balance = parseBalance(await this.http.bearer('/api/oauth2/balance', access, boundedSignal(AbortSignal.any([...(signal ? [signal] : []), this.shutdown.signal]))));
        if (epoch === this.epoch && this.current === snapshot) { snapshot.balance = balance; snapshot.balanceStale = false; }
      } catch {
        if (epoch === this.epoch && this.current === snapshot) snapshot.balanceStale = true;
      }
      if (epoch === this.epoch && this.current === snapshot) this.status();
    })();
    this.balancePending = { hash, task };
    try { await task; }
    finally { if (this.balancePending?.task === task) this.balancePending = undefined; }
  }

  prices(filter = ''): string {
    return this.current ? priceReport(this.current.admissions, filter) : 'No account-scoped LMM catalog. Use /login, then retry /lmm-prices. No models or capabilities are guessed.';
  }

  async revoke(access: string, signal: AbortSignal): Promise<void> {
    const snapshot = this.current;
    if (snapshot && snapshot.accessHash !== fingerprint(access)) throw new LmmError('account_changed', 'LMM account changed; retry from the current account.');
    await this.oauth.revoke(access, signal);
    if (snapshot) this.revoked.add(snapshot.session);
    // Do not erase a new account if the old account's revocation finishes late.
    if (this.current === snapshot) this.clear();
  }

  dispose(): void { this.shutdown.abort(); this.clear(); }
}
