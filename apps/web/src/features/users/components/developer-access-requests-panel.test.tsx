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

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'https://console.example.test/' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLTextAreaElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'PointerEvent',
  'FocusEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
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
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { DeveloperAccessRequestsPanel } =
  await import('./developer-access-requests-panel')

const originalGet = api.get
const originalPost = api.post
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

async function flushEffects() {
  await new Promise((resolve) => setTimeout(resolve, 25))
}

async function waitForCondition(
  condition: () => boolean,
  failureMessage: string
) {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    if (condition()) return
    await flushEffects()
  }
  throw new Error(`${failureMessage}: ${document.body.textContent}`)
}

async function setTextareaValue(textarea: HTMLTextAreaElement, value: string) {
  const setValue = Object.getOwnPropertyDescriptor(
    HTMLTextAreaElement.prototype,
    'value'
  )?.set
  assert.ok(setValue)
  await act(async () => {
    setValue.call(textarea, value)
    textarea.dispatchEvent(new Event('input', { bubbles: true }))
    await flushEffects()
  })
}

async function renderPanel() {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  await act(async () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <DeveloperAccessRequestsPanel />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flushEffects()
  })
  return { container, root, queryClient }
}

async function unmountPanel(panel: Awaited<ReturnType<typeof renderPanel>>) {
  await act(async () => panel.root.unmount())
  panel.queryClient.clear()
  panel.container.remove()
}

afterEach(() => {
  api.get = originalGet
  api.post = originalPost
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('DeveloperAccessRequestsPanel', () => {
  test('requires an administrator reply before reviewing each user recommendation letter', async () => {
    api.get = (async (url: string) => {
      assert.equal(url, '/api/developer-access/requests')
      return {
        data: {
          success: true,
          data: [
            {
              id: 17,
              user_id: 8,
              status: 'pending',
              reason: 'I will use Claude Code for private development.',
              source: 'assistant_recommendation',
              ai_recommendation:
                'Recommend L1 because the user supplied a concrete use case.',
              admin_user_id: 0,
              admin_note: '',
              created_at: 1_786_400_000,
              reviewed_at: 0,
              username: 'test-user',
              email: 'test@example.test',
            },
            {
              id: 18,
              user_id: 9,
              status: 'pending',
              reason: 'I want to connect a local test client.',
              source: 'assistant_request',
              ai_recommendation: '',
              admin_user_id: 0,
              admin_note: '',
              created_at: 1_786_400_001,
              reviewed_at: 0,
              username: 'manual-user',
              email: 'manual@example.test',
            },
            {
              id: 19,
              user_id: 10,
              status: 'pending',
              reason: 'Obsolete legacy request.',
              source: 'legacy',
              ai_recommendation: '',
              admin_user_id: 0,
              admin_note: '',
              created_at: 1_786_400_002,
              reviewed_at: 0,
              username: 'legacy-user',
              email: 'legacy@example.test',
            },
          ],
        },
      }
    }) as typeof api.get

    let reviewRequest: { url: string; data: unknown } | undefined
    api.post = (async (url: string, data: unknown) => {
      reviewRequest = { url, data }
      return { data: { success: true, data: {} } }
    }) as typeof api.post

    const panel = await renderPanel()

    try {
      await waitForCondition(
        () => document.body.textContent?.includes('test-user') === true,
        'AI recommendation did not render'
      )
      assert.match(document.body.textContent ?? '', /L1 access requests/)
      assert.match(
        document.body.textContent ?? '',
        /Recommend L1 because the user supplied a concrete use case\./
      )
      assert.match(document.body.textContent ?? '', /manual-user/)
      assert.match(document.body.textContent ?? '', /Direct request/)
      assert.doesNotMatch(document.body.textContent ?? '', /legacy-user/)
      assert.doesNotMatch(document.body.textContent ?? '', /Legacy request/)
      assert.match(
        document.body.textContent ?? '',
        /I want to connect a local test client\./
      )

      const textarea = document.querySelector('textarea')
      assert.ok(textarea)
      const approve = [...document.querySelectorAll('button')].find((button) =>
        button.textContent?.includes('Approve and unlock L1')
      )
      const reject = [...document.querySelectorAll('button')].find((button) =>
        button.textContent?.includes('Reject')
      )
      assert.ok(approve)
      assert.ok(reject)
      assert.equal(approve.disabled, true)
      assert.equal(reject.disabled, true)

      await setTextareaValue(textarea, 'O')
      assert.equal(approve.disabled, true)
      await setTextareaValue(textarea, ' Approved ')
      assert.equal(approve.disabled, false)
      assert.equal(reject.disabled, false)

      await act(async () => {
        approve.click()
        await flushEffects()
      })
      await waitForCondition(
        () => reviewRequest !== undefined,
        'Approval was not submitted'
      )
      assert.deepEqual(reviewRequest, {
        url: '/api/developer-access/requests/17/approve',
        data: { note: 'Approved' },
      })
    } finally {
      await unmountPanel(panel)
    }
  })

  test('removes requests approved by the background reviewer on refresh', async () => {
    let pending = true
    api.get = (async () => ({
      data: {
        success: true,
        data: pending
          ? [
              {
                id: 27,
                user_id: 18,
                status: 'pending',
                reason: 'I am building an API client.',
                source: 'assistant_recommendation',
                ai_recommendation: 'Recommend L1 for an API client.',
                admin_user_id: 0,
                admin_note: '',
                created_at: 1,
                reviewed_at: 0,
                username: 'auto-reviewed-user',
                email: 'auto@example.test',
              },
            ]
          : [],
      },
    })) as typeof api.get

    const panel = await renderPanel()
    try {
      await waitForCondition(
        () =>
          document.body.textContent?.includes('auto-reviewed-user') === true,
        'Pending request did not render'
      )
      const query = panel.queryClient.getQueryCache().find({
        queryKey: ['developer-access-requests', 'pending'],
      })
      assert.ok(query)
      const refetchInterval = (query.options as { refetchInterval?: unknown })
        .refetchInterval
      assert.equal(typeof refetchInterval, 'function')
      assert.equal(
        (refetchInterval as (value: typeof query) => number | false)(query),
        5_000
      )

      pending = false
      await act(async () => {
        await panel.queryClient.refetchQueries({
          queryKey: ['developer-access-requests', 'pending'],
        })
        await flushEffects()
      })
      assert.doesNotMatch(document.body.textContent ?? '', /auto-reviewed-user/)
      assert.match(
        document.body.textContent ?? '',
        /No pending unlock requests/
      )
    } finally {
      await unmountPanel(panel)
    }
  })

  test('reloads the pending queue after an approval conflict', async () => {
    let getCalls = 0
    api.get = (async () => {
      getCalls += 1
      return {
        data: {
          success: true,
          data:
            getCalls === 1
              ? [
                  {
                    id: 37,
                    user_id: 28,
                    status: 'pending',
                    reason: 'I am building a model client.',
                    source: 'assistant_request',
                    ai_recommendation: '',
                    admin_user_id: 0,
                    admin_note: '',
                    created_at: 1,
                    reviewed_at: 0,
                    username: 'already-reviewed-user',
                    email: 'reviewed@example.test',
                  },
                ]
              : [],
        },
      }
    }) as typeof api.get
    api.post = (async () => {
      const error = new Error('request was already reviewed')
      Object.assign(error, { response: { status: 409 } })
      throw error
    }) as typeof api.post

    const panel = await renderPanel()
    try {
      await waitForCondition(
        () =>
          document.body.textContent?.includes('already-reviewed-user') === true,
        'Pending request did not render'
      )
      const textarea = document.querySelector('textarea')
      assert.ok(textarea)
      await setTextareaValue(textarea, 'Reviewed elsewhere')
      const approve = [...document.querySelectorAll('button')].find((button) =>
        button.textContent?.includes('Approve and unlock L1')
      )
      assert.ok(approve)
      await act(async () => {
        approve.click()
        await flushEffects()
      })
      await waitForCondition(
        () =>
          document.body.textContent?.includes('already-reviewed-user') ===
          false,
        'Approval conflict did not refresh the queue'
      )
      assert.ok(getCalls >= 2)
    } finally {
      await unmountPanel(panel)
    }
  })
})
