/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { AssistantKeyManagementAction } from './api'
import {
  keyActionReceipt,
  preparedKeyAction,
} from './assistant-key-management-test-fixtures'
import { act, api, flushEffects } from './assistant-key-tool-test-support'

const { StrictMode } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { AssistantKeyManagementTool } =
  await import('./assistant-key-management-tool')

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })

async function renderTool(
  action: AssistantKeyManagementAction,
  onCancelled = () => {}
) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const render = async (nextAction: AssistantKeyManagementAction) => {
    await act(async () => {
      root.render(
        <StrictMode>
          <QueryClientProvider client={queryClient}>
            <I18nextProvider i18n={i18n}>
              <AssistantKeyManagementTool
                action={nextAction}
                onCancelled={onCancelled}
              />
            </I18nextProvider>
          </QueryClientProvider>
        </StrictMode>
      )
      await flushEffects()
    })
  }
  await render(action)
  return {
    container,
    queryClient,
    render,
    unmount: async () => {
      await act(async () => root.unmount())
      queryClient.clear()
      container.remove()
    },
  }
}

function button(container: HTMLElement, label: string) {
  const result = [
    ...container.querySelectorAll<HTMLButtonElement>('button'),
  ].find((node) => node.textContent?.includes(label))
  assert.ok(result, `missing button ${label}`)
  return result
}

async function enterCode(container: HTMLElement, code: string) {
  const input = container.querySelector<HTMLInputElement>('input')
  assert.ok(input)
  await act(async () => {
    Object.getOwnPropertyDescriptor(
      window.HTMLInputElement.prototype,
      'value'
    )?.set?.call(input, code)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

test('mounting and rerendering show the exact target and effects without executing', async () => {
  let posts = 0
  api.post = (async () => {
    posts += 1
    throw new Error('must not execute')
  }) as typeof api.post
  const action = preparedKeyAction()
  const rendered = await renderTool(action)
  try {
    await rendered.render({ ...action })
    assert.equal(posts, 0)
    assert.match(
      rendered.container.textContent ?? '',
      /Production SDK · ID 7 · Group: default/
    )
    assert.match(rendered.container.textContent ?? '', /irreversible/)
    assert.equal(
      rendered.container.innerHTML.includes(action.confirmation_token),
      false
    )
    assert.equal(rendered.container.querySelector('input'), null)
  } finally {
    await rendered.unmount()
  }
})

test('cancellation clears a pending 2FA card and never executes its token', async () => {
  let posts = 0
  let cancelled = 0
  api.post = (async () => {
    posts += 1
    throw new Error('must not execute')
  }) as typeof api.post
  const action = preparedKeyAction('disable', true)
  const rendered = await renderTool(action, () => {
    cancelled += 1
  })
  try {
    assert.match(rendered.container.textContent ?? '', /stops future requests/)
    assert.equal(button(rendered.container, 'Confirm disabling').disabled, true)
    await enterCode(rendered.container, '654321')
    assert.equal(
      button(rendered.container, 'Confirm disabling').disabled,
      false
    )
    await act(async () => button(rendered.container, 'Cancel').click())
    await rendered.render(action)
    assert.equal(rendered.container.textContent, '')
    assert.equal(rendered.container.querySelector('input'), null)
    assert.equal(cancelled, 1)
    assert.equal(posts, 0)
  } finally {
    await rendered.unmount()
  }
})

test('an explicit double click executes once, sends only opaque confirmation data, and refreshes keys', async () => {
  const action = preparedKeyAction('disable', true)
  const requests: Array<{ url: string; data: unknown }> = []
  let resolveRequest!: (value: unknown) => void
  api.post = (async (url: string, data: unknown) => {
    requests.push({ url, data })
    return await new Promise<unknown>((resolve) => {
      resolveRequest = resolve
    })
  }) as typeof api.post
  const rendered = await renderTool(action)
  try {
    rendered.queryClient.setQueryData(['keys', 1], ['old-key-list'])
    rendered.queryClient.setQueryData(['api-key', 7], { status: 1 })
    await enterCode(rendered.container, '  RECOVERY-CODE  ')
    const confirm = button(rendered.container, 'Confirm disabling')
    await act(async () => {
      confirm.click()
      confirm.click()
      await flushEffects()
    })
    assert.equal(confirm.disabled, true)
    assert.equal(button(rendered.container, 'Cancel').disabled, true)
    assert.deepEqual(requests, [
      {
        url: '/api/assistant/tools/key-action',
        data: {
          confirmation_token: action.confirmation_token,
          two_factor_code: 'RECOVERY-CODE',
        },
      },
    ])
    await act(async () => {
      resolveRequest({
        data: { success: true, data: keyActionReceipt(action) },
      })
      await flushEffects()
    })
    assert.match(rendered.container.textContent ?? '', /API key disabled/)
    assert.match(rendered.container.textContent ?? '', /Production SDK · ID 7/)
    assert.equal(rendered.container.querySelector('input'), null)
    assert.equal(
      rendered.queryClient.getQueryState(['keys', 1])?.isInvalidated,
      true
    )
    assert.equal(
      rendered.queryClient.getQueryState(['api-key', 7])?.isInvalidated,
      true
    )
    assert.equal(rendered.container.innerHTML.includes('RECOVERY-CODE'), false)
    assert.equal(
      rendered.container.innerHTML.includes(action.confirmation_token),
      false
    )
  } finally {
    await rendered.unmount()
  }
})

test('tampered or secret-bearing preparations never render or execute', async () => {
  let posts = 0
  api.post = (async () => {
    posts += 1
    throw new Error('must not execute')
  }) as typeof api.post
  for (const action of [
    { ...preparedKeyAction(), api_key: 'sk-private' },
    {
      ...preparedKeyAction(),
      token: { ...preparedKeyAction().token, key: 'sk-private' },
    },
    { ...preparedKeyAction(), requires_confirmation: false },
  ]) {
    const rendered = await renderTool(action as AssistantKeyManagementAction)
    try {
      assert.equal(rendered.container.textContent, '')
      assert.equal(rendered.container.querySelector('button'), null)
      assert.equal(posts, 0)
    } finally {
      await rendered.unmount()
    }
  }
})

test('wrong-target or secret-bearing receipts cannot claim success or expose raw text', async () => {
  const action = preparedKeyAction()
  api.post = (async () => ({
    data: {
      success: true,
      data: {
        ...keyActionReceipt(action),
        id: 99,
        api_key: 'sk-private',
      },
    },
  })) as typeof api.post
  const rendered = await renderTool(action)
  try {
    await act(async () => {
      button(rendered.container, 'Confirm deletion').click()
      await flushEffects()
    })
    assert.ok(rendered.container.querySelector('[role="alert"]'))
    assert.doesNotMatch(
      rendered.container.textContent ?? '',
      /API key deleted|sk-private/
    )
    assert.equal(button(rendered.container, 'Confirm deletion').disabled, false)
  } finally {
    await rendered.unmount()
  }
})

test('expired confirmation disables the old action while authentication errors allow a fresh code', async () => {
  const action = preparedKeyAction('delete', true)
  let posts = 0
  api.post = (async () => {
    posts += 1
    return {
      data: {
        success: false,
        message: 'sk-private',
        code:
          posts === 1
            ? 'ASSISTANT_TWO_FACTOR_INVALID'
            : 'ASSISTANT_KEY_CONFIRMATION_INVALID',
      },
    }
  }) as typeof api.post
  const rendered = await renderTool(action)
  try {
    await enterCode(rendered.container, 'bad-code')
    await act(async () => {
      button(rendered.container, 'Confirm deletion').click()
      await flushEffects()
    })
    assert.equal(rendered.container.querySelector('input')?.value, '')
    assert.doesNotMatch(rendered.container.textContent ?? '', /sk-private/)
    await enterCode(rendered.container, 'new-code')
    assert.equal(button(rendered.container, 'Confirm deletion').disabled, false)
    await act(async () => {
      button(rendered.container, 'Confirm deletion').click()
      await flushEffects()
    })
    assert.match(rendered.container.textContent ?? '', /no longer valid/)
    assert.equal(button(rendered.container, 'Confirm deletion').disabled, true)
    assert.equal(rendered.container.querySelector('input')?.disabled, true)
    assert.equal(posts, 2)
  } finally {
    await rendered.unmount()
  }
})
