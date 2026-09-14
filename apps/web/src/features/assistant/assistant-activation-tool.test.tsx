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
const { AssistantActivationTool } = await import('./assistant-activation-tool')

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

const pendingRequest = {
  id: 9,
  status: 'pending' as const,
  reason: 'Need access for a test integration.',
  source: 'assistant_recommendation' as const,
  ai_recommendation: 'Recommend L1 for a concrete test integration.',
  admin_note: '',
  created_at: 1_786_400_000,
  reviewed_at: 0,
}
const directPendingRequest = {
  ...pendingRequest,
  source: 'assistant_request' as const,
  ai_recommendation: '',
}
const approvedRequest = {
  ...pendingRequest,
  status: 'approved' as const,
  admin_note: 'Approved for testing.',
  reviewed_at: 1_786_500_000,
}
const rejectedRequest = {
  ...pendingRequest,
  status: 'rejected' as const,
  admin_note: 'Please explain which client you will connect.',
  reviewed_at: 1_786_500_000,
}
const recommendationDraft = {
  type: 'l1_recommendation' as const,
  user_statement: 'I need access for a private Claude Code integration.',
  recommendation:
    'Recommend L1 because the user provided a specific client and use case.',
  confirmation_token: 'assistant-confirmation-token',
}

async function flushEffects() {
  await new Promise((resolve) => setTimeout(resolve, 25))
}

async function renderTool(
  options: {
    onContinueSetup?: () => void
    onSubmitted?: () => void
    onDraftConsumed?: () => void
    recommendationDraft?: typeof recommendationDraft
  } = {}
) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <AssistantActivationTool
            recommendationDraft={options.recommendationDraft}
            onContinueSetup={options.onContinueSetup}
            onSubmitted={options.onSubmitted}
            onDraftConsumed={options.onDraftConsumed}
          />
        </I18nextProvider>
      </QueryClientProvider>
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
  return { container, queryClient, root }
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

async function unmount(rendered: Awaited<ReturnType<typeof renderTool>>) {
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

describe('AssistantActivationTool', () => {
  test('keeps the direct request fallback collapsed until requested', async () => {
    api.get = (async () => ({
      data: { success: true, data: null },
    })) as typeof api.get

    const rendered = await renderTool()
    try {
      assert.equal(document.querySelector('textarea'), null)
      assert.equal(document.querySelector('a[href="/wallet"]'), null)
      assert.ok(findButton('Write request myself'))

      await act(async () => {
        findButton('Write request myself').click()
        await flushEffects()
      })

      assert.ok(document.querySelector('textarea'))
      assert.match(
        document.body.textContent ?? '',
        /without an AI recommendation/
      )
      assert.equal(findButton('Submit for review').disabled, true)
    } finally {
      await unmount(rendered)
    }
  })

  test('submits a direct request without an AI recommendation', async () => {
    api.get = (async () => ({
      data: { success: true, data: null },
    })) as typeof api.get
    let submittedBody: unknown
    api.post = (async (url: string, data: unknown) => {
      assert.equal(url, '/api/user/developer-access/request')
      submittedBody = data
      return { data: { success: true, data: directPendingRequest } }
    }) as typeof api.post

    const rendered = await renderTool()
    try {
      await act(async () => {
        findButton('Write request myself').click()
        await flushEffects()
      })
      const textarea = document.querySelector<HTMLTextAreaElement>('textarea')
      assert.ok(textarea)
      await setTextareaValue(textarea, 'I need L1 for a small test client.')

      await act(async () => {
        findButton('Submit for review').click()
        await flushEffects()
      })
      await waitForCondition(
        () => submittedBody !== undefined,
        'Direct request was not submitted'
      )
      assert.deepEqual(submittedBody, {
        reason: 'I need L1 for a small test client.',
        confirmed: true,
      })
      assert.match(
        document.body.textContent ?? '',
        /Access request submitted · Pending review/
      )
      assert.equal(document.querySelector('textarea'), null)
    } finally {
      await unmount(rendered)
    }
  })

  test('submits the exact AI recommendation only after confirmation', async () => {
    api.get = (async () => ({
      data: { success: true, data: null },
    })) as typeof api.get
    let submittedBody: unknown
    api.post = (async (url: string, data: unknown) => {
      assert.equal(url, '/api/user/developer-access/request')
      submittedBody = data
      return { data: { success: true, data: pendingRequest } }
    }) as typeof api.post

    let submittedCalls = 0
    const rendered = await renderTool({
      recommendationDraft,
      onSubmitted: () => {
        submittedCalls += 1
      },
    })
    try {
      assert.equal(document.querySelectorAll('textarea').length, 1)
      assert.match(
        document.body.textContent ?? '',
        /Recommend L1 because the user provided a specific client/
      )

      await act(async () => {
        findButton('Confirm and submit for review').click()
        await flushEffects()
      })
      await waitForCondition(
        () => submittedBody !== undefined,
        'Recommendation was not submitted'
      )
      assert.deepEqual(submittedBody, {
        reason: recommendationDraft.user_statement,
        ai_recommendation: recommendationDraft.recommendation,
        confirmation_token: recommendationDraft.confirmation_token,
        confirmed: true,
      })
      assert.equal(submittedCalls, 1)
      assert.match(
        document.body.textContent ?? '',
        /AI recommendation submitted · Pending review/
      )
      assert.equal(document.querySelector('textarea'), null)
    } finally {
      await unmount(rendered)
    }
  })

  test('switches an edited initial AI draft to the token-free manual path', async () => {
    api.get = (async () => ({
      data: { success: true, data: null },
    })) as typeof api.get
    let submittedBody: unknown
    api.post = (async (url: string, data: unknown) => {
      assert.equal(url, '/api/user/developer-access/request')
      submittedBody = data
      return { data: { success: true, data: pendingRequest } }
    }) as typeof api.post

    const rendered = await renderTool({ recommendationDraft })
    try {
      const textarea = document.querySelector<HTMLTextAreaElement>('textarea')
      assert.ok(textarea)
      await setTextareaValue(
        textarea,
        'A manually revised initial recommendation for my client.'
      )

      await act(async () => {
        findButton('Confirm and submit for review').click()
        await flushEffects()
      })
      await waitForCondition(
        () => submittedBody !== undefined,
        'Edited recommendation was not submitted'
      )
      assert.deepEqual(submittedBody, {
        reason: recommendationDraft.user_statement,
        ai_recommendation:
          'A manually revised initial recommendation for my client.',
        confirmation_token: undefined,
        confirmed: true,
      })
    } finally {
      await unmount(rendered)
    }
  })

  test('applies an AI revision to the one pending recommendation after confirmation', async () => {
    api.get = (async () => ({
      data: { success: true, data: pendingRequest },
    })) as typeof api.get
    let submittedBody: unknown
    api.post = (async (url: string, data: unknown) => {
      assert.equal(url, '/api/user/developer-access/request')
      submittedBody = data
      return {
        data: {
          success: true,
          data: {
            ...pendingRequest,
            ai_recommendation: recommendationDraft.recommendation,
          },
        },
      }
    }) as typeof api.post

    const rendered = await renderTool({ recommendationDraft })
    try {
      await waitForCondition(
        () =>
          document.querySelector<HTMLTextAreaElement>('textarea')?.value ===
          recommendationDraft.recommendation,
        'AI revision was not opened for confirmation'
      )

      await act(async () => {
        findButton('Save changes').click()
        await flushEffects()
      })
      await waitForCondition(
        () => submittedBody !== undefined,
        'AI revision was not submitted'
      )
      assert.deepEqual(submittedBody, {
        reason: pendingRequest.reason,
        ai_recommendation: recommendationDraft.recommendation,
        confirmation_token: recommendationDraft.confirmation_token,
        confirmed: true,
      })
      assert.match(
        document.body.textContent ?? '',
        /AI recommendation submitted · Pending review/
      )
    } finally {
      await unmount(rendered)
    }
  })

  test('switches an edited AI draft to the token-free manual path', async () => {
    api.get = (async () => ({
      data: { success: true, data: pendingRequest },
    })) as typeof api.get
    let submittedBody: unknown
    api.post = (async (url: string, data: unknown) => {
      assert.equal(url, '/api/user/developer-access/request')
      submittedBody = data
      return {
        data: {
          success: true,
          data: {
            ...pendingRequest,
            source: 'user_edited',
            ai_recommendation:
              'A manually revised recommendation from the user.',
          },
        },
      }
    }) as typeof api.post

    const rendered = await renderTool({ recommendationDraft })
    try {
      await waitForCondition(
        () => document.querySelector('textarea') !== null,
        'AI revision editor did not open'
      )
      const textarea = document.querySelector<HTMLTextAreaElement>('textarea')
      assert.ok(textarea)
      await setTextareaValue(
        textarea,
        'A manually revised recommendation from the user.'
      )
      await act(async () => {
        findButton('Save changes').click()
        await flushEffects()
      })
      await waitForCondition(
        () => submittedBody !== undefined,
        'Manual edit was not submitted'
      )
      assert.deepEqual(submittedBody, {
        reason: pendingRequest.reason,
        ai_recommendation: 'A manually revised recommendation from the user.',
        confirmation_token: undefined,
        confirmed: true,
      })
    } finally {
      await unmount(rendered)
    }
  })

  test('allows a pending recommendation letter to be cleared completely', async () => {
    api.get = (async () => ({
      data: { success: true, data: pendingRequest },
    })) as typeof api.get
    let submittedBody: unknown
    api.post = (async (url: string, data: unknown) => {
      assert.equal(url, '/api/user/developer-access/request')
      submittedBody = data
      return {
        data: {
          success: true,
          data: {
            ...pendingRequest,
            source: 'assistant_request',
            ai_recommendation: '',
          },
        },
      }
    }) as typeof api.post

    const rendered = await renderTool()
    try {
      const pendingStatus = document.querySelector(
        '[data-testid="assistant-pending-recommendation"]'
      )
      assert.ok(pendingStatus)
      assert.match(
        pendingStatus.textContent ?? '',
        /AI recommendation submitted · Pending review/
      )
      assert.equal(pendingStatus.querySelector('[data-slot="card"]'), null)
      assert.equal(document.querySelector('textarea'), null)

      await act(async () => {
        findButton('Edit').click()
        await flushEffects()
      })
      const textarea = document.querySelector<HTMLTextAreaElement>('textarea')
      assert.ok(textarea)
      await setTextareaValue(textarea, '')
      assert.equal(textarea.value, '')
      assert.equal(findButton('Save changes').disabled, false)

      await act(async () => {
        findButton('Save changes').click()
        await flushEffects()
      })
      await waitForCondition(
        () => submittedBody !== undefined,
        'Cleared recommendation was not saved'
      )
      assert.deepEqual(submittedBody, {
        reason: pendingRequest.reason,
        confirmed: true,
      })
      assert.equal(document.querySelector('textarea'), null)
      assert.match(
        document.querySelector(
          '[data-testid="assistant-pending-recommendation"]'
        )?.textContent ?? '',
        /Access request submitted · Pending review/
      )

      await act(async () => {
        findButton('Edit').click()
        await flushEffects()
      })
      const reopenedTextarea =
        document.querySelector<HTMLTextAreaElement>('textarea')
      assert.ok(reopenedTextarea)
      assert.equal(reopenedTextarea.value, '')
    } finally {
      await unmount(rendered)
    }
  })

  test('manual edits never reuse an AI confirmation token', async () => {
    api.get = (async () => ({
      data: { success: true, data: pendingRequest },
    })) as typeof api.get
    const submittedBodies: unknown[] = []
    api.post = (async (_url: string, data: unknown) => {
      submittedBodies.push(data)
      return {
        data: {
          success: true,
          data: {
            ...pendingRequest,
            ai_recommendation:
              'A manually revised recommendation that remains editable.',
          },
        },
      }
    }) as typeof api.post

    const rendered = await renderTool({ recommendationDraft })
    try {
      await waitForCondition(
        () => document.querySelector('textarea') !== null,
        'AI revision editor did not open'
      )
      await act(async () => {
        findButton('Save changes').click()
        await flushEffects()
      })
      await waitForCondition(
        () => submittedBodies.length === 1,
        'AI revision was not saved'
      )

      await act(async () => {
        findButton('Edit').click()
        await flushEffects()
      })
      const textarea = document.querySelector<HTMLTextAreaElement>('textarea')
      assert.ok(textarea)
      await setTextareaValue(
        textarea,
        'A manually revised recommendation that remains editable.'
      )
      await act(async () => {
        findButton('Save changes').click()
        await flushEffects()
      })
      await waitForCondition(
        () => submittedBodies.length === 2,
        'Manual revision was not saved'
      )

      assert.deepEqual(submittedBodies[0], {
        reason: pendingRequest.reason,
        ai_recommendation: recommendationDraft.recommendation,
        confirmation_token: recommendationDraft.confirmation_token,
        confirmed: true,
      })
      assert.deepEqual(submittedBodies[1], {
        reason: pendingRequest.reason,
        ai_recommendation:
          'A manually revised recommendation that remains editable.',
        confirmation_token: undefined,
        confirmed: true,
      })
    } finally {
      await unmount(rendered)
    }
  })

  test('recovers an expired AI edit as a token-free manual edit', async () => {
    api.get = (async () => ({
      data: { success: true, data: pendingRequest },
    })) as typeof api.get
    const submittedBodies: unknown[] = []
    api.post = (async (_url: string, data: unknown) => {
      submittedBodies.push(data)
      if (submittedBodies.length === 1) {
        throw {
          response: {
            data: { code: 'DEVELOPER_ACCESS_AI_CONFIRMATION_INVALID' },
          },
        }
      }
      return {
        data: {
          success: true,
          data: {
            ...pendingRequest,
            ai_recommendation: recommendationDraft.recommendation,
          },
        },
      }
    }) as typeof api.post
    let consumed = 0

    const rendered = await renderTool({
      recommendationDraft,
      onDraftConsumed: () => {
        consumed += 1
      },
    })
    try {
      await waitForCondition(
        () => document.querySelector('textarea') !== null,
        'AI revision editor did not open'
      )
      await act(async () => {
        findButton('Save changes').click()
        await flushEffects()
      })
      assert.equal(consumed, 1)

      await act(async () => {
        findButton('Save changes').click()
        await flushEffects()
      })
      await waitForCondition(
        () => submittedBodies.length === 2,
        'Recovered manual edit was not submitted'
      )
      assert.deepEqual(submittedBodies[1], {
        reason: pendingRequest.reason,
        ai_recommendation: recommendationDraft.recommendation,
        confirmation_token: undefined,
        confirmed: true,
      })
    } finally {
      await unmount(rendered)
    }
  })

  test('keeps a cleared recommendation empty after remounting the pending request', async () => {
    api.get = (async () => ({
      data: { success: true, data: directPendingRequest },
    })) as typeof api.get

    for (let mount = 0; mount < 2; mount += 1) {
      const rendered = await renderTool()
      try {
        assert.match(
          document.querySelector(
            '[data-testid="assistant-pending-recommendation"]'
          )?.textContent ?? '',
          /Access request submitted · Pending review/
        )

        await act(async () => {
          findButton('Edit').click()
          await flushEffects()
        })
        const textarea = document.querySelector<HTMLTextAreaElement>('textarea')
        assert.ok(textarea)
        assert.equal(textarea.value, '')
      } finally {
        await unmount(rendered)
      }
    }
  })

  test('cancels a pending recommendation edit without changing its one stored letter', async () => {
    api.get = (async () => ({
      data: { success: true, data: pendingRequest },
    })) as typeof api.get
    let submittedCalls = 0
    api.post = (async () => {
      submittedCalls += 1
      throw new Error('Cancel must not submit the recommendation')
    }) as typeof api.post

    const rendered = await renderTool()
    try {
      await act(async () => {
        findButton('Edit').click()
        await flushEffects()
      })
      let textarea = document.querySelector<HTMLTextAreaElement>('textarea')
      assert.ok(textarea)
      assert.equal(textarea.value, pendingRequest.ai_recommendation)
      await setTextareaValue(textarea, 'A local edit that should be discarded.')

      await act(async () => {
        findButton('Cancel').click()
        await flushEffects()
      })
      assert.equal(document.querySelector('textarea'), null)
      assert.equal(submittedCalls, 0)

      await act(async () => {
        findButton('Edit').click()
        await flushEffects()
      })
      textarea = document.querySelector<HTMLTextAreaElement>('textarea')
      assert.ok(textarea)
      assert.equal(textarea.value, pendingRequest.ai_recommendation)
    } finally {
      await unmount(rendered)
    }
  })

  test('refreshes a pending request and opens setup after approval', async () => {
    let getCalls = 0
    api.get = (async (url: string) => {
      assert.equal(url, '/api/user/developer-access/request')
      getCalls += 1
      return {
        data: {
          success: true,
          data: getCalls === 1 ? pendingRequest : approvedRequest,
        },
      }
    }) as typeof api.get

    let continueCalls = 0
    const rendered = await renderTool({
      onContinueSetup: () => {
        continueCalls += 1
      },
    })
    try {
      await waitForCondition(
        () =>
          document.body.textContent?.includes('AI recommendation submitted') ===
          true,
        'Pending recommendation state did not render'
      )
      assert.equal(getCalls, 1)

      await act(async () => {
        await rendered.queryClient.refetchQueries({
          queryKey: ['assistant-developer-access-request'],
        })
        await flushEffects()
      })
      await waitForCondition(
        () =>
          document.body.textContent?.includes('L1 access approved') === true,
        'Approved state did not render after refresh'
      )
      assert.equal(getCalls, 2)
      assert.match(document.body.textContent ?? '', /Approved for testing\./)

      await act(async () => {
        findButton('Continue setup').click()
        await flushEffects()
      })
      assert.equal(continueCalls, 1)
    } finally {
      await unmount(rendered)
    }
  })

  for (const reviewedRequest of [approvedRequest, rejectedRequest]) {
    test(`lets polling replace a locally submitted pending request with ${reviewedRequest.status}`, async () => {
      let getCalls = 0
      api.get = (async (url: string) => {
        assert.equal(url, '/api/user/developer-access/request')
        getCalls += 1
        return {
          data: {
            success: true,
            data: getCalls === 1 ? null : reviewedRequest,
          },
        }
      }) as typeof api.get
      api.post = (async () => ({
        data: { success: true, data: directPendingRequest },
      })) as typeof api.post

      const rendered = await renderTool()
      try {
        await act(async () => {
          findButton('Write request myself').click()
          await flushEffects()
        })
        const textarea = document.querySelector<HTMLTextAreaElement>('textarea')
        assert.ok(textarea)
        await setTextareaValue(textarea, 'I need L1 for a small test client.')
        await act(async () => {
          findButton('Submit for review').click()
          await flushEffects()
        })
        await waitForCondition(
          () =>
            document.body.textContent?.includes('Access request submitted') ===
            true,
          'Locally submitted pending request did not render'
        )

        await act(async () => {
          await rendered.queryClient.refetchQueries({
            queryKey: ['assistant-developer-access-request'],
          })
          await flushEffects()
        })
        await waitForCondition(
          () =>
            document.body.textContent?.includes(
              reviewedRequest.status === 'approved'
                ? 'L1 access approved'
                : 'Previous request rejected'
            ) === true,
          `Polled ${reviewedRequest.status} request did not replace local pending state`
        )
        assert.match(
          document.body.textContent ?? '',
          new RegExp(reviewedRequest.admin_note.replaceAll('.', '\\.'))
        )
      } finally {
        await unmount(rendered)
      }
    })
  }

  test('shows the administrator reply and keeps the direct form after rejection', async () => {
    api.get = (async () => ({
      data: { success: true, data: rejectedRequest },
    })) as typeof api.get

    const rendered = await renderTool()
    try {
      assert.equal(document.querySelector('textarea'), null)
      assert.match(document.body.textContent ?? '', /Previous request rejected/)
      assert.match(
        document.body.textContent ?? '',
        /Please explain which client you will connect\./
      )
      assert.match(
        document.body.textContent ?? '',
        /Continue the conversation and address the administrator feedback/
      )
      assert.ok(findButton('Write request myself'))
    } finally {
      await unmount(rendered)
    }
  })

  test('wraps long administrator replies for narrow screens', async () => {
    const adminNote =
      'https://console.example.test/review/this-is-a-very-long-administrator-note-without-spaces-that-must-wrap-on-mobile'
    api.get = (async () => ({
      data: {
        success: true,
        data: { ...rejectedRequest, admin_note: adminNote },
      },
    })) as typeof api.get

    const rendered = await renderTool()
    try {
      const reply = [...document.querySelectorAll('p')].find(
        (node) => node.textContent === adminNote
      )
      assert.ok(reply)
      assert.match(reply.className, /break-words/)
      assert.match(reply.className, /\[overflow-wrap:anywhere\]/)
    } finally {
      await unmount(rendered)
    }
  })
})
