/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { installWebMcp, listWebMcpTools } from './index'

test('registers safe read/navigation tools and aborts them on cleanup', async () => {
  const registered: { name: string; signal?: AbortSignal }[] = []
  const listeners: (() => void)[] = []
  const previousDocument = globalThis.document
  Object.defineProperty(globalThis, 'document', {
    configurable: true,
    value: {
      modelContext: {
        registerTool: async (
          tool: { name: string },
          options?: { signal?: AbortSignal }
        ) => {
          registered.push({ name: tool.name, signal: options?.signal })
        },
      },
    },
  })
  const router = {
    navigate: async () => undefined,
    subscribe: (_event: 'onResolved', listener: () => void) => {
      listeners.push(listener)
      return () => undefined
    },
  }

  try {
    const cleanup = installWebMcp(router)
    await new Promise((resolve) => setTimeout(resolve, 0))
    const names = registered.map((tool) => tool.name)
    for (const name of [
      'lmm_signal_state',
      'lmm_signal_submit',
      'lmm_site_info',
      'lmm_site_map',
      'lmm_page_outline',
      'lmm_navigate',
      'lmm_model_prices',
      'lmm_public_scripts',
      'lmm_source_repositories',
      'lmm_account_status',
    ]) {
      assert.ok(names.includes(name), `${name} is registered`)
    }
    assert.equal(new Set(names).size, names.length)
    assert.equal(listeners.length, 1)
    assert.ok(registered.every((tool) => tool.signal && !tool.signal.aborted))
    cleanup()
    assert.ok(registered.every((tool) => tool.signal?.aborted))
  } finally {
    if (previousDocument) {
      Object.defineProperty(globalThis, 'document', {
        configurable: true,
        value: previousDocument,
      })
    } else {
      delete (globalThis as { document?: unknown }).document
    }
  }
})

test('rejects unknown navigation input before changing route', async () => {
  const previousDocument = globalThis.document
  let execute!: (
    input: Record<string, unknown>,
    options: { signal: AbortSignal }
  ) => Promise<unknown>
  Object.defineProperty(globalThis, 'document', {
    configurable: true,
    value: {
      modelContext: {
        registerTool: async (tool: {
          name: string
          execute: typeof execute
        }) => {
          if (tool.name === 'lmm_navigate') execute = tool.execute
        },
      },
    },
  })
  let navigated = false
  const router = {
    navigate: async () => {
      navigated = true
    },
    subscribe: () => () => undefined,
  }
  try {
    const cleanup = installWebMcp(router)
    await new Promise((resolve) => setTimeout(resolve, 0))
    await assert.rejects(
      execute({ path: '/admin' }, { signal: new AbortController().signal }),
      /Unknown navigation path/
    )
    await assert.rejects(
      execute({ path: '__proto__' }, { signal: new AbortController().signal }),
      /Unknown navigation path/
    )
    assert.equal(navigated, false)
    cleanup()
  } finally {
    if (previousDocument) {
      Object.defineProperty(globalThis, 'document', {
        configurable: true,
        value: previousDocument,
      })
    } else {
      delete (globalThis as { document?: unknown }).document
    }
  }
})

test('does nothing when the browser has no WebMCP support', () => {
  const previousDocument = globalThis.document
  Object.defineProperty(globalThis, 'document', {
    configurable: true,
    value: {},
  })
  try {
    assert.doesNotThrow(() => installWebMcp({} as never))
  } finally {
    if (previousDocument) {
      Object.defineProperty(globalThis, 'document', {
        configurable: true,
        value: previousDocument,
      })
    } else {
      delete (globalThis as { document?: unknown }).document
    }
  }
})

for (const mode of ['sync', 'throw', 'reject'] as const) {
  test(`optional WebMCP ${mode} registration cannot prevent app startup`, async () => {
    const previous = Object.getOwnPropertyDescriptor(globalThis, 'document')
    let calls = 0
    Object.defineProperty(globalThis, 'document', {
      configurable: true,
      value: {
        modelContext: {
          registerTool: () => {
            calls += 1
            if (mode === 'throw') throw new Error('registration unavailable')
            if (mode === 'reject') return Promise.reject(new Error('rejected'))
          },
        },
      },
    })
    try {
      const cleanup = installWebMcp({
        navigate: async () => undefined,
        subscribe: () => () => undefined,
      })
      await new Promise((resolve) => setTimeout(resolve, 0))
      assert.equal(calls, listWebMcpTools().length)
      cleanup()
    } finally {
      if (previous) Object.defineProperty(globalThis, 'document', previous)
      else delete (globalThis as { document?: unknown }).document
    }
  })
}

test('blocked browser capability getter cannot break startup', () => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'document')
  Object.defineProperty(globalThis, 'document', {
    configurable: true,
    value: Object.defineProperty({}, 'modelContext', {
      get() {
        throw new Error('Capability blocked')
      },
    }),
  })
  try {
    assert.doesNotThrow(() => installWebMcp({} as never))
  } finally {
    if (previous) Object.defineProperty(globalThis, 'document', previous)
    else delete (globalThis as { document?: unknown }).document
  }
})
