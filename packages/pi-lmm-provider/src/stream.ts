import {
  createAssistantMessageEventStream,
  type Api, type AssistantMessage, type AssistantMessageEvent, type Context, type Model,
  type ProviderHeaders, type ProviderStreamOptions, type ProviderStreams, type StreamOptions,
} from '@earendil-works/pi-ai';
import { anthropicMessagesApi, openAICompletionsApi, openAIResponsesApi } from '@earendil-works/pi-ai/compat';
import type { Admission } from './catalog.ts';
import type { LmmHttp } from './http.ts';
import { PROVIDER_ID, LmmError, accessToken, object, requireValue, type LmmApi } from './protocol.ts';

const streams: Record<LmmApi, ProviderStreams> = {
  'openai-completions': openAICompletionsApi(),
  'openai-responses': openAIResponsesApi(),
  'anthropic-messages': anthropicMessagesApi(),
};

export function bearerFromHeaders(headers: ProviderHeaders | undefined): string {
  const values = Object.entries(headers ?? {}).filter(([key]) => key.toLowerCase() === 'authorization').map(([, value]) => value);
  requireValue(values.length === 1 && typeof values[0] === 'string' && values[0].startsWith('Bearer '), 'LMM model requests require native OAuth authorization.');
  return accessToken(values[0].slice(7));
}

function errorMessage(model: Model<Api>, aborted: boolean): AssistantMessage {
  return {
    role: 'assistant', content: [], api: model.api, provider: PROVIDER_ID, model: model.id,
    usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0,
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } },
    stopReason: aborted ? 'aborted' : 'error',
    errorMessage: aborted ? 'LMM request cancelled.' : 'LMM model request failed. Check authorization, catalog eligibility, and server availability.',
    timestamp: Date.now(),
  };
}

function rebind(message: AssistantMessage, model: Model<Api>): AssistantMessage {
  const result = { ...message, provider: PROVIDER_ID, model: model.id, api: model.api };
  // Upstream errors can echo headers / credentials. Never print raw provider errors.
  if (result.stopReason === 'error' || result.stopReason === 'aborted') {
    result.errorMessage = result.stopReason === 'aborted' ? 'LMM request cancelled.' : 'LMM model request failed. Check authorization and server availability.';
  }
  return result;
}

function rebindEvent(event: AssistantMessageEvent, model: Model<Api>): AssistantMessageEvent {
  if (event.type === 'done') return { ...event, message: rebind(event.message, model) };
  if (event.type === 'error') return { ...event, error: rebind(event.error, model) };
  if ('partial' in event) return { ...event, partial: rebind(event.partial, model) };
  return event;
}

export interface RelayHooks {
  lookup(modelId: string, access: string): Admission | undefined;
  onFinish(access: string): Promise<void>;
}

export function createRelay(http: LmmHttp, hooks: RelayHooks): ProviderStreams {
  const run = (simple: boolean, selected: Model<Api>, context: Context, options: StreamOptions = {}) => {
    const output = createAssistantMessageEventStream();
    void (async () => {
      let access: string | undefined;
      let sent = false;
      try {
        requireValue(options.apiKey === undefined, 'An API key must not be combined with LMM OAuth.');
        access = bearerFromHeaders(options.headers);
        const admission = hooks.lookup(selected.id, access);
        requireValue(admission?.model, 'This LMM model is no longer admitted by the current account catalog.');
        const { entry, model } = admission;
        requireValue(model && selected.provider === PROVIDER_ID && selected.api === model.api);
        if (options.maxTokens !== undefined) {
          requireValue(Number.isSafeInteger(options.maxTokens) && options.maxTokens > 0 && options.maxTokens <= model.maxTokens,
            'Requested output exceeds the verified LMM model capability.');
        }
        const api = model.api;
        const wire: Model<Api> = { ...structuredClone(model), id: entry.upstream_model, headers: undefined };
        const path = api === 'anthropic-messages' ? '/v1/messages' : api === 'openai-responses' ? '/v1/responses' : '/v1/chat/completions';
        const headers: ProviderHeaders = {
          authorization: `Bearer ${access}`, 'X-LMM-Group': entry.group_id,
          // Pi's Anthropic adapter passes apiKey:null when a Bearer header is used.
          // Null additionally deletes the SDK default instead of sending a conflicting key.
          'x-api-key': null,
        };
        const authorizedAccess = access;
        const guardedFetch: typeof fetch = async (input, init) => {
          const target = input instanceof Request ? input.url : String(input);
          const endpoint = `${http.issuer}${path}`;
          // The Anthropic SDK adds this transport flag to its beta endpoint.
          // LMM carries beta features in headers and rejects relay query strings.
          const sdkBeta = api === 'anthropic-messages' && target === `${endpoint}?beta=true`;
          requireValue(target === endpoint || sdkBeta, 'LMM relay destination does not match its advertised protocol.');
          const actual = new Headers(init?.headers ?? (input instanceof Request ? input.headers : undefined));
          for (const name of ['x-api-key', 'api-key', 'proxy-authorization', 'cookie']) actual.delete(name);
          actual.set('authorization', `Bearer ${authorizedAccess}`);
          actual.set('x-lmm-group', entry.group_id);
          sent = true;
          const destination = sdkBeta ? (input instanceof Request ? new Request(endpoint, input) : endpoint) : input;
          return http.fetch(destination, { ...init, headers: actual, redirect: 'error', credentials: 'omit' });
        };
        const originalOnPayload = options.onPayload;
        const wireOptions: ProviderStreamOptions = {
          ...options, apiKey: undefined, headers, env: {}, fetch: guardedFetch,
          maxTokens: options.maxTokens ?? model.maxTokens,
          // Do not auto-retry a billable request; host retries are a separate user-visible decision.
          maxRetries: 0,
          onPayload: async (payload, payloadModel) => {
            const replacement = await originalOnPayload?.(payload, payloadModel);
            return { ...object(replacement ?? payload), model: entry.upstream_model, stream: true };
          },
        };
        const wireContext: Context = {
          ...context,
          messages: context.messages.map((message) => message.role === 'assistant' && message.provider === PROVIDER_ID && message.model === selected.id
            ? { ...message, model: entry.upstream_model } : message),
        };
        const source = simple ? streams[api].streamSimple(wire, wireContext, wireOptions) : streams[api].stream(wire, wireContext, wireOptions);
        for await (const event of source) output.push(rebindEvent(event, selected));
        output.end(rebind(await source.result(), selected));
      } catch (error) {
        const message = errorMessage(selected, options.signal?.aborted === true);
        if (error instanceof LmmError) message.errorMessage = error.message;
        output.push({ type: 'error', reason: options.signal?.aborted ? 'aborted' : 'error', error: message });
        output.end(message);
      } finally {
        if (sent && access) await hooks.onFinish(access).catch(() => {});
      }
    })();
    return output;
  };
  return {
    stream: (model, context, options) => run(false, model, context, options),
    streamSimple: (model, context, options) => run(true, model, context, options),
  };
}
