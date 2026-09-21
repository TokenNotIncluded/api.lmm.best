/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'
import type { ReactNode } from 'react'

import type { AssistantNewUserGift as Gift } from './api'

const domWindow = new Window({ url: 'https://console.example.test/' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'CustomEvent',
  'MutationObserver',
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
const { AssistantJourneyProgress } = await import('./assistant-journey')
const { AssistantNewUserGift } = await import('./assistant-new-user-gift')
const { AssistantWeeklyDiscount } = await import('./assistant-weekly-discount')

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

async function flushQueries() {
  await new Promise((resolve) => setTimeout(resolve, 20))
}

async function render(element: ReactNode) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>{element}</I18nextProvider>
      </QueryClientProvider>
    )
    await flushQueries()
  })
  await act(flushQueries)
  return { container, queryClient, root }
}

async function unmount(rendered: Awaited<ReturnType<typeof render>>) {
  await act(async () => rendered.root.unmount())
  rendered.queryClient.clear()
  rendered.container.remove()
}

afterEach(() => {
  api.get = originalGet
  api.post = originalPost
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('assistant game-style progress', () => {
  test('distinguishes earned, pending, and used gift-side-quest states', async () => {
    api.get = (async (url: string) => {
      assert.equal(url, '/api/assistant/journey')
      return {
        data: {
          success: true,
          data: {
            main: [{ id: 'ask_ai', status: 'completed' }],
            side: [
              { id: 'earn_ai_gift', status: 'failed' },
              { id: 'accept_bounty', status: 'pending' },
            ],
          },
        },
      }
    }) as typeof api.get

    const rendered = await render(<AssistantJourneyProgress />)
    try {
      const text = rendered.container.textContent ?? ''
      assert.match(text, /Main quest 1\/1/)
      assert.match(text, /Side quest 0\/2/)
      assert.match(
        text,
        /Chat with AI to earn a \$0–\$10 \(Platform\) new-user gift/
      )
      assert.match(text, /Accept an open-source bounty/)
    } finally {
      await unmount(rendered)
    }
  })

  test('keeps the open page journey in normal flow on narrow screens and makes the desktop popover opaque', async () => {
    api.get = (async (url: string) => {
      assert.equal(url, '/api/assistant/journey')
      return {
        data: {
          success: true,
          data: {
            main: [{ id: 'ask_ai', status: 'completed' }],
            side: [{ id: 'accept_bounty', status: 'pending' }],
          },
        },
      }
    }) as typeof api.get

    const rendered = await render(
      <AssistantJourneyProgress presentation='page' />
    )
    try {
      const journey = rendered.container.querySelector<HTMLElement>(
        '[data-testid="assistant-journey"]'
      )
      const panel = rendered.container.querySelector<HTMLElement>(
        '[data-testid="assistant-journey-panel"]'
      )
      assert.ok(journey)
      assert.ok(panel)
      assert.match(journey.className, /order-last/)
      assert.match(journey.className, /w-full/)
      assert.match(journey.className, /md:relative/)
      assert.match(panel.className, /bg-background/)
      assert.match(panel.className, /w-full/)
      assert.match(panel.className, /md:absolute/)
      assert.doesNotMatch(panel.className, /bg-background\//)
      assert.doesNotMatch(panel.className, /(?:^|\s)absolute(?:\s|$)/)
    } finally {
      await unmount(rendered)
    }
  })

  test('shows a zero decision as consumed and claims a positive gift once', async () => {
    let gift: Gift = {
      amount_cents: 0,
      quota: 0,
      status: 'declined' as const,
      reason: 'The one-time decision was zero.',
      created_at: 1,
      claimed_at: 0,
    }
    api.get = (async (url: string) => {
      assert.equal(url, '/api/assistant/new-user-gift')
      return { data: { success: true, data: gift } }
    }) as typeof api.get

    let rendered = await render(<AssistantNewUserGift enabled />)
    try {
      assert.match(rendered.container.textContent ?? '', /No gift this time/)
      assert.match(rendered.container.textContent ?? '', /Opportunity used/)
      assert.equal(rendered.container.querySelector('button'), null)
    } finally {
      await unmount(rendered)
    }

    gift = {
      amount_cents: 625,
      quota: 3_125_000,
      status: 'offered',
      reason: 'Clear, constructive, and concrete engagement.',
      created_at: 2,
      claimed_at: 0,
    }
    let claims = 0
    api.post = (async (url: string) => {
      assert.equal(url, '/api/assistant/new-user-gift/claim')
      claims++
      return {
        data: {
          success: true,
          data: {
            gift: { ...gift, status: 'claimed', claimed_at: 3 },
            already_claimed: false,
          },
        },
      }
    }) as typeof api.post
    rendered = await render(<AssistantNewUserGift enabled />)
    try {
      const button =
        rendered.container.querySelector<HTMLButtonElement>('button')
      assert.ok(button)
      await act(async () => {
        button.click()
        await flushQueries()
      })
      assert.equal(claims, 1)
      assert.match(rendered.container.textContent ?? '', /Claimed/)
    } finally {
      await unmount(rendered)
    }
  })

  test('keeps a retry path visible when the welcome gift status cannot load', async () => {
    let attempts = 0
    api.get = (async (url: string) => {
      assert.equal(url, '/api/assistant/new-user-gift')
      attempts++
      throw new Error('temporary failure')
    }) as typeof api.get

    const rendered = await render(<AssistantNewUserGift enabled />)
    try {
      const error = rendered.container.querySelector<HTMLElement>(
        '[data-testid="assistant-new-user-gift-error"]'
      )
      assert.ok(error)
      assert.match(error.textContent ?? '', /Failed to load/)

      const retry = error.querySelector<HTMLButtonElement>('button')
      assert.ok(retry)
      await act(async () => {
        retry.click()
        await flushQueries()
      })
      assert.equal(attempts, 2)
    } finally {
      await unmount(rendered)
    }
  })

  test('keeps a low-interruption retry path when weekly discount status cannot load', async () => {
    let attempts = 0
    api.get = (async (url: string) => {
      assert.equal(url, '/api/assistant/weekly-discount')
      attempts++
      throw new Error('temporary failure')
    }) as typeof api.get

    const rendered = await render(<AssistantWeeklyDiscount enabled />)
    try {
      const error = rendered.container.querySelector<HTMLElement>(
        '[data-testid="assistant-weekly-discount-error"]'
      )
      assert.ok(error)
      assert.match(
        error.textContent ?? '',
        /Unable to load current top-up discounts/
      )

      const retry = error.querySelector<HTMLButtonElement>('button')
      assert.ok(retry)
      await act(async () => {
        retry.click()
        await flushQueries()
      })
      assert.equal(attempts, 2)
    } finally {
      await unmount(rendered)
    }
  })
})
