import {
  base64url,
  canonicalId,
  object,
  requireValue,
  text,
} from "./vendor/protocol.ts";

export interface Model {
  id: string;
  name: string;
  group: string;
  upstream: string;
  apis: string[];
}
export function parseCatalog(
  value: unknown,
  resource: string,
  scope: string,
): Model[] {
  const data = object(value);
  requireValue(data.schema_version === 1 && data.resource === resource);
  requireValue(
    Array.isArray(data.groups) &&
      Array.isArray(data.models) &&
      data.models.length <= 20000,
  );
  const allowed = new Set(scope.split(" "));
  const groups = new Map<string, string>();
  for (const value of data.groups) {
    const row = object(value);
    const id = canonicalId(row.id);
    const name = text(row.name);
    requireValue(
      id === base64url(name) &&
        row.scope === `group:${id}` &&
        allowed.has(`group:${id}`) &&
        !groups.has(id),
    );
    groups.set(id, name);
  }
  const ids = new Set<string>();
  return data.models.map((value) => {
    const row = object(value);
    const group = canonicalId(row.group_id);
    const upstream = text(row.upstream_model, 512);
    const id = text(row.id, 4096);
    requireValue(
      groups.get(group) === row.group &&
        id === `lmm:${group}:${base64url(upstream)}` &&
        !ids.has(id),
    );
    requireValue(
      Array.isArray(row.apis) &&
        row.apis.every((api) => typeof api === "string"),
    );
    ids.add(id);
    return {
      id,
      group,
      upstream,
      name: text(row.name),
      apis: row.apis as string[],
    };
  });
}

// Context limits and tool/image support are user-supplied verified metadata.
// The OAuth catalog v1 does not expose these; never guess from model names.
export interface ModelConfiguration {
  id: string;
  max_tokens: number;
  max_output_tokens?: number;
  capabilities?: {
    tools?: boolean;
    images?: boolean;
    parallel_tool_calls?: boolean;
  };
}
export function zedSettings(
  models: Model[],
  configuration: ModelConfiguration[],
  port: number,
) {
  requireValue(Number.isInteger(port) && port > 0 && port <= 65535);
  const byId = new Map(models.map((model) => [model.id, model]));
  const seen = new Set<string>();
  const available_models = configuration.map((raw) => {
    const entry = object(raw);
    const id = text(entry.id, 4096);
    const model = byId.get(id);
    requireValue(
      model && !seen.has(id),
      "Configured model is missing from the current account catalog or duplicated.",
    );
    seen.add(id);
    requireValue(
      Number.isSafeInteger(entry.max_tokens) &&
        (entry.max_tokens as number) > 0,
    );
    if (entry.max_output_tokens !== undefined)
      requireValue(
        Number.isSafeInteger(entry.max_output_tokens) &&
          (entry.max_output_tokens as number) > 0 &&
          (entry.max_output_tokens as number) <= (entry.max_tokens as number),
      );
    const chat = model.apis.includes("openai-completions");
    requireValue(
      chat || model.apis.includes("openai-responses"),
      "This model has no Zed-compatible OpenAI protocol.",
    );
    const capabilities = object(entry.capabilities ?? {});
    requireValue(
      Object.keys(capabilities).every((key) =>
        ["tools", "images", "parallel_tool_calls"].includes(key),
      ) &&
        Object.values(capabilities).every(
          (value) => typeof value === "boolean",
        ),
    );
    return {
      name: id,
      display_name: model.name,
      max_tokens: entry.max_tokens,
      ...(entry.max_output_tokens === undefined
        ? {}
        : { max_output_tokens: entry.max_output_tokens }),
      capabilities: {
        tools: false,
        images: false,
        parallel_tool_calls: false,
        ...capabilities,
        chat_completions: chat,
      },
    };
  });
  requireValue(
    available_models.length > 0,
    "Configure at least one verified model.",
  );
  return {
    language_models: {
      openai_compatible: {
        lmm: { api_url: `http://127.0.0.1:${port}/v1`, available_models },
      },
    },
  };
}

/** Compatibility fallback, not a claim about upstream limits or capabilities. */
export function defaultConfiguration(models: Model[]): ModelConfiguration[] {
  return models
    .filter(
      (model) =>
        model.apis.includes("openai-completions") ||
        model.apis.includes("openai-responses"),
    )
    .map((model) => ({
      id: model.id,
      max_tokens: 32768,
      max_output_tokens: 4096,
      capabilities: { tools: false, images: false, parallel_tool_calls: false },
    }));
}
