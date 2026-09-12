import assert from 'node:assert/strict';
import { test } from 'node:test';
import { resolveKnownCapabilities } from '../src/capabilities.ts';
import type { CatalogEntry } from '../src/catalog.ts';

function entry(model: string, apis: CatalogEntry['apis']): CatalogEntry {
  return { upstream_model: model, apis } as CatalogEntry;
}
test('exact known model and advertised protocol supply capabilities, never vendor cost', () => {
  const result = resolveKnownCapabilities(entry('gpt-4o', ['openai-responses']));
  assert.ok(result);
  assert.equal(result.api, 'openai-responses');
  assert.ok(result.contextWindow > 0 && result.maxTokens > 0);
  assert.match(result.provenance, /openai\/gpt-4o$/);
  assert.equal('cost' in result, false);
});
test('unknown names, aliases and unadvertised vendor protocols remain unknown', () => {
  assert.equal(resolveKnownCapabilities(entry('gpt-4o-unverified-alias', ['openai-responses'])), undefined);
  assert.equal(resolveKnownCapabilities(entry('gpt-4o', ['anthropic-messages'])), undefined);
});
test('native vendor fallbacks are removed from gateway capabilities', () => {
  const result = resolveKnownCapabilities(entry('claude-fable-5', ['anthropic-messages']));
  assert.ok(result);
  assert.equal('allowedFallbackModels' in result.compat, false);
});
