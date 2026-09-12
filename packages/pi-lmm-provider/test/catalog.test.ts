import assert from 'node:assert/strict';
import { test } from 'node:test';
import { parseCatalog } from '../src/catalog.ts';

const group = { id: 'ZGVmYXVsdA', name: 'default', scope: 'group:ZGVmYXVsdA', multiplier: 2 };
const pricing = {
  currency: 'USD', unit: 'million_tokens', price_basis: 'configured_base_rates',
  group_multiplier: 2, trust_multiplier: 0.5, input: 1, output: 2, cache_read: 0.1, cache_write: 0.2,
  request: null, final_cost_depends_on_usage: true, updated_at: 1789240000,
};
const model = {
  id: 'lmm:ZGVmYXVsdA:Z3B0LXRlc3Q', group_id: 'ZGVmYXVsdA', group: 'default',
  upstream_model: 'gpt-test', name: 'default / gpt-test', apis: ['openai-completions'], pricing,
  native_cost: { input: 1, output: 2, cacheRead: 0.1, cacheWrite: 0.2 },
};
const catalog = { schema_version: 1, resource: 'https://lmm.test/api/oauth2', updated_at: 1789240000, groups: [group], models: [model] };

test('accepts matching native cost under configured base rates', () => {
  assert.equal(parseCatalog(catalog, catalog.resource, 'catalog:read balance:read models:invoke group:ZGVmYXVsdA').models[0]!.native_cost?.output, 2);
});

test('rejects a mismatched native cost and does not treat missing cache as zero', () => {
  assert.throws(() => parseCatalog({ ...catalog, models: [{ ...model, native_cost: { ...model.native_cost, output: 3 } }] }, catalog.resource, 'group:ZGVmYXVsdA'));
  const missingCache = { ...model, native_cost: null, pricing: { ...pricing, cache_read: null, cache_write: null } };
  assert.equal(parseCatalog({ ...catalog, models: [missingCache] }, catalog.resource, 'group:ZGVmYXVsdA').models[0]!.native_cost, null);
});

test('accepts a dynamic estimate as native cost while retaining its estimate basis', () => {
  const result = parseCatalog({ ...catalog, models: [{ ...model, pricing: { ...pricing, price_basis: 'dynamic_estimate' } }] }, catalog.resource, 'group:ZGVmYXVsdA');
  assert.equal(result.models[0]!.pricing.price_basis, 'dynamic_estimate');
  assert.equal(result.models[0]!.native_cost?.input, 1);
});
