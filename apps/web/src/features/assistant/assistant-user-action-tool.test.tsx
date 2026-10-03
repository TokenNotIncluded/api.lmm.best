/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

import type { AssistantUserDisplayNameChangeAction } from './api'

const domWindow = new Window({ url: 'https://console.example.test/' })
for (const key of [
  'window',
  'document',
  'navigator',
  'history',
  'location',
  'HTMLElement',
  'HTMLButtonElement',
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
const {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} = await import('@tanstack/react-router')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { toast } = await import('sonner')
const { api } = await import('@/lib/api')
const { AssistantUserActionTool } = await import('./assistant-user-action-tool')

const originalPut = api.put
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

const action: AssistantUserDisplayNameChangeAction = {
  type: 'user_display_name_change',
  requires_confirmation: true,
  target_user_id: 7,
  target_username: 'alice',
  target_display_name: 'Current name',
  target_role: 1,
  target_group: 'default',
  target_is_self: true,
  proposed_display_name: 'Proposed name',
  confirmation_token: 'display-name-confirmation',
}

type PutCall = { url: string; body: unknown }

function stubSuccessfulPut(calls: PutCall[]) {
  api.put = (async (url: string, body: unknown) => {
    calls.push({ url, body })
    return { data: { success: true, data: { display_name: 'Updated name' } } }
  }) as typeof api.put
}

async function flushEffects() {
  await new Promise((resolve) => setTimeout(resolve, 20))
}

async function renderTool(
  options: {
    action?: AssistantUserDisplayNameChangeAction
    onCompleted?: () => void
    onUpdated?: () => void | Promise<void>
  } = {}
) {
  const rootRoute = createRootRoute({
    component: () => (
      <I18nextProvider i18n={i18n}>
        <AssistantUserActionTool
          action={options.action ?? action}
          onCompleted={options.onCompleted ?? (() => {})}
          onUpdated={options.onUpdated}
        />
      </I18nextProvider>
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
    root.render(<RouterProvider router={router} />)
    await flushEffects()
  })
  await act(flushEffects)
  return { container, root }
}

function findButton(container: HTMLElement, text: string) {
  const button = [
    ...container.querySelectorAll<HTMLButtonElement>('button'),
  ].find((candidate) => candidate.textContent?.trim() === text)
  assert.ok(button, `Could not find ${text} button`)
  return button
}

function findNameInput(container: HTMLElement) {
  const input = container.querySelector<HTMLInputElement>(
    '#assistant-display-name'
  )
  assert.ok(input)
  return input
}

async function setName(input: HTMLInputElement, value: string) {
  const setValue = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setValue)
  await act(async () => {
    setValue.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await flushEffects()
  })
}

async function click(button: HTMLButtonElement) {
  await act(async () => {
    button.click()
    await flushEffects()
  })
}

async function unmount(rendered: Awaited<ReturnType<typeof renderTool>>) {
  await act(async () => rendered.root.unmount())
  rendered.container.remove()
}

afterEach(() => {
  api.put = originalPut
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('AssistantUserActionTool display name confirmation', () => {
  test('shows the current and editable proposed name without sending a mutation', async () => {
    const calls: PutCall[] = []
    stubSuccessfulPut(calls)
    const rendered = await renderTool()
    try {
      assert.match(
        rendered.container.textContent ?? '',
        /Current name \(alice\)/
      )
      assert.match(rendered.container.textContent ?? '', /Change display name/)
      const input = findNameInput(rendered.container)
      assert.equal(input.value, 'Proposed name')
      assert.equal(input.disabled, false)
      await setName(input, 'Edited name')
      assert.equal(input.value, 'Edited name')
      assert.deepEqual(calls, [])
    } finally {
      await unmount(rendered)
    }
  })

  test('cancel completes the card without updating the account or sending PUT', async () => {
    const calls: PutCall[] = []
    const events: string[] = []
    stubSuccessfulPut(calls)
    const rendered = await renderTool({
      onCompleted: () => {
        events.push('completed')
      },
      onUpdated: () => {
        events.push('updated')
      },
    })
    try {
      await setName(findNameInput(rendered.container), 'Edited name')
      await click(findButton(rendered.container, 'Cancel'))
      assert.deepEqual(calls, [])
      assert.deepEqual(events, ['completed'])
    } finally {
      await unmount(rendered)
    }
  })

  test('confirm sends only the trimmed edited name and confirmation token before refreshing', async () => {
    const calls: PutCall[] = []
    const events: string[] = []
    stubSuccessfulPut(calls)
    const rendered = await renderTool({
      onUpdated: async () => {
        events.push('updated')
      },
      onCompleted: () => {
        events.push('completed')
      },
    })
    try {
      await setName(findNameInput(rendered.container), '  Edited name  ')
      assert.deepEqual(calls, [])
      await click(findButton(rendered.container, 'Confirm'))
      assert.deepEqual(calls, [
        {
          url: '/api/assistant/profile/display-name',
          body: {
            display_name: 'Edited name',
            confirmation_token: action.confirmation_token,
            confirmed: true,
          },
        },
      ])
      assert.deepEqual(events, ['updated', 'completed'])
    } finally {
      await unmount(rendered)
    }
  })

  for (const [label, name] of [
    ['empty after trimming', '   '],
    ['21 Unicode characters', '😀'.repeat(21)],
  ] as const) {
    test(`blocks a name that is ${label} locally and retains its draft`, async () => {
      const calls: PutCall[] = []
      let completed = false
      stubSuccessfulPut(calls)
      const rendered = await renderTool({
        onCompleted: () => {
          completed = true
        },
      })
      try {
        const input = findNameInput(rendered.container)
        await setName(input, name)
        await click(findButton(rendered.container, 'Confirm'))
        assert.deepEqual(calls, [])
        assert.equal(completed, false)
        assert.equal(input.value, name)
        assert.equal(input.getAttribute('aria-invalid'), 'true')
        assert.equal(
          input.getAttribute('aria-describedby'),
          'assistant-display-name-error'
        )
        assert.match(
          rendered.container.querySelector('[role="alert"]')?.textContent ?? '',
          /Display name must be 1 to 20 characters/
        )
      } finally {
        await unmount(rendered)
      }
    })
  }

  for (const [label, name] of [
    ['an internal newline', 'New\nName'],
    ['an internal NUL', 'New\u0000Name'],
  ] as const) {
    test(`rejects ${label} locally without discarding the draft`, async () => {
      const calls: PutCall[] = []
      const events: string[] = []
      stubSuccessfulPut(calls)
      const rendered = await renderTool({
        action:
          label === 'an internal newline'
            ? { ...action, proposed_display_name: name }
            : action,
        onUpdated: () => {
          events.push('updated')
        },
        onCompleted: () => {
          events.push('completed')
        },
      })
      try {
        const input = findNameInput(rendered.container)
        // Single-line HTML inputs remove newlines from their displayed value.
        // The initial React draft must still reject the original proposal.
        if (label === 'an internal NUL') {
          await setName(input, name)
        }
        const visibleDraft = input.value
        await click(findButton(rendered.container, 'Confirm'))
        assert.deepEqual(calls, [])
        assert.deepEqual(events, [])
        assert.equal(input.value, visibleDraft)
        assert.equal(input.getAttribute('aria-invalid'), 'true')
        assert.match(
          rendered.container.querySelector('[role="alert"]')?.textContent ?? '',
          /Display name cannot contain control characters/
        )
        await click(findButton(rendered.container, 'Confirm'))
        assert.deepEqual(calls, [])
        assert.equal(input.value, visibleDraft)
        assert.deepEqual(events, [])
      } finally {
        await unmount(rendered)
      }
    })
  }

  test('still completes a successful update when refreshing the account fails', async () => {
    const calls: PutCall[] = []
    const events: string[] = []
    const successes: string[] = []
    const errors: string[] = []
    stubSuccessfulPut(calls)
    const originalSuccess = toast.success
    const originalError = toast.error
    toast.success = ((message: string) => {
      successes.push(message)
      return 'success'
    }) as typeof toast.success
    toast.error = ((message: string) => {
      errors.push(message)
      return 'error'
    }) as typeof toast.error
    const rendered = await renderTool({
      onUpdated: async () => {
        events.push('updated')
        throw new Error('Account refresh failed')
      },
      onCompleted: () => {
        events.push('completed')
      },
    })
    try {
      await setName(findNameInput(rendered.container), 'Edited name')
      await click(findButton(rendered.container, 'Confirm'))
      assert.equal(calls.length, 1)
      assert.deepEqual(events, ['updated', 'completed'])
      assert.deepEqual(successes, ['Display name changed successfully'])
      assert.deepEqual(errors, [])
      assert.equal(rendered.container.querySelector('[role="alert"]'), null)
      assert.equal(
        findNameInput(rendered.container).getAttribute('aria-invalid'),
        'false'
      )
    } finally {
      await unmount(rendered)
      toast.success = originalSuccess
      toast.error = originalError
    }
  })

  test('accepts 20 emoji as 20 Unicode characters rather than 40 UTF-16 units', async () => {
    const calls: PutCall[] = []
    stubSuccessfulPut(calls)
    const rendered = await renderTool()
    try {
      const name = '😀'.repeat(20)
      await setName(findNameInput(rendered.container), name)
      await click(findButton(rendered.container, 'Confirm'))
      assert.deepEqual(calls, [
        {
          url: '/api/assistant/profile/display-name',
          body: {
            display_name: name,
            confirmation_token: action.confirmation_token,
            confirmed: true,
          },
        },
      ])
      assert.equal(rendered.container.querySelector('[role="alert"]'), null)
    } finally {
      await unmount(rendered)
    }
  })

  test('shows a server error inline, preserves the edited draft, and allows retry', async () => {
    const calls: PutCall[] = []
    const events: string[] = []
    api.put = (async (url: string, body: unknown) => {
      calls.push({ url, body })
      if (calls.length === 1) {
        return {
          data: {
            success: false,
            message: 'Display name confirmation expired',
          },
        }
      }
      return { data: { success: true, data: { display_name: 'Edited name' } } }
    }) as typeof api.put
    const rendered = await renderTool({
      onUpdated: () => {
        events.push('updated')
      },
      onCompleted: () => {
        events.push('completed')
      },
    })
    try {
      const input = findNameInput(rendered.container)
      await setName(input, 'Edited name')
      await click(findButton(rendered.container, 'Confirm'))
      assert.equal(calls.length, 1)
      assert.equal(input.value, 'Edited name')
      assert.equal(input.disabled, false)
      assert.equal(input.getAttribute('aria-invalid'), 'true')
      assert.match(
        rendered.container.querySelector('[role="alert"]')?.textContent ?? '',
        /Display name confirmation expired/
      )
      assert.deepEqual(events, [])
      await click(findButton(rendered.container, 'Confirm'))
      assert.equal(calls.length, 2)
      assert.deepEqual(calls[1], calls[0])
      assert.equal(rendered.container.querySelector('[role="alert"]'), null)
      assert.equal(input.getAttribute('aria-invalid'), 'false')
      assert.deepEqual(events, ['updated', 'completed'])
    } finally {
      await unmount(rendered)
    }
  })

  test('disables editing and both buttons while submitting and sends no duplicate PUT', async () => {
    const calls: PutCall[] = []
    let resolveRequest: ((value: unknown) => void) | undefined
    api.put = ((url: string, body: unknown) => {
      calls.push({ url, body })
      return new Promise<unknown>((resolve) => {
        resolveRequest = resolve
      })
    }) as typeof api.put
    const events: string[] = []
    const rendered = await renderTool({
      onUpdated: () => {
        events.push('updated')
      },
      onCompleted: () => {
        events.push('completed')
      },
    })
    try {
      const confirm = findButton(rendered.container, 'Confirm')
      const cancel = findButton(rendered.container, 'Cancel')
      await click(confirm)
      assert.equal(calls.length, 1)
      assert.equal(confirm.disabled, true)
      assert.equal(cancel.disabled, true)
      assert.equal(findNameInput(rendered.container).disabled, true)
      await click(confirm)
      await click(cancel)
      assert.equal(calls.length, 1)
      assert.deepEqual(events, [])
      assert.ok(resolveRequest)
      await act(async () => {
        resolveRequest?.({
          data: { success: true, data: { display_name: 'Proposed name' } },
        })
        await flushEffects()
      })
      assert.deepEqual(events, ['updated', 'completed'])
      assert.equal(confirm.disabled, false)
    } finally {
      await unmount(rendered)
    }
  })
})
