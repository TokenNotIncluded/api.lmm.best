import type { Api, Model } from '@earendil-works/pi-ai';
import { getBuiltinModels } from '@earendil-works/pi-ai/providers/all';
import type { CapabilityResolver, VerifiedCapabilities } from './catalog.ts';
import { SUPPORTED_APIS, type LmmApi } from './protocol.ts';

const sources: readonly [string, ReadonlyMap<string, Model<Api>>][] = (
  ['openai', 'anthropic', 'deepseek', 'xai', 'moonshotai', 'minimax'] as const
).map((provider) => [
  provider, new Map(getBuiltinModels(provider).map((model) => [model.id, model])),
]);

/** Exact vendor catalog matches only. LMM account pricing always comes from its server. */
export const resolveKnownCapabilities: CapabilityResolver = (entry) => {
  const candidates: VerifiedCapabilities[] = [];
  for (const [provider, models] of sources) {
    const model = models.get(entry.upstream_model);
    if (!model || !SUPPORTED_APIS.includes(model.api as LmmApi) || !entry.apis.includes(model.api as LmmApi)) continue;
    const compat = structuredClone(model.compat ?? {});
    // A vendor fallback may use a different model and price not authorized by this catalog entry.
    if ('allowedFallbackModels' in compat) delete compat.allowedFallbackModels;
    candidates.push({
      api: model.api as LmmApi, contextWindow: model.contextWindow, maxTokens: model.maxTokens,
      reasoning: model.reasoning, input: [...model.input], compat,
      thinkingLevelMap: model.thinkingLevelMap ? structuredClone(model.thinkingLevelMap) : undefined,
      provenance: `pi-ai vendor catalog: ${provider}/${model.id}`,
    });
  }
  if (!candidates.length) return undefined;
  const profile = ({ provenance: _source, ...rest }: VerifiedCapabilities) => JSON.stringify(rest);
  const first = candidates[0]!;
  // Ambiguous aliases are not resolved by guessing which upstream is behind the gateway.
  return candidates.every((candidate) => profile(candidate) === profile(first)) ? first : undefined;
};
