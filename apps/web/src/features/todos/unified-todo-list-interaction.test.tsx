/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, describe, test } from 'node:test'

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

const { act, createElement } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} = await import('@tanstack/react-router')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { UnifiedTodoList } = await import('./unified-todo-list')

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

async function renderList() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const rootRoute = createRootRoute({
    component: () =>
      createElement(
        QueryClientProvider,
        { client: queryClient },
        createElement(I18nextProvider, { i18n }, createElement(UnifiedTodoList))
      ),
  })
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/',
    component: () => null,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  await act(async () => {
    root.render(createElement(RouterProvider, { router }))
    await flushEffects()
  })
  await act(flushEffects)
  return { container, queryClient, root }
}

async function unmount(rendered: Awaited<ReturnType<typeof renderList>>) {
  await act(async () => rendered.root.unmount())
  rendered.queryClient.clear()
  rendered.container.remove()
}

beforeEach(() => {
  useAuthStore.getState().auth.setUser({ id: 10, username: 'staff', role: 10 })
})

afterEach(() => {
  useAuthStore.getState().auth.reset()
  api.get = originalGet
  api.post = originalPost
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('UnifiedTodoList interaction', () => {
  test('marks a notification as read even when it has no destination', async () => {
    const posts: Array<{ url: string; body: unknown }> = []
    api.get = (async () => ({
      data: {
        success: true,
        data: {
          items: [
            {
              id: 'open_source_bounty:12',
              source_id: 12,
              category: 'open_source_bounty',
              type: 'comment',
              title: 'Bounty update',
              summary: 'A new comment needs your attention.',
              read: false,
              created_at: 1_786_400_000,
              updated_at: 1_786_400_000,
            },
          ],
          page: 1,
          page_size: 50,
          total: 1,
          category: 'all',
          unread_count: 1,
          total_unread_count: 1,
          unread_by_category: { open_source_bounty: 1 },
          categories: [{ key: 'open_source_bounty', total: 1, unread: 1 }],
        },
      },
    })) as typeof api.get
    api.post = (async (url: string, body: unknown) => {
      posts.push({ url, body })
      return { data: { success: true, data: { marked: 1 } } }
    }) as typeof api.post

    const rendered = await renderList()
    try {
      const categoryButtons = [...rendered.container.querySelectorAll('button')]
      const allCategory = categoryButtons.find((candidate) =>
        candidate.textContent?.startsWith('All')
      )
      const bountyCategory = categoryButtons.find((candidate) =>
        candidate.textContent?.startsWith('Bounty notifications')
      )
      assert.equal(allCategory?.getAttribute('aria-pressed'), 'true')
      assert.equal(bountyCategory?.getAttribute('aria-pressed'), 'false')
      assert.ok(allCategory?.classList.contains('min-h-11'))
      const markAll = [...rendered.container.querySelectorAll('button')].find(
        (candidate) => candidate.textContent?.includes('Mark all as read')
      )
      assert.ok(markAll)
      assert.ok(markAll.classList.contains('min-h-11'))

      const row = [...rendered.container.querySelectorAll('button')].find(
        (candidate) =>
          candidate.textContent?.includes('A new comment needs your attention.')
      )
      assert.ok(row)

      await act(async () => {
        row.click()
        await flushEffects()
      })
      await act(flushEffects)

      assert.deepEqual(posts, [
        {
          url: '/api/todos/read',
          body: {
            category: 'open_source_bounty',
            ids: [12],
            all: false,
          },
        },
      ])
    } finally {
      await unmount(rendered)
    }
  })
})

function supportTodoPage() {
  return {
    items: [
      {
        id: 'human_support:4',
        source_id: 4,
        category: 'human_support',
        type: 'handoff',
        title: 'assistant.human_support',
        summary: 'Help connecting my client',
        read: true,
        created_at: 1_786_400_000,
        updated_at: 1_786_400_000,
        details: {
          user_id: 20,
          username: 'customer',
          status: 'pending',
          conversation_id: 31,
        },
      },
    ],
    page: 1,
    page_size: 50,
    total: 1,
    category: 'all',
    unread_count: 0,
    total_unread_count: 0,
    unread_by_category: { human_support: 0 },
    categories: [{ key: 'human_support', total: 1, unread: 0 }],
  }
}

function findButton(text: string) {
  const button = [...document.querySelectorAll('button')].find((candidate) =>
    candidate.textContent?.includes(text)
  )
  assert.ok(button, `missing button: ${text}`)
  return button
}

async function clickButton(text: string) {
  await act(async () => {
    findButton(text).click()
    await flushEffects()
  })
  await act(flushEffects)
}

describe('human support staff workflow', () => {
  test('claims before reading the transcript and sends a reply into the same request', async () => {
    const gets: string[] = []
    const posts: Array<{ url: string; body: unknown }> = []
    let replied = false
    api.get = (async (url: string) => {
      gets.push(url)
      return {
        data: {
          success: true,
          data: url.startsWith('/api/todos')
            ? supportTodoPage()
            : {
                request: {
                  id: 4,
                  user_id: 20,
                  conversation_id: 31,
                  status: 'accepted',
                  assigned_admin_id: 10,
                },
                messages: [
                  {
                    id: 1,
                    role: 'user',
                    content: 'Customer original question',
                  },
                  { id: 2, role: 'assistant', content: 'Prior AI explanation' },
                  ...(replied
                    ? [
                        {
                          id: 3,
                          role: 'human',
                          actor_name: 'Actual staff author',
                          content: 'Check your endpoint',
                        },
                      ]
                    : []),
                ],
              },
        },
      }
    }) as typeof api.get
    api.post = (async (url: string, body: unknown) => {
      posts.push({ url, body })
      if (url.endsWith('/messages')) replied = true
      return {
        data: {
          success: true,
          data: {
            request: { id: 4, status: 'accepted', assigned_admin_id: 10 },
            message: { id: 3 },
          },
        },
      }
    }) as typeof api.post
    const rendered = await renderList()
    try {
      await clickButton('Help connecting my client')
      assert.ok(document.body.textContent?.includes('Accept support request'))
      assert.equal(
        gets.filter((url) => url.startsWith('/api/assistant/support')).length,
        0
      )
      await clickButton('Accept support request')
      assert.ok(
        document.body.textContent?.includes('Customer original question')
      )
      assert.ok(document.body.textContent?.includes('Prior AI explanation'))
      const textarea = document.querySelector('textarea')
      assert.ok(textarea)
      await act(async () => {
        const setter = Object.getOwnPropertyDescriptor(
          HTMLTextAreaElement.prototype,
          'value'
        )?.set
        assert.ok(setter)
        setter.call(textarea, 'Check your endpoint')
        textarea.dispatchEvent(new Event('input', { bubbles: true }))
        await flushEffects()
      })
      await clickButton('Send')
      assert.ok(document.body.textContent?.includes('Actual staff author'))
      assert.deepEqual(posts.slice(0, 2), [
        { url: '/api/assistant/support/4/accept', body: {} },
        {
          url: '/api/assistant/support/4/messages',
          body: { content: 'Check your endpoint' },
        },
      ])
      await clickButton('End human support')
      assert.deepEqual(posts.at(-1), {
        url: '/api/assistant/support/4/close',
        body: { cancel: false },
      })
      assert.equal(document.querySelector('[role="dialog"]'), null)
    } finally {
      await unmount(rendered)
    }
  })

  for (const change of ['account', 'session', 'role'] as const) {
    test(`discards late claim results after a ${change} change`, async () => {
      const gets: string[] = []
      let resolveClaim: ((response: unknown) => void) | undefined
      api.get = (async (url: string) => {
        gets.push(url)
        return { data: { success: true, data: supportTodoPage() } }
      }) as typeof api.get
      api.post = (() =>
        new Promise<unknown>((resolve) => {
          resolveClaim = resolve
        })) as typeof api.post
      const rendered = await renderList()
      try {
        await clickButton('Help connecting my client')
        await clickButton('Accept support request')
        assert.ok(resolveClaim)
        await act(async () => {
          if (change === 'session') {
            useAuthStore.setState((state) => ({
              auth: {
                ...state.auth,
                session: {
                  sid: 'replacement-session',
                  current: true,
                  login_method: 'password',
                  ip: '',
                  user_agent: '',
                  created_at: 1,
                  last_active_at: 1,
                  expires_at: 2,
                },
              },
            }))
          } else {
            useAuthStore.getState().auth.setUser({
              id: change === 'account' ? 11 : 10,
              username: 'changed-staff',
              role: change === 'role' ? 1 : 10,
            })
          }
          await flushEffects()
        })
        await act(async () => {
          resolveClaim?.({
            data: {
              success: true,
              data: {
                request: { id: 4, status: 'accepted', assigned_admin_id: 10 },
              },
            },
          })
          await flushEffects()
        })
        assert.equal(document.querySelector('[role="dialog"]'), null)
        assert.equal(
          gets.filter((url) => url.startsWith('/api/assistant/support')).length,
          0
        )
      } finally {
        await unmount(rendered)
      }
    })
  }
})
