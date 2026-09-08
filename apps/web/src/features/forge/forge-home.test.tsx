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

import type { AuthUser } from '@/stores/auth-store'

const domWindow = new Window({ url: 'https://console.example.test/' })
Object.defineProperty(domWindow.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'history',
  'location',
  'HTMLElement',
  'HTMLAnchorElement',
  'HTMLButtonElement',
  'HTMLFormElement',
  'HTMLInputElement',
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

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} = await import('@tanstack/react-router')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { consumeQueuedAssistantRequest, subscribeToAssistantOpen } =
  await import('@/features/assistant/assistant-events')
const { useAuthStore } = await import('@/stores/auth-store')
const { ForgeHome } = await import('./forge-home')
const { PublicAccessPricing } = await import('../pricing/public-access-pricing')

const originalGet = api.get
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
  await new Promise((resolve) => setTimeout(resolve, 20))
}

function makeRouter(component = ForgeHome) {
  const rootRoute = createRootRoute({ component: Outlet })
  const homeRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/',
    component,
  })
  const routes = [
    '/about',
    '/challenges',
    '/dashboard',
    '/getting-started',
    '/guide',
    '/open-source-bounties',
    '/pricing',
    '/security',
    '/sign-in',
    '/sign-up',
    '/wallet',
  ].map((path) =>
    createRoute({
      getParentRoute: () => rootRoute,
      path,
      component: () => null,
    })
  )
  const challengeDetailRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/challenges/$challengeId',
    component: () => null,
  })

  return createRouter({
    routeTree: rootRoute.addChildren([
      homeRoute,
      ...routes,
      challengeDetailRoute,
    ]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
}

async function renderHome(
  user: AuthUser | null,
  assistantEnabled = true,
  statusPending = false,
  options: { registrationEnabled?: boolean; component?: typeof ForgeHome } = {}
) {
  useAuthStore.getState().auth.setUser(user)
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        refetchOnReconnect: false,
        refetchOnWindowFocus: false,
        retry: false,
      },
    },
  })
  const router = makeRouter(options.component)
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  api.get = (async (url) => {
    if (url === '/api/notice') {
      return { data: { success: true, data: '' } }
    }
    if (url === '/api/status') {
      if (statusPending) return await new Promise(() => undefined)
      return {
        data: {
          success: true,
          data: {
            backend_capabilities: { bounty_public_read: false },
            assistant: { enabled: assistantEnabled },
            register_enabled: options.registrationEnabled ?? true,
          },
        },
      }
    }
    if (url === '/api/assistant/pre-conversation-presets') {
      return {
        data: {
          success: true,
          data: {
            generation: 1_786_500_000,
            version: 'generated-v1',
            presets: [
              {
                id: 'generated_model_setup',
                label: 'Model setup',
                prompt: 'Configure a current model for my coding client.',
              },
              {
                id: 'generated_cost_review',
                label: 'Estimate cost',
                prompt: 'Estimate the cost of my expected model usage.',
              },
            ],
          },
        },
      }
    }
    return { data: { success: true, data: [] } }
  }) as typeof api.get

  await act(async () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <RouterProvider router={router} />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flushEffects()
  })
  await act(flushEffects)

  return { container, queryClient, root, router }
}

async function unmountHome(rendered: Awaited<ReturnType<typeof renderHome>>) {
  await act(async () => rendered.root.unmount())
  rendered.queryClient.clear()
  rendered.container.remove()
}

function findMessageInput(container: HTMLElement) {
  const input = container.querySelector<HTMLInputElement>('#forge-home-message')
  assert.ok(input)
  return input
}

async function submitMessage(container: HTMLElement, message: string) {
  const input = findMessageInput(container)
  const form = container.querySelector<HTMLFormElement>('form')
  assert.ok(form)
  const setValue = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setValue)

  await act(async () => {
    setValue.call(input, message)
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await flushEffects()
  })
  await act(async () => {
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await flushEffects()
  })
}

afterEach(() => {
  consumeQueuedAssistantRequest()
  api.get = originalGet
  useAuthStore.getState().auth.reset('complete')
  window.localStorage.clear()
  window.sessionStorage.clear()
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('ForgeHome code preview ornament', () => {
  test('renders one ornament outside the tablist and buttons', async () => {
    const rendered = await renderHome(null)
    const windowBars = rendered.container.querySelectorAll(
      '.forge-home-window-bar'
    )

    assert.equal(windowBars.length, 1)
    const windowBar = windowBars[0]
    assert.ok(windowBar)
    assert.equal(windowBar.childElementCount, 1)
    const ornament = windowBar.firstElementChild
    assert.ok(ornament)

    const tablist = rendered.container.querySelector('[role="tablist"]')
    assert.ok(tablist)
    assert.equal(tablist.contains(ornament), false)
    for (const button of rendered.container.querySelectorAll('button')) {
      assert.equal(button.contains(ornament), false)
    }

    await unmountHome(rendered)
  })
})

describe('ForgeHome API examples', () => {
  test('provides complete Claude and Gemini request bodies with JSON headers', async () => {
    const rendered = await renderHome(null)
    for (const tabName of ['Claude', 'Gemini']) {
      const tab = Array.from(
        rendered.container.querySelectorAll<HTMLButtonElement>('[role="tab"]')
      ).find((button) => button.textContent === tabName)
      assert.ok(tab)
      await act(async () => {
        tab.click()
        await flushEffects()
      })
      const code =
        rendered.container.querySelector('.forge-home-code-block')
          ?.textContent ?? ''
      assert.match(code, /Content-Type: application\/json/)
      assert.match(code, /\$LMM_API_KEY/)
      const body = code.match(/-d '([\s\S]+)'$/)?.[1]
      assert.ok(body)
      const request = JSON.parse(body)
      if (tabName === 'Claude') {
        assert.match(code, /\/v1\/messages/)
        assert.ok(request.max_tokens > 0)
        assert.deepEqual(request.messages, [{ role: 'user', content: 'Hello' }])
      } else {
        assert.match(code, /\/v1beta\/models\/model-name:generateContent/)
        assert.deepEqual(request.contents, [
          { role: 'user', parts: [{ text: 'Hello' }] },
        ])
      }
    }
    await unmountHome(rendered)
  })
})

describe('ForgeHome assistant entry', () => {
  test('starts app setup from a visible suggestion and preserves guidance across sign-in', async () => {
    const rendered = await renderHome(null)
    const suggestion = Array.from(
      rendered.container.querySelectorAll<HTMLButtonElement>(
        '.forge-home-assistant-prompts button'
      )
    ).find((button) => button.textContent?.includes('Connect my API key'))
    assert.ok(suggestion)

    await act(async () => {
      suggestion.click()
      await flushEffects()
    })

    assert.equal(rendered.router.state.location.pathname, '/sign-in')
    const queued = consumeQueuedAssistantRequest()
    assert.equal(queued?.autoSend, true)
    assert.match(queued?.message ?? '', /Ask which app and device I use/)
    assert.match(
      queued?.message ?? '',
      /Do not ask me to paste my key into chat/
    )
    await unmountHome(rendered)
  })

  test('animates server-generated prompts and stops when the visitor interacts', async () => {
    const rendered = await renderHome(null)
    const input = findMessageInput(rendered.container)

    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 90))
    })
    assert.match(
      input.placeholder,
      /^C|^Co/,
      'typewriter should start with the first server-generated prompt'
    )

    await act(async () => {
      input.focus()
      await flushEffects()
    })
    assert.equal(input.placeholder, 'Describe what you need...')

    await unmountHome(rendered)
  })

  test('queues onboarding with the message and redirects anonymous users to sign-in', async () => {
    const opened: Array<{
      autoSend: boolean
      message: string | undefined
      preset: string | undefined
    }> = []
    const unsubscribe = subscribeToAssistantOpen((queued) => {
      opened.push({
        preset: queued.preset,
        message: queued.message,
        autoSend: queued.autoSend,
      })
    })
    const rendered = await renderHome(null)

    await submitMessage(rendered.container, '  Help me configure the SDK  ')

    assert.deepEqual(opened, [
      {
        preset: undefined,
        message: 'Help me configure the SDK',
        autoSend: true,
      },
    ])
    assert.equal(rendered.router.state.location.pathname, '/sign-in')
    assert.deepEqual(
      { ...rendered.router.state.location.search },
      {
        redirect: '/dashboard',
      }
    )

    unsubscribe()
    await unmountHome(rendered)
  })

  test('queues onboarding with the message and redirects an L0 user to getting started', async () => {
    const opened: Array<{
      autoSend: boolean
      message: string | undefined
      preset: string | undefined
    }> = []
    const unsubscribe = subscribeToAssistantOpen((queued) => {
      opened.push({
        preset: queued.preset,
        message: queued.message,
        autoSend: queued.autoSend,
      })
    })
    const rendered = await renderHome({
      id: 7,
      username: 'l0-user',
      role: 1,
      developer_access_granted: false,
    })

    await submitMessage(rendered.container, '  I need L1 access  ')

    assert.deepEqual(opened, [
      { preset: 'onboarding', message: 'I need L1 access', autoSend: true },
    ])
    assert.equal(rendered.router.state.location.pathname, '/getting-started')

    unsubscribe()
    await unmountHome(rendered)
  })

  test('queues service guidance with the message and redirects an L1 user to the dashboard', async () => {
    const opened: Array<{
      autoSend: boolean
      message: string | undefined
      preset: string | undefined
    }> = []
    const unsubscribe = subscribeToAssistantOpen((queued) => {
      opened.push({
        preset: queued.preset,
        message: queued.message,
        autoSend: queued.autoSend,
      })
    })
    const rendered = await renderHome({
      id: 8,
      username: 'l1-user',
      role: 1,
      developer_access_granted: true,
    })

    await submitMessage(rendered.container, '  Show me the API setup  ')

    assert.deepEqual(opened, [
      { preset: 'service', message: 'Show me the API setup', autoSend: true },
    ])
    assert.equal(rendered.router.state.location.pathname, '/dashboard')

    unsubscribe()
    await unmountHome(rendered)
  })

  test('does not submit whitespace-only messages', async () => {
    const opened: Array<string | undefined> = []
    const unsubscribe = subscribeToAssistantOpen((request) => {
      opened.push(request.preset)
    })
    const rendered = await renderHome(null)

    await submitMessage(rendered.container, '   \n\t  ')

    const submit = rendered.container.querySelector<HTMLButtonElement>(
      'button[type="submit"]'
    )
    assert.ok(submit)
    assert.equal(submit.disabled, true)
    assert.deepEqual(opened, [])
    assert.equal(rendered.router.state.location.pathname, '/')

    unsubscribe()
    await unmountHome(rendered)
  })

  test('keeps the homepage input available while status is loading', async () => {
    const rendered = await renderHome(null, true, true)

    await submitMessage(rendered.container, 'Help me configure the SDK')

    assert.equal(rendered.router.state.location.pathname, '/sign-in')
    assert.equal(consumeQueuedAssistantRequest()?.autoSend, true)

    await unmountHome(rendered)
  })

  test('does not queue or navigate for a single punctuation mark', async () => {
    const opened: string[] = []
    const unsubscribe = subscribeToAssistantOpen((request) => {
      opened.push(request.id)
    })
    const rendered = await renderHome(null)

    await submitMessage(rendered.container, '.')

    assert.deepEqual(opened, [])
    assert.equal(rendered.router.state.location.pathname, '/')
    assert.equal(consumeQueuedAssistantRequest(), undefined)

    unsubscribe()
    await unmountHome(rendered)
  })

  test('does not leave a queued message when the assistant is disabled', async () => {
    const rendered = await renderHome(null, false)
    assert.equal(
      rendered.container.querySelector('.forge-home-assistant-prompts'),
      null
    )

    await submitMessage(rendered.container, 'Help me configure the SDK')

    assert.equal(rendered.router.state.location.pathname, '/')
    assert.equal(consumeQueuedAssistantRequest(), undefined)

    await unmountHome(rendered)
  })

  test('redacts sensitive values before queuing them across login', async () => {
    const queued: Array<
      NonNullable<ReturnType<typeof consumeQueuedAssistantRequest>>
    > = []
    const unsubscribe = subscribeToAssistantOpen((request) => {
      queued.push(request)
    })
    const rendered = await renderHome(null)

    await submitMessage(
      rendered.container,
      'Help configure the SDK for alice@example.test with sk-secret1234567890'
    )

    assert.equal(queued.length, 1)
    assert.equal(queued[0]?.autoSend, true)
    assert.equal(
      queued[0]?.message,
      'Help configure the SDK for [REDACTED_EMAIL] with [REDACTED_API_KEY]'
    )
    assert.equal(
      window.sessionStorage.getItem('lmm_assistant_queued_message'),
      null
    )
    const storage = JSON.stringify({ ...window.sessionStorage })
    assert.equal(storage.includes('alice@example.test'), false)
    assert.equal(storage.includes('sk-secret1234567890'), false)

    unsubscribe()
    await unmountHome(rendered)
  })
})

describe('Purchase entry follows account access', () => {
  test('offers registration only after live status confirms it', async () => {
    const pending = await renderHome(null, true, true)
    assert.equal(
      pending.container
        .querySelector('.forge-home-hero-actions a')
        ?.getAttribute('href'),
      '/sign-in?redirect=%2Fwallet'
    )
    await unmountHome(pending)

    const ready = await renderHome(null)
    assert.equal(
      ready.container
        .querySelector('.forge-home-hero-actions a')
        ?.getAttribute('href'),
      '/sign-up'
    )
    assert.ok(
      ready.container.textContent?.includes('Payment does not unlock access.')
    )
    await unmountHome(ready)
  })

  test('does not advertise disabled registration', async () => {
    const rendered = await renderHome(null, true, false, {
      registrationEnabled: false,
    })
    assert.equal(
      rendered.container
        .querySelector('.forge-home-hero-actions a')
        ?.textContent?.trim(),
      'Sign in'
    )
    await unmountHome(rendered)
  })

  test('takes an approved customer directly to the wallet', async () => {
    const rendered = await renderHome({
      id: 11,
      username: 'approved',
      role: 1,
      developer_access_granted: true,
    })
    const purchase = rendered.container.querySelector<HTMLAnchorElement>(
      '.forge-home-hero-actions a'
    )
    assert.ok(purchase)
    await act(async () => {
      purchase.click()
      await flushEffects()
    })
    assert.equal(rendered.router.state.location.pathname, '/wallet')
    await unmountHome(rendered)
  })

  test('keeps a pending customer on the access-request path', async () => {
    const rendered = await renderHome({
      id: 12,
      username: 'pending',
      role: 1,
      developer_access_granted: false,
    })
    const purchase = rendered.container.querySelector<HTMLAnchorElement>(
      '.forge-home-hero-actions a'
    )
    assert.ok(purchase)
    assert.equal(purchase.textContent?.trim(), 'Check access status')
    await act(async () => {
      purchase.click()
      await flushEffects()
    })
    assert.equal(rendered.router.state.location.pathname, '/getting-started')
    await unmountHome(rendered)
  })

  test('explains purchase conditions and answers pricing questions before access', async () => {
    const rendered = await renderHome(
      { id: 13, username: 'pending', role: 1, developer_access_granted: false },
      true,
      false,
      { component: PublicAccessPricing }
    )
    const main = rendered.container.querySelector('main')
    assert.ok(main)
    assert.equal(
      main.querySelector('a')?.getAttribute('href'),
      '/getting-started'
    )
    assert.equal(main.querySelectorAll('ol li').length, 3)
    const question = Array.from(
      main.querySelectorAll<HTMLButtonElement>('button')
    ).find((button) =>
      button.textContent?.includes('Is platform credit the same as money?')
    )
    assert.ok(question)
    await act(async () => {
      question.click()
      await flushEffects()
    })
    assert.equal(question.getAttribute('aria-expanded'), 'true')
    assert.ok(
      main.textContent?.includes(
        'The checkout shows the actual payment separately, with its settlement currency.'
      )
    )
    await unmountHome(rendered)
  })
})
