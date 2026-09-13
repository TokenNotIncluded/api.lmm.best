import type { Model, ModelCost } from '@earendil-works/pi-ai';
import {
  PROVIDER_ID, SUPPORTED_APIS, base64url, canonicalId, nullableNumber, object, nonnegative,
  requireValue, text, unixSeconds, type LmmApi,
} from './protocol.ts';

export interface Group { id: string; name: string; scope: string; multiplier: number | null }
export interface Pricing {
  currency: string; unit: string; price_basis: string;
  group_multiplier: number | null; trust_multiplier: number | null;
  input: number | null; output: number | null; cache_read: number | null; cache_write: number | null; request: number | null;
  final_cost_depends_on_usage: boolean; updated_at: number;
}
export interface CatalogEntry {
  id: string; group_id: string; group: string; upstream_model: string; name: string;
  apis: LmmApi[]; pricing: Pricing; native_cost: ModelCost | null;
}
export interface Catalog { schema_version: 1; resource: string; updated_at: number; groups: Group[]; models: CatalogEntry[] }

function parseGroup(value: unknown): Group {
  const row = object(value);
  const id = canonicalId(row.id);
  const name = text(row.name);
  requireValue(id === base64url(name) && row.scope === `group:${id}`);
  return { id, name, scope: `group:${id}`, multiplier: nullableNumber(row.multiplier) };
}

function parsePricing(value: unknown): Pricing {
  const row = object(value);
  requireValue(typeof row.final_cost_depends_on_usage === 'boolean');
  return {
    currency: text(row.currency, 16), unit: text(row.unit, 64), price_basis: text(row.price_basis, 64),
    group_multiplier: nullableNumber(row.group_multiplier), trust_multiplier: nullableNumber(row.trust_multiplier),
    input: nullableNumber(row.input), output: nullableNumber(row.output), cache_read: nullableNumber(row.cache_read),
    cache_write: nullableNumber(row.cache_write), request: nullableNumber(row.request),
    final_cost_depends_on_usage: row.final_cost_depends_on_usage, updated_at: unixSeconds(row.updated_at),
  };
}

function nativeCost(value: unknown, pricing: Pricing): ModelCost | null {
  // Missing fields are an explicit integration gate, never a zero-price fallback.
  if (value === null || value === undefined) return null;
  const row = object(value);
  const cost: ModelCost = {
    input: nonnegative(row.input), output: nonnegative(row.output),
    cacheRead: nonnegative(row.cacheRead), cacheWrite: nonnegative(row.cacheWrite),
  };
  requireValue(pricing.currency === 'USD' && pricing.unit === 'million_tokens' &&
    (pricing.price_basis === 'configured_base_rates' || pricing.price_basis === 'dynamic_estimate') && pricing.request === null,
    'LMM advertised a native cost for incompatible billing.');
  requireValue(cost.input === pricing.input && cost.output === pricing.output && cost.cacheRead === pricing.cache_read && cost.cacheWrite === pricing.cache_write,
    'LMM native cost differs from its already-adjusted server rates.');
  return cost;
}

function parseEntry(value: unknown, groups: ReadonlyMap<string, Group>): CatalogEntry {
  const row = object(value);
  const group_id = canonicalId(row.group_id);
  const group = text(row.group);
  const upstream_model = text(row.upstream_model, 512);
  const id = text(row.id, 4096);
  requireValue(groups.get(group_id)?.name === group && id === `lmm:${group_id}:${base64url(upstream_model)}`);
  requireValue(Array.isArray(row.apis) && row.apis.length > 0 && row.apis.length <= 16);
  const apis: LmmApi[] = [];
  for (const value of row.apis) {
    const api = text(value, 64);
    if ((SUPPORTED_APIS as readonly string[]).includes(api)) apis.push(api as LmmApi);
  }
  requireValue(new Set(apis).size === apis.length);
  const pricing = parsePricing(row.pricing);
  return { id, group_id, group, upstream_model, name: text(row.name), apis, pricing, native_cost: nativeCost(row.native_cost, pricing) };
}

export function parseCatalog(value: unknown, resource: string, grantedScope: string): Catalog {
  const body = object(value);
  requireValue(body.schema_version === 1 && body.resource === resource);
  requireValue(Array.isArray(body.groups) && body.groups.length <= 2000 && Array.isArray(body.models) && body.models.length <= 20_000);
  const scopes = new Set(grantedScope.split(' '));
  const groups = body.groups.map(parseGroup);
  const byGroup = new Map(groups.map((group) => [group.id, group]));
  requireValue(byGroup.size === groups.length && groups.every((group) => scopes.has(group.scope)), 'LMM catalog contains a group outside the granted OAuth scopes.');
  const models = body.models.map((value) => parseEntry(value, byGroup));
  requireValue(new Set(models.map((model) => model.id)).size === models.length);
  return { schema_version: 1, resource, updated_at: unixSeconds(body.updated_at), groups, models };
}

/** Deliberately not inferred from names. The v1 wire contract does not yet provide this. */
export interface VerifiedCapabilities {
  api: LmmApi;
  contextWindow: number;
  maxTokens: number;
  reasoning: boolean;
  input: ('text' | 'image')[];
  /** Explicit protocol flags, reviewed with the capability source. */
  compat: NonNullable<Model<LmmApi>['compat']>;
  thinkingLevelMap?: Model<LmmApi>['thinkingLevelMap'];
  provenance: string;
}
export type CapabilityResolver = (entry: Readonly<CatalogEntry>) => VerifiedCapabilities | undefined;
export interface Admission { entry: CatalogEntry; model?: Model<LmmApi>; reason?: string }

/** Pi renders model.id in its native picker. Keep wire IDs separate from readable IDs. */
export function nativeModelId(entry: Pick<CatalogEntry, 'group' | 'upstream_model'>): string {
  const part = (value: string) => value.replaceAll('%', '%25').replaceAll(' / ', '%20%2F%20');
  return `${part(entry.group)} / ${part(entry.upstream_model)}`;
}

export function admitCatalog(catalog: Catalog, issuer: string, resolve: CapabilityResolver = () => undefined): Admission[] {
  return catalog.models.map((entry) => {
    const capabilities = resolve(entry);
    if (!capabilities) return { entry, reason: 'Capability metadata unavailable; not registered in /model.' };
    if (!entry.native_cost) return { entry, reason: 'No truthful native static cost; available for price inspection only.' };
    requireValue(entry.apis.includes(capabilities.api) && Number.isSafeInteger(capabilities.contextWindow) && capabilities.contextWindow > 0 &&
      Number.isSafeInteger(capabilities.maxTokens) && capabilities.maxTokens > 0 && capabilities.maxTokens <= capabilities.contextWindow &&
      typeof capabilities.reasoning === 'boolean' && capabilities.input.length > 0 && capabilities.input.every((input) => input === 'text' || input === 'image'));
    text(capabilities.provenance);
    const multiplier = entry.pricing.group_multiplier === null ? 'unknown' : `${entry.pricing.group_multiplier}×`;
    const estimate = entry.pricing.price_basis === 'dynamic_estimate' ? ' · estimate' : '';
    const model: Model<LmmApi> = {
      id: nativeModelId(entry), name: `${entry.group} / ${entry.upstream_model} · group ${multiplier}${estimate}`,
      provider: PROVIDER_ID, api: capabilities.api,
      baseUrl: capabilities.api === 'anthropic-messages' ? issuer : `${issuer}/v1`,
      reasoning: capabilities.reasoning, input: [...capabilities.input], contextWindow: capabilities.contextWindow,
      maxTokens: capabilities.maxTokens, compat: structuredClone(capabilities.compat), cost: { ...entry.native_cost },
      ...(capabilities.thinkingLevelMap ? { thinkingLevelMap: structuredClone(capabilities.thinkingLevelMap) } : {}),
    };
    return { entry, model };
  });
}

function amount(value: number | null): string { return value === null ? 'unknown' : String(value); }

export function priceReport(admissions: readonly Admission[], filter = ''): string {
  const selected = admissions.filter(({ entry }) => `${entry.id} ${entry.name}`.toLowerCase().includes(filter.toLowerCase()));
  const lines = selected.slice(0, 40).map(({ entry, reason }) => {
    const p = entry.pricing;
    return [
      `${entry.group} / ${entry.upstream_model}`,
      `  ${p.currency}/${p.unit}; ${p.price_basis}; group ×${amount(p.group_multiplier)}, trust ×${amount(p.trust_multiplier)} (already included)`,
      `  input ${amount(p.input)}, output ${amount(p.output)}, cache read ${amount(p.cache_read)}, cache write ${amount(p.cache_write)}, request ${amount(p.request)}`,
      `  ${reason ?? 'Select using native /model.'} Snapshot ${new Date(p.updated_at * 1000).toISOString()}.`,
    ].join('\n');
  });
  if (selected.length > 40) lines.push(`Showing 40 of ${selected.length}; pass an ID or name to /lmm-prices to narrow the read-only report.`);
  lines.push('Final billing depends on usage and current server policy; this is not a locked quote or a budget approval. Unknown is not free.');
  return lines.join('\n');
}
