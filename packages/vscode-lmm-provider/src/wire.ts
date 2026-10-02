import { assert, record, RESOURCE, LmmError } from "./protocol";
export interface CatalogModel {
  id: string;
  group_id: string;
  group: string;
  upstream_model: string;
  name: string;
}
export function catalogModels(body: unknown, scope: string): CatalogModel[] {
  const b = record(body);
  assert(
    b.schema_version === 1 &&
      b.resource === RESOURCE &&
      Array.isArray(b.models) &&
      b.models.length <= 20000,
  );
  const scopes = new Set(scope.split(" ")),
    seen = new Set<string>();
  return b.models
    .filter(
      (m: any) =>
        Array.isArray(m.apis) && m.apis.includes("openai-completions"),
    )
    .map((value: unknown) => {
      const m = record(value);
      assert(
        ["id", "group_id", "group", "upstream_model", "name"].every(
          (k) =>
            typeof m[k] === "string" &&
            m[k].length > 0 &&
            m[k].length <= 4096 &&
            !/[\p{Cc}\p{Cf}]/u.test(m[k]),
        ),
      );
      assert(
        m.group_id === Buffer.from(m.group).toString("base64url") &&
          m.id ===
            `lmm:${m.group_id}:${Buffer.from(m.upstream_model).toString("base64url")}`,
      );
      assert(
        scopes.has(`group:${m.group_id}`) && !seen.has(m.id),
        "LMM catalog returned a model outside the granted groups.",
      );
      seen.add(m.id);
      return {
        id: m.id,
        group_id: m.group_id,
        group: m.group,
        upstream_model: m.upstream_model,
        name: m.name,
      };
    });
}
export type StreamPart =
  | { type: "text"; text: string }
  | { type: "tool"; id: string; name: string; input: Record<string, unknown> };
/** SSE framing tolerates arbitrary UTF-8 and CR/LF boundaries. */
export async function streamCompletion(
  response: Response,
  emit: (part: StreamPart) => void,
  signal: AbortSignal,
): Promise<void> {
  if (!response.ok) {
    await response.body?.cancel();
    throw new LmmError(
      "http",
      `LMM model request failed (HTTP ${response.status}).`,
    );
  }
  assert(
    response.headers.get("content-type")?.includes("text/event-stream") &&
      response.body,
    "LMM model did not return an event stream.",
  );
  const reader = response.body.getReader(),
    decoder = new TextDecoder();
  let buffer = "",
    data: string[] = [],
    eventLength = 0,
    done = false,
    finished = false;
  const calls = new Map<
    number,
    { id: string; name: string; arguments: string }
  >();
  const event = () => {
    if (!data.length) return;
    const raw = data.join("\n");
    data = [];
    eventLength = 0;
    if (raw === "[DONE]") {
      done = true;
      return;
    }
    const b = record(JSON.parse(raw));
    assert(!b.error, "LMM model returned a stream error.");
    if (!Array.isArray(b.choices) || b.choices.length === 0) return;
    const choice = record(b.choices[0]);
    if (choice.finish_reason !== null && choice.finish_reason !== undefined) {
      assert(
        ["stop", "length", "tool_calls", "function_call"].includes(
          choice.finish_reason,
        ),
        "LMM model could not complete this request.",
      );
      finished = true;
    }
    const delta = record(choice.delta ?? {});
    if (delta.content !== null && delta.content !== undefined) {
      assert(typeof delta.content === "string");
      emit({ type: "text", text: delta.content });
    }
    if (delta.tool_calls) {
      assert(Array.isArray(delta.tool_calls));
      for (const value of delta.tool_calls) {
        const d = record(value);
        assert(Number.isSafeInteger(d.index) && d.index >= 0 && d.index < 128);
        const c = calls.get(d.index) ?? { id: "", name: "", arguments: "" };
        if (d.id !== undefined) {
          assert(typeof d.id === "string");
          c.id += d.id;
        }
        if (d.function?.name !== undefined) {
          assert(typeof d.function.name === "string");
          c.name += d.function.name;
        }
        if (d.function?.arguments !== undefined) {
          assert(typeof d.function.arguments === "string");
          c.arguments += d.function.arguments;
        }
        assert(
          c.arguments.length <= 1000000 &&
            c.id.length <= 4096 &&
            c.name.length <= 256,
          "LMM tool response exceeds its size limit.",
        );
        calls.set(d.index, c);
      }
    }
  };
  const line = (s: string) => {
    if (!s) event();
    else if (s.startsWith("data:")) {
      eventLength += s.length;
      assert(eventLength <= 2000000, "LMM stream event is too large.");
      data.push(s.slice(5).replace(/^ /, ""));
    }
  };
  try {
    while (!done) {
      signal.throwIfAborted();
      const chunk = await reader.read();
      if (chunk.done) {
        buffer += decoder.decode();
        break;
      }
      buffer += decoder.decode(chunk.value, { stream: true });
      assert(buffer.length <= 2000000, "LMM stream event is too large.");
      let match: RegExpExecArray | null;
      while ((match = /\r\n|\r(?!$)|\n/.exec(buffer))) {
        line(buffer.slice(0, match.index));
        buffer = buffer.slice(match.index + match[0].length);
        if (done) break;
      }
    }
    if (!done) {
      if (buffer.endsWith("\r")) buffer = buffer.slice(0, -1);
      if (buffer) line(buffer);
      event();
    }
    assert(
      done && finished,
      "LMM stream ended before completion; the request was not retried.",
    );
    const ids = new Set<string>();
    for (const c of calls.values()) {
      assert(c.id && c.name && !ids.has(c.id));
      ids.add(c.id);
      emit({
        type: "tool",
        id: c.id,
        name: c.name,
        input: record(JSON.parse(c.arguments || "{}")),
      });
    }
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}
