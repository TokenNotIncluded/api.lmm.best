import snapshot from "./model-limits.json";
import { assert, record } from "./protocol";
import type { CatalogModel } from "./wire";

export interface ModelLimitOverride { contextWindow: number; maxOutputTokens: number }
export interface ReferenceModel {
  source: string; type: string; family: string | null;
  context: number | null; input: number | null; output: number | null;
  inputModalities: string[] | null; outputModalities: string[] | null;
  reasoning: boolean | null; toolCalling: boolean | null;
}
const references: Readonly<Record<string, ReferenceModel[]>> = snapshot.models;
const qualified = new Map(Object.values(references).flatMap(rows => rows.map(row => [row.source, row] as const)));
const nonChatIds: readonly string[] = snapshot.nonChatIds;
function candidates(id: string, models = references): ReferenceModel[] {
  const exact = models === references ? qualified.get(id) : Object.values(models).flat().find(row => row.source === id);
  return exact ? [exact] : Object.hasOwn(models, id) ? models[id] : [];
}
function textChat(row: ReferenceModel): boolean {
  return row.type === "chat" && !!row.inputModalities?.includes("text") &&
    row.outputModalities?.length === 1 && row.outputModalities[0] === "text";
}
/** Keep known non-chat entries excluded even when a user sets a limit or tool override. */
export function isTextChatModel(id: string): boolean {
  if (nonChatIds.includes(id) || nonChatIds.some(model => snapshot.providers.some(provider => id === `${provider}/${model}`))) return false;
  return candidates(id).every(textChat);
}
/** Disagreeing exact IDs never borrow whichever provider happened to be first. */
export function referenceMetadata(id: string, models = references): ReferenceModel | undefined {
  const rows = candidates(id, models);
  const profile = ({ source: _source, ...row }: ReferenceModel) => JSON.stringify(row);
  if (!rows.length || !rows.every(row => textChat(row) && profile(row) === profile(rows[0]))) return undefined;
  const row = rows[0];
  if (!Number.isSafeInteger(row.context) || row.context! <= 1 ||
      !Number.isSafeInteger(row.output) || row.output! <= 0 ||
      (row.input !== null && (!Number.isSafeInteger(row.input) || row.input <= 0))) return undefined;
  return row;
}
export function toolCallingEnabled(entry: CatalogModel, models: readonly string[]): boolean {
  return isTextChatModel(entry.upstream_model) && (models.includes(entry.id) || models.includes(entry.upstream_model));
}

/** Exact IDs only: never infer limits from a model's name or billing group. */
export function modelMetadata(entry: CatalogModel, overrides: Record<string, ModelLimitOverride> = {}, inputCap?: number, outputCap = 4096) {
  assert(isTextChatModel(entry.upstream_model), "This LMM model is not a supported text chat model.");
  assert(Number.isSafeInteger(outputCap) && outputCap > 0 && outputCap <= 2000000, "Invalid lmm.maxOutputTokens setting.");
  assert(inputCap === undefined || (Number.isSafeInteger(inputCap) && inputCap > 0 && inputCap <= 2000000), "Invalid lmm.maxInputTokens setting.");
  const override = Object.hasOwn(overrides, entry.id) ? overrides[entry.id] :
    Object.hasOwn(overrides, entry.upstream_model) ? overrides[entry.upstream_model] : undefined;
  const known = referenceMetadata(entry.upstream_model);
  let contextWindow: number, output: number, upstreamInput = Infinity, source: string;
  if (override !== undefined) {
    const value = record(override);
    assert(Number.isSafeInteger(value.contextWindow) && value.contextWindow > 1 && value.contextWindow <= 2000000 &&
      Number.isSafeInteger(value.maxOutputTokens) && value.maxOutputTokens > 0 && value.maxOutputTokens < value.contextWindow,
      "Invalid lmm.modelLimits override.");
    contextWindow = value.contextWindow;
    output = value.maxOutputTokens;
    source = "user override";
  } else if (known) {
    contextWindow = known.context!;
    output = known.output!;
    upstreamInput = known.input ?? Infinity;
    source = `models.dev: ${known.source} (snapshot ${snapshot.retrieved.slice(0, 10)}; upstream reference, not gateway guarantee)`;
  } else {
    contextWindow = (inputCap ?? 32768) + outputCap;
    output = outputCap;
    source = "unknown model limits; compatibility estimate — configure lmm.modelLimits";
  }
  const maxOutputTokens = Math.min(output, outputCap, contextWindow - 1);
  const maxInputTokens = Math.min(contextWindow - maxOutputTokens, upstreamInput, inputCap ?? Infinity);
  const ratio = entry.groupMultiplier === null ? "倍率未知" : `${entry.groupMultiplier}×`;
  const capabilities = known ? `Upstream reference: reasoning ${known.reasoning === true ? "yes" : known.reasoning === false ? "no" : "unknown"}; tools ${known.toolCalling === true ? "yes" : known.toolCalling === false ? "no" : "unknown"}. Gateway tools require lmm.toolModels verification.` : "Upstream capabilities unknown. Gateway tools require lmm.toolModels verification.";
  return {
    name: `${entry.group} / ${entry.upstream_model} · ${ratio}`,
    detail: `${entry.group} · ${ratio}`,
    tooltip: `LMM · ${entry.group} · 分组倍率 ${ratio}\nContext: ${contextWindow.toLocaleString("en-US")} tokens; input budget: ${maxInputTokens.toLocaleString("en-US")}; output budget: ${maxOutputTokens.toLocaleString("en-US")}\n${source}\n${capabilities}\nToken pricing: current LMM server pricing applies; reference catalog prices are not used.\nInput reserves the configured output budget. Token counting is a conservative UTF-8 byte estimate, not an exact tokenizer. Usage is billed to your LMM wallet.`,
    maxInputTokens, maxOutputTokens,
  };
}
