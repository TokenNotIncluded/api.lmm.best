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
test('known language models work over the gateway advertised OpenAI transport', () => {
  for (const id of ['gpt-5.6-sol', 'claude-sonnet-4-6', 'gemini-3.7-flash', 'glm-5.3']) {
    const result = resolveKnownCapabilities(entry(id, ['openai-completions']));
    assert.ok(result, id);
    assert.equal(result.api, 'openai-completions');
    assert.ok(result.contextWindow > 0 && result.maxTokens > 0);
  }
  const translated = resolveKnownCapabilities(entry('gpt-5.6-sol', ['openai-completions']))!;
  assert.equal(translated.reasoning, false);
  assert.ok('supportsReasoningEffort' in translated.compat && 'supportsStore' in translated.compat);
  assert.equal(translated.compat.supportsReasoningEffort, false);
  assert.equal(translated.compat.supportsStore, false);
  assert.equal(resolveKnownCapabilities(entry('gpt-image-2', ['openai-completions'])), undefined);
});
