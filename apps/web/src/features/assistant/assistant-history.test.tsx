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
  'history',
  'location',
  'HTMLElement',
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'PointerEvent',
  'FocusEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'scrollTo',
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
const { useAuthStore } = await import('@/stores/auth-store')
const { AssistantHistory, AssistantHistoryConversation } =
  await import('./assistant-history')

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

const activeConversation = {
  id: 1,
  title: 'Active support',
  last_message_preview: 'active-support api_key=sk-history-secret-123456',
  created_at: 1_786_400_000,
  updated_at: 1_786_400_001,
  archived_at: 0,
  owner: 'self' as const,
  privacy_notice: 'Conversations are not private.',
}

const lowerAccessConversation = {
  ...activeConversation,
  id: 2,
  title: 'Lower-access support',
  last_message_preview: 'lower-access-support',
  owner: 'lower_level_user' as const,
}

const archivedConversation = {
  ...activeConversation,
  id: 3,
  last_message_preview: 'archived-support',
  archived_at: 1_786_400_100,
}

async function flushEffects() {
  await new Promise((resolve) => setTimeout(resolve, 25))
}

function setUser(role: number, trustLevel = 0) {
  useAuthStore.getState().auth.setUser({
    id: 99,
    username: 'history-tester',
    role,
    ...(trustLevel > 0
      ? {
          trust_level_info: {
            level: trustLevel,
            automatic_level: trustLevel,
            override_level: null,
            paid_amount: 0,
            discount_ratio: 1,
            discount_percent: 0,
            inactivity_decay_steps: 0,
            decay_period_days: 0,
            overridden: false,
          },
        }
      : {}),
  })
}

async function renderHistory(presentation: 'cards' | 'rows' = 'cards') {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  await act(async () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <AssistantHistory
            active
            presentation={presentation}
            onOpenConversation={() => {}}
          />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flushEffects()
  })
  await act(flushEffects)
  return { container, queryClient, root }
}

async function renderHistoryConversation() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  await act(async () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <AssistantHistoryConversation conversation={activeConversation} />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flushEffects()
  })
  await act(flushEffects)
  return { container, queryClient, root }
}

function findButton(text: string): HTMLButtonElement {
  const button = [
    ...document.querySelectorAll<HTMLButtonElement>('button'),
  ].find((candidate) => candidate.textContent?.includes(text))
  assert.ok(button, `Could not find button containing ${text}`)
  return button
}

async function unmount(rendered: Awaited<ReturnType<typeof renderHistory>>) {
  await act(async () => rendered.root.unmount())
  rendered.queryClient.clear()
  rendered.container.remove()
}

afterEach(() => {
  api.get = originalGet
  api.post = originalPost
  useAuthStore.getState().auth.reset('complete')
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('AssistantHistory archive controls', () => {
  test('uses whitespace and separators instead of bordered cards in row presentation', async () => {
    api.get = (async () => ({
      data: {
        success: true,
        data: {
          conversations: [activeConversation, lowerAccessConversation],
        },
      },
    })) as typeof api.get

    const rendered = await renderHistory('rows')
    try {
      const list = rendered.container.querySelector<HTMLElement>(
        '[data-testid="assistant-history-list"]'
      )
      assert.ok(list)
      assert.equal(list.dataset.presentation, 'rows')
      assert.equal(
        list.querySelectorAll('[data-testid="assistant-history-item"]').length,
        2
      )
      assert.equal(list.querySelectorAll('[data-slot="separator"]').length, 1)
      for (const item of list.querySelectorAll<HTMLElement>(
        '[data-testid="assistant-history-item"]'
      )) {
        assert.match(item.className, /py-4/)
        assert.doesNotMatch(item.className, /rounded-lg/)
        assert.doesNotMatch(item.className, /border/)
        assert.equal(
          item
            .querySelector<HTMLButtonElement>('button')
            ?.className.includes('border-border'),
          false
        )
      }
    } finally {
      await unmount(rendered)
    }
  })

  test('loads active conversations by default and shows archived conversations through the filter', async () => {
    const calls: Array<{ archived: boolean }> = []
    api.get = (async (_url: string, config: unknown) => {
      const archived =
        (config as { params?: { archived?: boolean } } | undefined)?.params
          ?.archived === true
      calls.push({ archived })
      return {
        data: {
          success: true,
          data: {
            conversations: archived
              ? [archivedConversation]
              : [activeConversation, lowerAccessConversation],
          },
        },
      }
    }) as typeof api.get

    const rendered = await renderHistory()
    try {
      assert.match(rendered.container.textContent ?? '', /active-support/)
      assert.match(rendered.container.textContent ?? '', /lower-access-support/)
      assert.doesNotMatch(
        rendered.container.textContent ?? '',
        /archived-support/
      )
      assert.doesNotMatch(
        rendered.container.textContent ?? '',
        /sk-history-secret-123456/
      )
      assert.deepEqual(
        [
          ...document.querySelectorAll<HTMLButtonElement>(
            '[data-testid="assistant-history-list"] button'
          ),
        ]
          .filter((button) => button.textContent?.trim() === 'View')
          .map((button) => button.getAttribute('aria-label')),
        ['View Active support', 'View Lower-access support']
      )
      assert.equal(
        document.querySelectorAll('button[aria-label="Archive conversation"]')
          .length,
        1
      )
      assert.equal(
        document.querySelectorAll('button[aria-label="Restore conversation"]')
          .length,
        0
      )
      assert.equal(calls.length, 1)
      assert.equal(calls[0]?.archived, false)

      await act(async () => {
        findButton('Archived conversations').click()
        await flushEffects()
      })
      await act(flushEffects)

      assert.match(rendered.container.textContent ?? '', /archived-support/)
      assert.doesNotMatch(
        rendered.container.textContent ?? '',
        /active-support/
      )
      assert.equal(calls.at(-1)?.archived, true)
      assert.equal(
        document.querySelectorAll('button[aria-label="Restore conversation"]')
          .length,
        1
      )
    } finally {
      await unmount(rendered)
    }
  })

  test('offers a retry action for transient list and detail failures', async () => {
    let calls = 0
    api.get = (async () => {
      calls += 1
      throw Object.assign(new Error('temporary failure'), {
        response: { status: 503 },
      })
    }) as typeof api.get

    const rendered = await renderHistory()
    try {
      assert.equal(calls, 1)
      assert.ok(
        rendered.container.querySelector(
          '[data-testid="assistant-history-retry"]'
        )
      )
      assert.ok(findButton('Retry'))
      await act(async () => {
        findButton('Retry').click()
        await flushEffects()
      })
      await act(flushEffects)
      assert.equal(calls, 2)
    } finally {
      await unmount(rendered)
    }

    const detailRendered = await renderHistoryConversation()
    try {
      assert.equal(calls, 3)
      assert.ok(
        detailRendered.container.querySelector(
          '[data-testid="assistant-history-detail-retry"]'
        )
      )
      assert.ok(findButton('Retry'))
      await act(async () => {
        findButton('Retry').click()
        await flushEffects()
      })
      await act(flushEffects)
      assert.equal(calls, 4)
    } finally {
      await unmount(detailRendered)
    }
  })

  test('restores an archived owner conversation and refreshes the current list', async () => {
    let archivedList = true
    let getCalls = 0
    const postCalls: string[] = []
    api.get = (async (_url: string, config: unknown) => {
      getCalls += 1
      const isArchived =
        (config as { params?: { archived?: boolean } } | undefined)?.params
          ?.archived === true
      return {
        data: {
          success: true,
          data: {
            conversations:
              isArchived && archivedList ? [archivedConversation] : [],
          },
        },
      }
    }) as typeof api.get
    api.post = (async (url: string) => {
      postCalls.push(url)
      archivedList = false
      return {
        data: {
          success: true,
          data: {
            id: archivedConversation.id,
            archived: false,
            archived_at: 0,
          },
        },
      }
    }) as typeof api.post

    const rendered = await renderHistory()
    try {
      await act(async () => {
        findButton('Archived conversations').click()
        await flushEffects()
      })
      await act(flushEffects)
      const callsBeforeRestore = getCalls

      await act(async () => {
        findButton('Restore').click()
        await flushEffects()
      })
      await act(flushEffects)

      assert.deepEqual(postCalls, ['/api/assistant/conversations/3/unarchive'])
      assert.ok(getCalls > callsBeforeRestore)
      assert.match(
        rendered.container.textContent ?? '',
        /No archived conversations yet\./
      )
    } finally {
      await unmount(rendered)
    }
  })

  test('does not show the audit scope to an ordinary L0 user', async () => {
    setUser(1)
    api.get = (async () => ({
      data: {
        success: true,
        data: { conversations: [activeConversation] },
      },
    })) as typeof api.get

    const rendered = await renderHistory()
    try {
      assert.equal(
        [...rendered.container.querySelectorAll('button')].some((button) =>
          button.textContent?.includes('User audit')
        ),
        false
      )
    } finally {
      await unmount(rendered)
    }
  })

  test('does not show the audit scope to a higher-trust ordinary user', async () => {
    setUser(1, 1)
    api.get = (async () => ({
      data: {
        success: true,
        data: { conversations: [activeConversation] },
      },
    })) as typeof api.get

    const rendered = await renderHistory()
    try {
      assert.equal(
        [...rendered.container.querySelectorAll('button')].some((button) =>
          button.textContent?.includes('User audit')
        ),
        false
      )
    } finally {
      await unmount(rendered)
    }
  })

  test('audits a positive user ID on submit, keeps lower-access records read-only, and restores self on switch back', async () => {
    setUser(10)
    const calls: Array<{ params?: Record<string, unknown> }> = []
    api.get = (async (url: string, config: unknown) => {
      if (!url.includes('/api/assistant/conversations')) {
        return {
          data: {
            success: true,
            data: { items: [], total: 0, page: 1, page_size: 24 },
          },
        }
      }
      const params = (
        config as { params?: Record<string, unknown> } | undefined
      )?.params
      calls.push({ params })
      const ownerId = params?.user_id
      return {
        data: {
          success: true,
          data: {
            conversations:
              ownerId === 42 ? [lowerAccessConversation] : [activeConversation],
          },
        },
      }
    }) as typeof api.get

    const rendered = await renderHistory()
    try {
      await act(async () => {
        findButton('User audit').click()
        await flushEffects()
      })

      const input = rendered.container.querySelector<HTMLInputElement>(
        '#assistant-history-audit-user-id'
      )
      assert.ok(input)
      await act(async () => {
        input.value = '42'
        input.dispatchEvent(new Event('input', { bubbles: true }))
        input.dispatchEvent(new Event('change', { bubbles: true }))
        await flushEffects()
      })
      const callsBeforeSubmit = calls.length
      assert.equal(callsBeforeSubmit, 1)

      await act(async () => {
        findButton('View').click()
        await flushEffects()
      })
      await act(flushEffects)

      assert.deepEqual(calls.at(-1)?.params, { user_id: 42 })
      assert.match(
        rendered.container.textContent ?? '',
        /Lower-access user conversation/
      )
      assert.equal(
        document.querySelectorAll('button[aria-label="Archive conversation"]')
          .length,
        0
      )
      assert.equal(
        document.querySelectorAll('button[aria-label="Restore conversation"]')
          .length,
        0
      )
      assert.deepEqual(
        rendered.queryClient
          .getQueryCache()
          .findAll({ queryKey: ['assistant-conversations'] })
          .map((query) => query.queryKey),
        [
          ['assistant-conversations', 99, undefined, 'self', null, 'active'],
          ['assistant-conversations', 99, undefined, 'audit', null, 'active'],
          ['assistant-conversations', 99, undefined, 'audit', 42, 'active'],
        ]
      )

      await act(async () => {
        findButton('My conversations').click()
        await flushEffects()
      })
      assert.match(rendered.container.textContent ?? '', /active-support/)
      assert.doesNotMatch(
        rendered.container.textContent ?? '',
        /lower-access-support/
      )
      assert.equal(
        document.querySelectorAll('button[aria-label="Archive conversation"]')
          .length,
        1
      )
    } finally {
      await unmount(rendered)
    }
  })

  test('does not request while typing an invalid ID and gives field feedback', async () => {
    setUser(10)
    let getCalls = 0
    api.get = (async (url: string) => {
      if (!url.includes('/api/assistant/conversations')) {
        return {
          data: {
            success: true,
            data: { items: [], total: 0, page: 1, page_size: 24 },
          },
        }
      }
      getCalls += 1
      return {
        data: {
          success: true,
          data: { conversations: [activeConversation] },
        },
      }
    }) as typeof api.get

    const rendered = await renderHistory()
    try {
      await act(async () => {
        findButton('User audit').click()
        await flushEffects()
      })
      const input = rendered.container.querySelector<HTMLInputElement>(
        '#assistant-history-audit-user-id'
      )
      assert.ok(input)
      await act(async () => {
        input.value = '0'
        input.dispatchEvent(new Event('input', { bubbles: true }))
        input.dispatchEvent(new Event('change', { bubbles: true }))
        await flushEffects()
        findButton('View').click()
        await flushEffects()
      })
      assert.equal(getCalls, 1)
      assert.ok(rendered.container.querySelector('[role="alert"]'))
      assert.match(
        rendered.container.textContent ?? '',
        /Enter a positive integer/
      )
    } finally {
      await unmount(rendered)
    }
  })

  test('keeps a safe non-enumerating message for a missing audited user', async () => {
    setUser(10)
    api.get = (async (_url: string, config: unknown) => {
      const params = (
        config as { params?: Record<string, unknown> } | undefined
      )?.params
      if (params?.user_id === 404) {
        const error = Object.assign(new Error('not found'), {
          response: { status: 404 },
        })
        throw error
      }
      return {
        data: {
          success: true,
          data: { conversations: [activeConversation] },
        },
      }
    }) as typeof api.get

    const rendered = await renderHistory()
    try {
      await act(async () => {
        findButton('User audit').click()
        await flushEffects()
      })
      const input = rendered.container.querySelector<HTMLInputElement>(
        '#assistant-history-audit-user-id'
      )
      assert.ok(input)
      await act(async () => {
        input.value = '404'
        input.dispatchEvent(new Event('input', { bubbles: true }))
        input.dispatchEvent(new Event('change', { bubbles: true }))
        await flushEffects()
        findButton('View').click()
        await flushEffects()
      })
      await act(flushEffects)
      assert.match(
        rendered.container.textContent ?? '',
        /This conversation no longer exists or is unavailable\./
      )
      assert.ok(
        rendered.container.querySelector('#assistant-history-audit-user-id')
      )
    } finally {
      await unmount(rendered)
    }
  })

  test('keeps long assistant history messages usable on narrow screens', async () => {
    const longAssistantMessage =
      'https://console.example.test/history/this-is-a-very-long-assistant-message-without-spaces-that-must-remain-readable-on-mobile'
    const longUserMessage =
      'https://console.example.test/history/this-is-a-very-long-user-message-without-spaces-that-must-remain-readable-on-mobile'
    api.get = (async (url: string) => {
      assert.equal(url, '/api/assistant/conversations/1')
      return {
        data: {
          success: true,
          data: {
            conversation: activeConversation,
            messages: [
              {
                id: 101,
                role: 'assistant' as const,
                content: longAssistantMessage,
                created_at: 1_786_400_002,
              },
              {
                id: 102,
                role: 'user' as const,
                content: longUserMessage,
                created_at: 1_786_400_003,
              },
            ],
            privacy_notice: 'Conversations are not private.',
          },
        },
      }
    }) as typeof api.get

    const rendered = await renderHistoryConversation()
    try {
      const assistantResponse = [
        ...rendered.container.querySelectorAll('div'),
      ].find(
        (node) =>
          node.textContent === longAssistantMessage &&
          node.className.includes('break-words')
      )
      assert.ok(assistantResponse)
      assert.match(assistantResponse.className, /max-w-full/)
      assert.match(assistantResponse.className, /\[&_pre\]:overflow-x-auto/)

      const userMessage = [...rendered.container.querySelectorAll('p')].find(
        (node) => node.textContent === longUserMessage
      )
      assert.ok(userMessage)
      assert.match(userMessage.className, /break-words/)
    } finally {
      await unmount(rendered)
    }
  })
})

test('history cache is isolated after switching the signed-in account', async () => {
  setUser(1)
  api.get = (async () => ({
    data: { success: true, data: { conversations: [activeConversation] } },
  })) as typeof api.get
  const rendered = await renderHistory()
  try {
    assert.match(rendered.container.textContent ?? '', /active-support/)
    api.get = (() => new Promise<unknown>(() => {})) as typeof api.get
    await act(async () => {
      useAuthStore
        .getState()
        .auth.setUser({ id: 100, username: 'other-account', role: 1 })
      await flushEffects()
    })
    assert.doesNotMatch(rendered.container.textContent ?? '', /active-support/)
  } finally {
    await act(async () => rendered.root.unmount())
    rendered.queryClient.clear()
  }
})
