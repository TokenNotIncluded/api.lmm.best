/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

import type { AxiosAdapter, AxiosResponse } from 'axios'
import { Window } from 'happy-dom'
import type { Root } from 'react-dom/client'

import type { AuthBundle } from '@/stores/auth-store'

const domWindow = new Window({
  url: 'https://console.example.test/settings/security',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'Node',
  'Element',
  'Event',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const i18next = (await import('i18next')).default
const { initReactI18next } = await import('react-i18next')

await i18next.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Automatic review history cleanup completed':
          'Automatic review history cleanup completed',
        Cancel: 'Cancel',
        'Clean up automatic review history?':
          'Clean up automatic review history?',
        'Clean up review history': 'Clean up review history',
        'Confirm Cleanup': 'Confirm Cleanup',
        'Failed to clean up automatic review history':
          'Failed to clean up automatic review history',
        'No completed automatic review runs are eligible for cleanup.':
          'No completed automatic review runs are eligible for cleanup.',
        'Security verification': 'Security verification',
        'This action cannot be undone.': 'This action cannot be undone.',
        'This will permanently delete {{count}} completed or failed automatic review runs while keeping the latest {{keep}}. Active runs and security audit evidence will not be deleted.':
          'This will permanently delete {{count}} completed or failed automatic review runs while keeping the latest {{keep}}. Active runs and security audit evidence will not be deleted.',
      },
    },
  },
})

const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { AssistantReviewCleanup } = await import('./assistant-review-cleanup')

const originalAdapter = api.defaults.adapter
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

function response(
  config: Parameters<AxiosAdapter>[0],
  data: unknown
): AxiosResponse {
  return {
    config,
    data,
    headers: {},
    status: 200,
    statusText: 'OK',
  }
}

function authBundle(): AuthBundle {
  const expiresAt = Math.floor(Date.now() / 1000) + 600
  return {
    access_token: 'assistant-review-cleanup-test-token',
    token_type: 'Bearer',
    access_expires_at: expiresAt,
    user: {
      id: 7,
      username: 'cleanup-admin',
      role: 10,
    },
    session: {
      sid: 'assistant-review-cleanup-session',
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'test',
      created_at: 1,
      last_active_at: 1,
      expires_at: expiresAt + 600,
    },
  }
}

async function renderCleanup(): Promise<{
  root: Root
  container: HTMLDivElement
}> {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  await act(async () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <AssistantReviewCleanup onCleaned={() => undefined} />
      </QueryClientProvider>
    )
  })
  return { root, container }
}

function button(label: string): HTMLButtonElement {
  const result = [...document.querySelectorAll('button')].find(
    (candidate) => candidate.textContent?.trim() === label
  )
  if (!(result instanceof HTMLButtonElement)) {
    throw new Error(`button not found: ${label}`)
  }
  return result
}

afterEach(() => {
  api.defaults.adapter = originalAdapter
  useAuthStore.getState().auth.reset('idle')
  document.body.innerHTML = ''
})

after(() => domWindow.close())

describe('AssistantReviewCleanup', () => {
  test('does not request a preview on mount', async () => {
    let requests = 0
    api.defaults.adapter = async (config) => {
      requests += 1
      return response(config, { success: true, data: {} })
    }
    const { root, container } = await renderCleanup()

    try {
      await act(async () => undefined)
      assert.equal(requests, 0)
    } finally {
      await act(async () => root.unmount())
      container.remove()
    }
  })

  test('does not open confirmation when no history is eligible', async () => {
    let requests = 0
    api.defaults.adapter = async (config) => {
      requests += 1
      return response(config, {
        success: true,
        data: {
          task_type: 'assistant_review',
          keep: 30,
          eligible_count: 0,
          deleted_count: 0,
        },
      })
    }
    const { root, container } = await renderCleanup()

    try {
      await act(async () => button('Clean up review history').click())
      assert.equal(requests, 1)
      assert.equal(document.querySelector('[role="alertdialog"]'), null)
    } finally {
      await act(async () => root.unmount())
      container.remove()
    }
  })

  test('requires secure verification before deletion', async () => {
    useAuthStore.getState().auth.setBundle(authBundle())
    const requested: string[] = []
    api.defaults.adapter = async (config) => {
      requested.push(`${config.method}:${config.url}`)
      if (config.url?.endsWith('/cleanup-preview')) {
        return response(config, {
          success: true,
          data: {
            task_type: 'assistant_review',
            keep: 30,
            eligible_count: 5,
            deleted_count: 0,
          },
        })
      }
      if (config.url === '/api/user/self') {
        return response(config, {
          success: true,
          data: { email: 'admin@example.com' },
        })
      }
      return response(config, {
        success: true,
        data: { enabled: false },
      })
    }
    const { root, container } = await renderCleanup()

    try {
      await act(async () => button('Clean up review history').click())
      assert.ok(document.querySelector('[role="alertdialog"]'))
      await act(async () => button('Confirm Cleanup').click())

      assert.match(document.body.textContent ?? '', /Security verification/)
      assert.equal(
        requested.some((entry) => entry.startsWith('delete:')),
        false
      )
      assert.deepEqual(requested.sort(), [
        'get:/api/security/admin/review-runs/cleanup-preview',
        'get:/api/user/2fa/status',
        'get:/api/user/passkey',
        'get:/api/user/self',
      ])
    } finally {
      await act(async () => root.unmount())
      container.remove()
    }
  })
})
