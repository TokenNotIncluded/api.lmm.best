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
import { after, afterEach, beforeEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type { ReactNode } from 'react'

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
const { AssistantToolCalls } = await import('./assistant-tool-calls')
type SupportAction = import('./api').AssistantHumanSupportAction
type ToolTrace = import('./api').AssistantToolTrace

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

const prepared: SupportAction = {
  type: 'human_support',
  requires_confirmation: true,
  expires_in_seconds: 600,
  confirmation_token: 'reviewed-action',
  message: 'Please investigate the console login failure.',
}
const pending = {
  id: 4,
  user_id: 1,
  source: 'handoff',
  intent: 'human_support',
  message: prepared.message,
  status: 'pending',
  admin_user_id: 0,
  admin_note: '',
  created_at: 1_786_400_000,
  updated_at: 1_786_400_000,
  resolved_at: 0,
}
const response = () => ({ data: { success: true, data: pending } })
const cleanup: Array<() => Promise<void>> = []

async function flushEffects() {
  await new Promise((resolve) => setTimeout(resolve, 25))
}

async function render(element: ReactNode) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const rerender = async (next: ReactNode) => {
    await act(async () => {
      root.render(
        <QueryClientProvider client={queryClient}>
          <I18nextProvider i18n={i18n}>{next}</I18nextProvider>
        </QueryClientProvider>
      )
      await flushEffects()
    })
    await act(flushEffects)
  }
  cleanup.push(async () => {
    await act(async () => root.unmount())
    queryClient.clear()
    container.remove()
  })
  await rerender(element)
  return { container, rerender }
}

function button(label: string): HTMLButtonElement {
  const found = [
    ...document.querySelectorAll<HTMLButtonElement>('button'),
  ].find((candidate) => candidate.textContent?.trim() === label)
  assert.ok(found, `Missing button: ${label}`)
  return found
}

async function click(label: string) {
  await act(async () => {
    button(label).click()
    await flushEffects()
  })
}

async function edit(textarea: HTMLTextAreaElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(
    HTMLTextAreaElement.prototype,
    'value'
  )?.set
  assert.ok(setter)
  await act(async () => {
    setter.call(textarea, value)
    textarea.dispatchEvent(new Event('input', { bubbles: true }))
    await flushEffects()
  })
}

beforeEach(() => {
  api.get = (async () => ({
    data: { success: true, data: null },
  })) as typeof api.get
  api.post = (async () => {
    assert.fail('A request must not be sent without explicit confirmation')
  }) as typeof api.post
})

afterEach(async () => {
  for (const unmount of cleanup.splice(0).reverse()) await unmount()
  api.get = originalGet
  api.post = originalPost
  useAuthStore.getState().auth.reset()
  document.body.replaceChildren()
})

after(() => domWindow.close())

test('review shows literal message text and does not submit before confirmation', async () => {
  const action = {
    ...prepared,
    message: '<img src=x onerror=alert(1)> support request',
  }
  await render(<AssistantHandoffTool confirmationAction={action} />)
  await click('Review message')
  const preview = document.querySelector(
    '[data-testid="assistant-handoff-review-message"]'
  )
  assert.equal(preview?.textContent, action.message)
  assert.equal(preview?.querySelector('img'), null)
  assert.equal(button('Confirm and send').disabled, false)
})

test('new action token invalidates an open review, even with the same text', async () => {
  let requests = 0
  api.post = (async () => {
    requests += 1
    return response()
  }) as typeof api.post
  const view = await render(
    <AssistantHandoffTool confirmationAction={prepared} />
  )
  await click('Review message')
  const oldConfirm = button('Confirm and send')
  await view.rerender(
    <AssistantHandoffTool
      confirmationAction={{ ...prepared, confirmation_token: 'replacement' }}
    />
  )
  await act(async () => {
    oldConfirm.click()
    await flushEffects()
  })
  assert.equal(requests, 0)
  await click('Review message')
  assert.equal(button('Confirm and send').disabled, false)
})

test('editing a signed request discards its token and survives equivalent rerenders', async () => {
  const posted: Array<Record<string, unknown>> = []
  api.post = (async (_url: string, body: Record<string, unknown>) => {
    posted.push(body)
    return response()
  }) as typeof api.post
  const view = await render(
    <AssistantHandoffTool confirmationAction={prepared} />
  )
  await click('Edit')
  const textarea = view.container.querySelector('textarea')
  assert.ok(textarea)
  const message = 'A manually edited request for a human administrator.'
  await edit(textarea, message)
  await view.rerender(
    <AssistantHandoffTool confirmationAction={{ ...prepared }} />
  )
  assert.equal(view.container.querySelector('textarea')?.value, message)
  assert.equal(posted.length, 0)
  await click('Review message')
  await click('Confirm and send')
  assert.equal(posted[0]?.message, message)
  assert.equal(posted[0]?.confirmation_token, undefined)
})

test('two same-tick confirmation clicks send only one request', async () => {
  let requests = 0
  let finish!: (value: ReturnType<typeof response>) => void
  const inFlight = new Promise<ReturnType<typeof response>>((resolve) => {
    finish = resolve
  })
  api.post = (async () => {
    requests += 1
    return inFlight
  }) as typeof api.post
  await render(<AssistantHandoffTool confirmationAction={prepared} />)
  await click('Review message')
  await act(async () => {
    const confirm = button('Confirm and send')
    confirm.click()
    confirm.click()
    await flushEffects()
  })
  assert.equal(requests, 1)
  assert.equal(button('Confirm and send').disabled, true)
  await act(async () => {
    finish(response())
    await flushEffects()
  })
})

test('a failed request remains visible and retries only after another explicit click', async () => {
  const bodies: unknown[] = []
  api.post = (async (_url: string, body: unknown) => {
    bodies.push(body)
    if (bodies.length === 1) throw new Error('Temporary support outage')
    return response()
  }) as typeof api.post
  await render(<AssistantHandoffTool confirmationAction={prepared} />)
  await click('Review message')
  await click('Confirm and send')
  assert.equal(bodies.length, 1)
  assert.ok(
    [...document.querySelectorAll('[role="alert"]')].some((alert) =>
      alert.textContent?.includes('Temporary support outage')
    )
  )
  assert.equal(button('Confirm and send').disabled, false)
  await act(flushEffects)
  assert.equal(bodies.length, 1)
  await click('Confirm and send')
  assert.equal(bodies.length, 2)
  assert.deepEqual(bodies[0], bodies[1])
})

test('oversized prepared text is editable and Unicode maximum is enforced before review', async () => {
  const view = await render(
    <AssistantHandoffTool
      confirmationAction={{ ...prepared, message: '中'.repeat(2001) }}
    />
  )
  const textarea = view.container.querySelector('textarea')
  assert.ok(textarea)
  assert.equal(button('Review message').disabled, true)
  await edit(textarea, '\u{1F600}'.repeat(2000))
  assert.equal(button('Review message').disabled, false)
  await edit(textarea, '\u{1F600}'.repeat(2001))
  assert.equal(button('Review message').disabled, true)
})

const supportTrace: ToolTrace = {
  name: 'request_human_support',
  status: 'approval-requested',
  input: { action: 'support', message: 'Do not replay this historical message.' },
}

test('waiting tool card has a visible manual recovery button outside the disclosure trigger', async () => {
  const posted: Array<Record<string, unknown>> = []
  api.post = (async (_url: string, body: Record<string, unknown>) => {
    posted.push(body)
    return response()
  }) as typeof api.post
  const view = await render(<AssistantToolCalls traces={[supportTrace]} />)
  const entry = button('Send a message to an administrator')
  assert.equal(entry.parentElement?.closest('button'), null)
  assert.equal(view.container.querySelector('textarea'), null)
  await click('Send a message to an administrator')
  const textarea = document.querySelector('textarea')
  assert.ok(textarea)
  assert.equal(textarea.value, '')
  assert.equal(posted.length, 0)
  await edit(textarea, 'Please ask a human administrator to investigate.')
  await click('Review message')
  assert.equal(posted.length, 0)
  await click('Confirm and send')
  assert.equal(
    posted[0]?.message,
    'Please ask a human administrator to investigate.'
  )
  assert.equal(posted[0]?.confirmation_token, undefined)
})

test('manual recovery is not offered for account-disable or unknown tool actions', async () => {
  const view = await render(
    <AssistantToolCalls
      traces={[
        { ...supportTrace, input: { action: 'disable_account' } },
        { ...supportTrace, input: { action: 'unknown_action' } },
        {
          ...supportTrace,
          status: 'output-available',
          input: { action: 'support' },
        },
      ]}
    />
  )
  assert.equal(
    [...view.container.querySelectorAll('button')].some((candidate) =>
      candidate.textContent?.includes('Send a message to an administrator')
    ),
    false
  )
})

test('manual recovery input does not reuse the existing form label ID', async () => {
  await render(
    <>
      <AssistantHandoffTool />
      <AssistantToolCalls traces={[supportTrace]} />
    </>
  )
  await click('Send a message to an administrator')
  const inputs = [...document.querySelectorAll('textarea')]
  assert.equal(inputs.length, 2)
  assert.equal(new Set(inputs.map((input) => input.id)).size, 2)
  for (const input of inputs) {
    assert.ok(
      [...document.querySelectorAll('label')].some(
        (label) => label.htmlFor === input.id
      )
    )
  }
})
