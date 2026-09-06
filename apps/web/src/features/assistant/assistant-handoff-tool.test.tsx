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

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { AssistantHandoffTool } = await import('./assistant-handoff-tool')
type AssistantHumanSupportAction = import('./api').AssistantHumanSupportAction

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

const pendingHandoff = {
  id: 4,
  user_id: 1,
  source: 'handoff',
  intent: 'human_support',
  message: 'The API key page failed at 10:30 UTC.',
  status: 'pending',
  admin_user_id: 0,
  admin_note: '',
  created_at: 1_786_400_000,
  resolved_at: 0,
}

async function flushEffects() {
  await new Promise((resolve) => setTimeout(resolve, 25))
}

async function renderTool(confirmationAction?: AssistantHumanSupportAction) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  const rerender = async (action?: AssistantHumanSupportAction) => {
    await act(async () => {
      root.render(
        <QueryClientProvider client={queryClient}>
          <I18nextProvider i18n={i18n}>
            <AssistantHandoffTool confirmationAction={action} />
          </I18nextProvider>
        </QueryClientProvider>
      )
      await flushEffects()
    })
    await act(flushEffects)
  }
  await rerender(confirmationAction)
  return { container, queryClient, root, rerender }
}

function findButton(text: string): HTMLButtonElement {
  const button = [
    ...document.querySelectorAll<HTMLButtonElement>('button'),
  ].find((candidate) => candidate.textContent?.includes(text))
  assert.ok(button, `Could not find button containing ${text}`)
  return button
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

async function unmount(rendered: Awaited<ReturnType<typeof renderTool>>) {
  await act(async () => rendered.root.unmount())
  rendered.queryClient.clear()
  rendered.container.remove()
}

afterEach(() => {
  api.get = originalGet
  api.post = originalPost
  document.body.replaceChildren()
  useAuthStore.getState().auth.reset()
})

after(() => domWindow.close())

describe('AssistantHandoffTool', () => {
  test('requires at least five characters before review', async () => {
    api.get = (async () => {
      return { data: { success: true, data: null } }
    }) as typeof api.get

    const rendered = await renderTool()
    const textarea = rendered.container.querySelector<HTMLTextAreaElement>(
      '#assistant-handoff-message'
    )
    assert.ok(textarea)
    assert.equal(textarea.required, true)
    assert.equal(textarea.minLength, 5)

    await setTextareaValue(textarea, '四个字')
    const reviewButton = findButton('Review message')
    assert.equal(reviewButton.disabled, true)
    assert.match(
      rendered.container.textContent ?? '',
      /Support message must contain at least 5 characters/
    )

    await setTextareaValue(textarea, '五个字符消息')
    assert.equal(reviewButton.disabled, false)

    await unmount(rendered)
  })

  test('hides the message form when a human-support request is already pending', async () => {
    api.get = (async (url: string) => {
      assert.equal(url, '/api/assistant/handoffs/self')
      return { data: { success: true, data: pendingHandoff } }
    }) as typeof api.get

    const rendered = await renderTool()
    assert.match(
      rendered.container.textContent ?? '',
      /Administrator follow-up requested/
    )
    assert.match(rendered.container.textContent ?? '', /Pending/)
    assert.equal(
      rendered.container.querySelector('#assistant-handoff-message'),
      null
    )

    await unmount(rendered)
  })

  test('confirms submission and refreshes the pending request to show the administrator reply', async () => {
    let getCalls = 0
    let posted: { url: string; data: unknown } | undefined
    api.get = (async (url: string) => {
      assert.equal(url, '/api/assistant/handoffs/self')
      getCalls += 1
      if (getCalls === 1) throw new Error('status offline')
      if (getCalls === 3) {
        return {
          data: {
            success: true,
            data: {
              ...pendingHandoff,
              status: 'resolved',
              admin_note: 'The API key page has been fixed. Please try again.',
              resolved_at: pendingHandoff.created_at + 60,
            },
          },
        }
      }
      return { data: { success: true, data: null } }
    }) as typeof api.get
    api.post = (async (url: string, data: unknown) => {
      posted = { url, data }
      return { data: { success: true, data: pendingHandoff } }
    }) as typeof api.post

    const rendered = await renderTool()
    assert.match(
      rendered.container.textContent ?? '',
      /Unable to check support request status/
    )
    assert.match(
      rendered.container.textContent ?? '',
      /server prevents duplicate pending requests/
    )

    await act(async () => {
      findButton('Retry').click()
      await flushEffects()
    })
    await act(flushEffects)
    assert.equal(getCalls, 2)
    assert.doesNotMatch(
      rendered.container.textContent ?? '',
      /Unable to check support request status/
    )

    const textarea = rendered.container.querySelector<HTMLTextAreaElement>(
      '#assistant-handoff-message'
    )
    assert.ok(textarea)
    await setTextareaValue(textarea, pendingHandoff.message)

    await act(async () => {
      findButton('Review message').click()
      await flushEffects()
    })
    assert.equal(posted, undefined)
    assert.match(document.body.textContent ?? '', /Send this message\?/)

    await act(async () => {
      findButton('Confirm and send').click()
      await flushEffects()
    })
    await act(flushEffects)

    assert.deepEqual(posted, {
      url: '/api/assistant/handoffs',
      data: { confirmed: true, message: pendingHandoff.message },
    })
    assert.match(
      rendered.container.textContent ?? '',
      /Administrator follow-up requested/
    )

    await act(async () => {
      findButton('Refresh').click()
      await flushEffects()
    })
    await act(flushEffects)

    assert.equal(getCalls, 3)
    assert.match(
      rendered.container.textContent ?? '',
      /Previous request resolved/
    )
    assert.match(
      rendered.container.textContent ?? '',
      /The API key page has been fixed\. Please try again\./
    )
    assert.doesNotMatch(
      rendered.container.textContent ?? '',
      /Administrator follow-up requested/
    )
    assert.ok(rendered.container.querySelector('#assistant-handoff-message'))

    await unmount(rendered)
  })

  test('preserves the pending request when refreshing fails and allows another refresh', async () => {
    let getCalls = 0
    api.get = (async () => {
      getCalls += 1
      if (getCalls === 2) throw new Error('status offline')
      return { data: { success: true, data: pendingHandoff } }
    }) as typeof api.get

    const rendered = await renderTool()
    try {
      await act(async () => {
        findButton('Refresh').click()
        await flushEffects()
      })
      await act(flushEffects)

      assert.match(
        rendered.container.textContent ?? '',
        /Administrator follow-up requested/
      )
      assert.match(
        rendered.container.textContent ?? '',
        /Unable to check support request status/
      )
      assert.equal(
        rendered.container.querySelector('#assistant-handoff-message'),
        null
      )
      assert.equal(findButton('Refresh').disabled, false)

      await act(async () => {
        findButton('Refresh').click()
        await flushEffects()
      })
      await act(flushEffects)

      assert.equal(getCalls, 3)
      assert.doesNotMatch(
        rendered.container.textContent ?? '',
        /Unable to check support request status/
      )
      assert.match(
        rendered.container.textContent ?? '',
        /Administrator follow-up requested/
      )
    } finally {
      await unmount(rendered)
    }
  })

  test('consumes a prepared handoff once, allows a new manual issue, and accepts a new prepared action', async () => {
    let posted: { url: string; data: unknown } | undefined
    let getCalls = 0
    api.get = (async () => {
      getCalls += 1
      return {
        data: {
          success: true,
          data:
            getCalls === 1 ? null : { ...pendingHandoff, status: 'resolved' },
        },
      }
    }) as typeof api.get
    api.post = (async (url: string, data: unknown) => {
      posted = { url, data }
      return { data: { success: true, data: pendingHandoff } }
    }) as typeof api.post

    const action: AssistantHumanSupportAction = {
      type: 'human_support',
      confirmation_token: 'handoff-token',
      requires_confirmation: true,
      expires_in_seconds: 600,
      message: 'Please investigate the failed API request.',
    }
    const rendered = await renderTool(action)
    assert.equal(
      rendered.container.querySelector('#assistant-handoff-message'),
      null
    )
    assert.match(
      rendered.container.textContent ?? '',
      /Please investigate the failed API request\./
    )

    await act(async () => {
      findButton('Review message').click()
      await flushEffects()
    })
    await act(async () => {
      findButton('Confirm and send').click()
      await flushEffects()
    })
    await act(flushEffects)

    assert.deepEqual(posted, {
      url: '/api/assistant/handoffs',
      data: {
        confirmed: true,
        message: 'Please investigate the failed API request.',
        confirmation_token: 'handoff-token',
      },
    })

    await act(async () => {
      findButton('Refresh').click()
      await flushEffects()
    })
    await act(flushEffects)
    // Re-rendering the same action must not reactivate its consumed token.
    await rendered.rerender({ ...action })
    const textarea = rendered.container.querySelector<HTMLTextAreaElement>(
      '#assistant-handoff-message'
    )
    assert.ok(textarea)
    assert.equal(textarea.value, '')
    assert.equal(findButton('Review message').disabled, true)
    await setTextareaValue(
      textarea,
      'A different issue needs administrator help.'
    )
    await act(async () => {
      findButton('Review message').click()
      await flushEffects()
    })
    await act(async () => {
      findButton('Confirm and send').click()
      await flushEffects()
    })
    await act(flushEffects)
    assert.deepEqual(posted?.data, {
      confirmed: true,
      message: 'A different issue needs administrator help.',
    })

    await act(async () => {
      findButton('Refresh').click()
      await flushEffects()
    })
    await act(flushEffects)
    const nextAction = {
      ...action,
      confirmation_token: 'next-handoff-token',
      message: 'Please review the new billing issue.',
    }
    await rendered.rerender(nextAction)
    assert.equal(
      rendered.container.querySelector('#assistant-handoff-message'),
      null
    )
    assert.match(
      rendered.container.textContent ?? '',
      /Please review the new billing issue/
    )
    await act(async () => {
      findButton('Review message').click()
      await flushEffects()
    })
    await act(async () => {
      findButton('Confirm and send').click()
      await flushEffects()
    })
    await act(flushEffects)
    assert.deepEqual(posted?.data, {
      confirmed: true,
      message: nextAction.message,
      confirmation_token: nextAction.confirmation_token,
    })
    await unmount(rendered)
  })

  for (const change of ['unmount', 'account', 'session'] as const) {
    test(`ignores a late submission after ${change}`, async () => {
      useAuthStore
        .getState()
        .auth.setUser({ id: 1, username: 'first-user', role: 1 })
      api.get = (async () => ({
        data: { success: true, data: null },
      })) as typeof api.get
      let finishPost: (() => void) | undefined
      api.post = (async () =>
        new Promise<unknown>((resolve) => {
          finishPost = () =>
            resolve({ data: { success: true, data: pendingHandoff } })
        })) as typeof api.post
      const rendered = await renderTool()
      const textarea = rendered.container.querySelector<HTMLTextAreaElement>(
        '#assistant-handoff-message'
      )
      assert.ok(textarea)
      await setTextareaValue(textarea, pendingHandoff.message)
      await act(async () => {
        findButton('Review message').click()
        await flushEffects()
      })
      await act(async () => {
        findButton('Confirm and send').click()
        await flushEffects()
      })
      assert.ok(finishPost)

      if (change === 'unmount') {
        await unmount(rendered)
      } else {
        await act(async () => {
          if (change === 'account') {
            useAuthStore
              .getState()
              .auth.setUser({ id: 2, username: 'second-user', role: 1 })
          } else {
            useAuthStore.setState((state) => ({
              auth: {
                ...state.auth,
                session: {
                  sid: 'replacement-session',
                  current: true,
                  login_method: 'password',
                  ip: '',
                  user_agent: '',
                  created_at: 0,
                  last_active_at: 0,
                  expires_at: 0,
                },
              },
            }))
          }
          await flushEffects()
        })
        await act(flushEffects)
      }

      await act(async () => {
        finishPost?.()
        await flushEffects()
      })
      await act(flushEffects)
      assert.ok(
        rendered.queryClient
          .getQueryCache()
          .findAll({ queryKey: ['assistant-handoff'] })
          .every((query) => query.state.data === null)
      )
      if (change !== 'unmount') {
        assert.doesNotMatch(
          rendered.container.textContent ?? '',
          /Administrator follow-up requested/
        )
        assert.ok(
          rendered.container.querySelector('#assistant-handoff-message')
        )
        await unmount(rendered)
      }
    })
  }
})
