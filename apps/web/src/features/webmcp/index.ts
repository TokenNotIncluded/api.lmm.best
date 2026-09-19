/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { getPricing } from '@/features/pricing/api'
import { useAuthStore } from '@/stores/auth-store'

type ToolExecuteOptions = { signal: AbortSignal }
type ModelContextTool = {
  name: string
  title?: string
  description: string
  inputSchema: Record<string, unknown>
  annotations?: { readOnlyHint?: boolean; consequential?: boolean }
  execute: (
    input: Record<string, unknown>,
    options: ToolExecuteOptions
  ) => Promise<unknown>
}
type ModelContext = {
  registerTool: (
    tool: ModelContextTool,
    options?: { signal?: AbortSignal }
  ) => void | Promise<void>
}
type WebMcpRouter = {
  navigate: (options: { to: string }) => Promise<unknown>
  subscribe: (event: 'onResolved', listener: () => void) => () => void
}

declare global {
  interface Document {
    modelContext?: ModelContext
  }
}

const NAVIGABLE_PATHS = {
  '/': '/',
  '/pricing': '/pricing',
  '/guide': '/guide',
  '/dashboard/overview': '/dashboard/overview',
  '/wallet': '/wallet',
  '/temporary-activations': '/temporary-activations',
} as const

function ensureObject(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new TypeError('Tool input must be an object')
  }
  return value as Record<string, unknown>
}

function ensureNotAborted(signal: AbortSignal) {
  if (signal.aborted) {
    throw signal.reason ?? new DOMException('Aborted', 'AbortError')
  }
}

function safeAccountStatus() {
  const user = useAuthStore.getState().auth.user
  if (!user) return { authenticated: false }
  return {
    authenticated: true,
    role: user.role,
    trust_level: user.trust_level_info?.level ?? null,
    developer_access_granted: user.developer_access_granted === true,
    onboarding_stage: user.onboarding?.stage ?? user.onboarding_stage ?? null,
  }
}

function toolsFor(router: WebMcpRouter): ModelContextTool[] {
  return [
    {
      name: 'lmm_site_info',
      title: 'LMM site information',
      description:
        'Return public LMM site information and the current page path.',
      inputSchema: {
        type: 'object',
        properties: {},
        additionalProperties: false,
      },
      annotations: { readOnlyHint: true },
      execute: async (_input, options) => {
        ensureNotAborted(options.signal)
        return {
          site: 'api.lmm.best',
          current_path: window.location.pathname,
          pricing_path: '/pricing',
          guide_path: '/guide',
          pi_oauth: true,
        }
      },
    },
    {
      name: 'lmm_navigate',
      title: 'Open an LMM page',
      description:
        'Navigate the current tab to one of the listed public or account pages.',
      inputSchema: {
        type: 'object',
        properties: {
          path: { type: 'string', enum: Object.keys(NAVIGABLE_PATHS) },
        },
        required: ['path'],
        additionalProperties: false,
      },
      execute: async (rawInput, options) => {
        const input = ensureObject(rawInput)
        const path = input.path
        if (typeof path !== 'string' || !Object.hasOwn(NAVIGABLE_PATHS, path)) {
          throw new TypeError('Unknown navigation path')
        }
        ensureNotAborted(options.signal)
        await router.navigate({
          to: NAVIGABLE_PATHS[path as keyof typeof NAVIGABLE_PATHS],
        })
        return { path, navigated: true }
      },
    },
    {
      name: 'lmm_model_prices',
      title: 'Read LMM model prices',
      description:
        'Read public model pricing. An optional exact model ID filters the result.',
      inputSchema: {
        type: 'object',
        properties: { model: { type: 'string', maxLength: 128 } },
        additionalProperties: false,
      },
      annotations: { readOnlyHint: true },
      execute: async (rawInput, options) => {
        const input = ensureObject(rawInput)
        const model = input.model
        if (
          model !== undefined &&
          (typeof model !== 'string' || model.length > 128)
        ) {
          throw new TypeError('model must be a short string')
        }
        ensureNotAborted(options.signal)
        const pricing = await getPricing(options.signal)
        ensureNotAborted(options.signal)
        const models = pricing.data
          .filter((item) => !model || item.model_name === model)
          .map((item) => {
            const inputRatio = Number(item.model_ratio)
            const outputRatio = inputRatio * Number(item.completion_ratio)
            const tokenBased = item.quota_type === 0
            return {
              model: item.model_name,
              vendor: item.vendor_name ?? null,
              quota_type: item.quota_type,
              billing_unit: tokenBased
                ? 'platform_credit_per_million_tokens'
                : 'platform_credit_per_request',
              input_platform_credit_per_million_tokens: tokenBased
                ? inputRatio * 2
                : null,
              output_platform_credit_per_million_tokens: tokenBased
                ? outputRatio * 2
                : null,
              cache_ratio_multiplier: tokenBased
                ? (item.cache_ratio ?? null)
                : null,
              request_platform_credit: tokenBased
                ? null
                : (item.model_price ?? null),
              group_multipliers: item.group_ratio ?? null,
              billing_mode: item.billing_mode ?? null,
            }
          })
        return {
          pricing_basis:
            'Base platform credit before group multipliers; not a fiat checkout price.',
          count: models.length,
          models,
        }
      },
    },
    {
      name: 'lmm_account_status',
      title: 'Read current LMM account status',
      description:
        'Read the current signed-in account access summary without credentials or private profile data.',
      inputSchema: {
        type: 'object',
        properties: {},
        additionalProperties: false,
      },
      annotations: { readOnlyHint: true },
      execute: async (_input, options) => {
        ensureNotAborted(options.signal)
        return safeAccountStatus()
      },
    },
  ]
}

export function installWebMcp(router: WebMcpRouter): () => void {
  const modelContext = document.modelContext
  if (!modelContext) return () => undefined
  let controller = new AbortController()
  let disposed = false

  const register = () => {
    controller = new AbortController()
    const signal = controller.signal
    for (const tool of toolsFor(router)) {
      try {
        void Promise.resolve(modelContext.registerTool(tool, { signal })).catch(
          () => undefined
        )
      } catch {
        // Optional browser integration must never prevent the app from mounting.
      }
    }
  }
  const refresh = () => {
    controller.abort()
    if (!disposed) register()
  }
  register()
  const unsubscribeAuth = useAuthStore.subscribe(refresh)
  const unsubscribeRouter = router.subscribe('onResolved', refresh)
  return () => {
    disposed = true
    unsubscribeAuth()
    unsubscribeRouter()
    controller.abort()
  }
}
