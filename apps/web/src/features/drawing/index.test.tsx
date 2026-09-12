/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, describe, test } from 'node:test'

import { IDBFactory } from 'fake-indexeddb'
import { Window } from 'happy-dom'

const domWindow = new Window({
  url: 'https://console.example.test/drawing',
  width: 390,
  height: 844,
})
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
  'HTMLButtonElement',
  'HTMLInputElement',
  'HTMLSelectElement',
  'HTMLTextAreaElement',
  'SVGElement',
  'customElements',
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

const matchMedia = ((query: string) => ({
  matches: false,
  media: query,
  onchange: null,
  addListener: () => undefined,
  removeListener: () => undefined,
  addEventListener: () => undefined,
  removeEventListener: () => undefined,
  dispatchEvent: () => false,
})) as unknown as typeof domWindow.matchMedia
Object.defineProperty(domWindow, 'matchMedia', {
  configurable: true,
  value: matchMedia,
})
Object.defineProperty(globalThis, 'matchMedia', {
  configurable: true,
  value: matchMedia,
})

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createRouter, createRootRoute, createMemoryHistory, RouterProvider } =
  await import('@tanstack/react-router')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { Drawing } = await import('./index')
const { useAuthStore } = await import('@/stores/auth-store')
const { createDrawingHistoryStore } = await import('./history-storage')
const { resetDrawingTaskState } = await import('./drawing-task-state')
const originalFetch = globalThis.fetch
const originalConfirm = window.confirm

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

const pricing = {
  success: true,
  data: [
    {
      id: 1,
      model_name: 'image-2',
      quota_type: 1 as const,
      model_ratio: 1,
      completion_ratio: 1,
      enable_groups: ['mobile-image-group'],
      supported_endpoint_types: ['image-generation'],
    },
  ],
  vendors: [],
  group_ratio: {},
  usable_group: {
    'mobile-image-group': {
      desc: 'A long routing description that must stay inside a 390 pixel mobile control.',
      ratio: 1,
    },
  },
  supported_endpoint: {},
  auto_groups: [],
}

async function flushEffects() {
  await new Promise((resolve) => setTimeout(resolve, 20))
}

async function waitForCondition(
  condition: () => boolean,
  failureMessage: string
) {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    if (condition()) return
    await flushEffects()
  }
  throw new Error(failureMessage)
}

// Finish each act before checking the DOM: a single outer act can defer React's
// commit until after the predicate times out. Poll observable state, bounded by
// the same attempt budget as the other asynchronous workbench checks.
async function waitForDrawingState(
  condition: () => boolean | Promise<boolean>,
  failureMessage: string
) {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    await act(flushEffects)
    let ready = false
    await act(async () => {
      ready = await condition()
    })
    if (ready) return
  }
  throw new Error(failureMessage)
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

async function renderDrawing() {
  const router = createRouter({
    routeTree: createRootRoute({ component: Drawing }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  const get = api.get
  api.get = (async (url: string, config?: Parameters<typeof api.get>[1]) => {
    if (url === '/api/user/self') {
      return { data: { success: true, data: { quota: 5000000 } } }
    }
    const response = await get(url, config)
    if (
      url === '/api/assistant/status' &&
      response.data?.success &&
      !Object.hasOwn(response.data.data ?? {}, 'drawing_web_access')
    ) {
      response.data.data = {
        ...response.data.data,
        drawing_web_access: {
          minimum_balance_usd: 10,
          balance_usd: 10,
          allowed: true,
        },
      }
    }
    return response
  }) as typeof api.get
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
          <RouterProvider router={router} />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flushEffects()
  })
  await act(flushEffects)
  return { container, queryClient, root }
}

beforeEach(() => {
  resetDrawingTaskState()
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'drawing-user', role: 10 })
  Object.defineProperty(globalThis, 'indexedDB', {
    configurable: true,
    writable: true,
    value: new IDBFactory(),
  })
  globalThis.fetch = (() => {
    throw new Error('Unexpected network fetch in drawing tests')
  }) as typeof fetch
  window.confirm = () => true
})

afterEach(() => {
  resetDrawingTaskState()
  globalThis.fetch = originalFetch
  window.confirm = originalConfirm
  useAuthStore.getState().auth.reset()
  api.get = originalGet
  api.post = originalPost
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('Drawing mobile controls', () => {
  test('keeps every native select full width with long group descriptions', async () => {
    api.get = (async (url: string) => {
      if (url === '/api/assistant/status') {
        return {
          data: {
            success: true,
            data: {
              enabled: true,
              model: 'assistant-test',
              developer_access_granted: true,
              funding: { mode: 'super_administrator' },
            },
          },
        }
      }
      if (url === '/api/pricing') return { data: pricing }
      if (url === '/api/user/self/groups') {
        return {
          data: {
            success: true,
            data: pricing.usable_group,
          },
        }
      }
      throw new Error(`unexpected GET ${url}`)
    }) as typeof api.get

    const rendered = await renderDrawing()
    try {
      await act(
        async () =>
          await waitForCondition(
            () => rendered.container.querySelectorAll('select').length === 5,
            'drawing controls did not render'
          )
      )

      const selects = rendered.container.querySelectorAll('select')
      assert.equal(selects.length, 5)
      for (const select of selects) {
        const wrapper = select.closest<HTMLElement>(
          '[data-slot="native-select-wrapper"]'
        )
        assert.ok(wrapper)
        assert.match(wrapper.className, /\bw-full\b/)
        assert.doesNotMatch(wrapper.className, /\bw-fit\b/)
        assert.match(select.className, /\bh-11\b/)
        assert.match(select.className, /\bsm:h-8\b/)
      }

      const composer = rendered.container.querySelector(
        '[data-slot="drawing-composer"]'
      )
      const inspector = rendered.container.querySelector(
        '[data-slot="drawing-inspector"]'
      )
      assert.ok(composer)
      assert.ok(inspector)
      assert.ok(composer.querySelector('#drawing-reference-images'))
      assert.match(composer.textContent ?? '', /Generate image/)
      assert.doesNotMatch(inspector.textContent ?? '', /Generate image/)
      assert.doesNotMatch(
        rendered.container.innerHTML,
        /radial-gradient\(circle/
      )
    } finally {
      await act(async () => rendered.root.unmount())
      rendered.queryClient.clear()
    }
  })

  test('keeps MCP configuration out of the primary workflow until requested', async () => {
    api.get = (async (url: string) => {
      if (url === '/api/assistant/status') {
        return {
          data: {
            success: true,
            data: {
              enabled: true,
              model: 'assistant-test',
              developer_access_granted: true,
              funding: { mode: 'super_administrator' },
            },
          },
        }
      }
      if (url === '/api/pricing') return { data: pricing }
      if (url === '/api/user/self/groups') {
        return {
          data: {
            success: true,
            data: pricing.usable_group,
          },
        }
      }
      throw new Error(`unexpected GET ${url}`)
    }) as typeof api.get

    const rendered = await renderDrawing()
    try {
      await act(
        async () =>
          await waitForCondition(
            () => rendered.container.querySelectorAll('select').length === 5,
            'drawing controls did not render'
          )
      )

      assert.equal(
        rendered.container.querySelector('#drawing-mcp-endpoint'),
        null
      )
      const mcpButton = [...rendered.container.querySelectorAll('button')].find(
        (button) => button.textContent?.includes('Drawing MCP')
      )
      assert.ok(mcpButton)
      assert.equal(mcpButton.getAttribute('aria-expanded'), 'false')

      await act(async () => {
        mcpButton.click()
        await flushEffects()
      })

      assert.ok(rendered.container.querySelector('#drawing-mcp-endpoint'))
      assert.equal(mcpButton.getAttribute('aria-expanded'), 'true')
    } finally {
      await act(async () => rendered.root.unmount())
      rendered.queryClient.clear()
    }
  })

  test('surfaces an error and keeps generation available when no preview is usable', async () => {
    api.get = (async (url: string) => {
      if (url === '/api/assistant/status') {
        return {
          data: {
            success: true,
            data: {
              enabled: true,
              model: 'assistant-test',
              developer_access_granted: true,
              funding: { mode: 'super_administrator' },
            },
          },
        }
      }
      if (url === '/api/pricing') return { data: pricing }
      if (url === '/api/user/self/groups') {
        return {
          data: {
            success: true,
            data: pricing.usable_group,
          },
        }
      }
      throw new Error(`unexpected GET ${url}`)
    }) as typeof api.get
    let postCalls = 0
    api.post = (async (url: string) => {
      postCalls += 1
      assert.equal(url, '/pg/images/generations?group=mobile-image-group')
      return {
        data: {
          data: [{ url: '  ', b64_json: '\n', revised_prompt: 'No source' }],
        },
      }
    }) as typeof api.post

    const rendered = await renderDrawing()
    try {
      await act(
        async () =>
          await waitForCondition(
            () => rendered.container.querySelectorAll('select').length === 5,
            'drawing controls did not render'
          )
      )
      const textarea = rendered.container.querySelector('textarea')
      assert.ok(textarea)
      await setTextareaValue(textarea, 'A quiet mountain lake at sunrise')

      const generateButton = [
        ...rendered.container.querySelectorAll('button'),
      ].find((button) => button.textContent?.includes('Generate image'))
      assert.ok(generateButton)
      assert.equal(generateButton.disabled, false)
      await act(async () => {
        generateButton.click()
        await flushEffects()
      })

      assert.equal(postCalls, 1)
      assert.match(
        rendered.container.textContent ?? '',
        /No images were returned/
      )
      assert.equal(rendered.container.querySelectorAll('figure').length, 0)
      assert.ok(generateButton.disabled === false)
    } finally {
      await act(async () => rendered.root.unmount())
      rendered.queryClient.clear()
    }
  })
})

describe('Drawing generation failures', () => {
  const cases = [
    {
      name: 'shows the relay detail as text when generation returns 503',
      reject: true,
      response: {
        status: 503,
        data: {
          error: {
            message:
              'No available channel for <image-2> (request id: request-123)',
          },
        },
      },
      expected: 'No available channel for <image-2> (request id: request-123)',
    },
    {
      name: 'uses a readable fallback for an HTML gateway outage',
      reject: true,
      response: { status: 503, data: '<html>Service Unavailable</html>' },
      expected: 'Please try again later.',
    },
    {
      name: 'explains network failures without exposing the Axios message',
      reject: true,
      response: undefined,
      expected: 'Network connection failed or server not responding',
    },
    {
      name: 'retains business error details from successful HTTP responses',
      reject: false,
      response: { status: 200, data: { message: 'Image quota exhausted' } },
      expected: 'Image quota exhausted',
    },
    {
      name: 'handles an empty successful response without a JavaScript error',
      reject: false,
      response: { status: 200, data: null },
      expected: 'Unable to generate the image',
    },
  ]

  for (const scenario of cases) {
    test(scenario.name, async () => {
      api.get = (async (url: string) => {
        if (url === '/api/assistant/status') {
          return {
            data: {
              success: true,
              data: { developer_access_granted: true },
            },
          }
        }
        if (url === '/api/pricing') return { data: pricing }
        if (url === '/api/user/self/groups') {
          return { data: { success: true, data: pricing.usable_group } }
        }
        throw new Error(`unexpected GET ${url}`)
      }) as typeof api.get
      let postCalls = 0
      api.post = (async () => {
        postCalls += 1
        if (scenario.reject) {
          throw Object.assign(
            new Error('Request failed with status code 503'),
            {
              response: scenario.response,
            }
          )
        }
        return scenario.response
      }) as typeof api.post

      const rendered = await renderDrawing()
      try {
        await act(
          async () =>
            await waitForCondition(
              () => rendered.container.querySelectorAll('select').length === 5,
              'drawing controls did not render'
            )
        )
        const textarea = rendered.container.querySelector('textarea')
        assert.ok(textarea)
        await setTextareaValue(textarea, 'A quiet mountain lake at sunrise')
        const generateButton = [
          ...rendered.container.querySelectorAll('button'),
        ].find((button) => button.textContent?.includes('Generate image'))
        assert.ok(generateButton)
        assert.equal(generateButton.disabled, false)

        await act(async () => {
          generateButton.click()
          await flushEffects()
        })

        assert.equal(
          postCalls,
          1,
          'paid generation must not retry automatically'
        )
        assert.ok(rendered.container.textContent?.includes(scenario.expected))
        assert.doesNotMatch(
          rendered.container.textContent ?? '',
          /Request failed with status code|Cannot read properties/
        )
        assert.equal(rendered.container.querySelector('image-2'), null)
        assert.equal(textarea.value, 'A quiet mountain lake at sunrise')
        assert.equal(generateButton.disabled, false)
      } finally {
        await act(async () => rendered.root.unmount())
        rendered.queryClient.clear()
      }
    })
  }
})

function mockWorkbench(balance: () => number | null, modelName = 'image-2') {
  let statusReads = 0
  api.get = (async (url: string) => {
    if (url === '/api/assistant/status') {
      statusReads++
      const current = balance()
      return {
        data: {
          success: true,
          data: {
            developer_access_granted: true,
            drawing_web_access: {
              minimum_balance_usd: 10,
              balance_usd: current,
              allowed: current !== null && current >= 10,
            },
          },
        },
      }
    }
    if (url === '/api/pricing') {
      return {
        data: {
          ...pricing,
          data: pricing.data.map((model) => ({
            ...model,
            model_name: modelName,
          })),
        },
      }
    }
    if (url === '/api/user/self/groups') {
      return { data: { success: true, data: pricing.usable_group } }
    }
    throw new Error(`Unexpected GET ${url}`)
  }) as typeof api.get
  return () => statusReads
}

function button(container: HTMLElement, text: string) {
  const target = [...container.querySelectorAll('button')].find((entry) =>
    entry.textContent?.includes(text)
  )
  assert.ok(target, `Missing button: ${text}`)
  return target
}

async function promptDrawing(container: HTMLElement) {
  const input = container.querySelector<HTMLTextAreaElement>(
    '#drawing-prompt-input'
  )
  assert.ok(input)
  await setTextareaValue(input, 'A stored painting')
}

const png =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl6aQAAAABJRU5ErkJggg=='

describe('Drawing balance and browser history', () => {
  for (const balance of [9.99, 10, null]) {
    test(`balance ${balance} gates only web generation while key/MCP controls remain available`, async () => {
      mockWorkbench(() => balance)
      let keyCalls = 0
      api.post = (async (url: string, body: unknown) => {
        assert.equal(url, '/api/assistant/drawing/key')
        assert.deepEqual(body, {})
        keyCalls++
        return {
          data: {
            success: true,
            data: { id: 7, name: 'Drawing', group: 'image-2', created: true },
          },
        }
      }) as typeof api.post
      const rendered = await renderDrawing()
      try {
        await promptDrawing(rendered.container)
        assert.equal(
          button(rendered.container, 'Generate image').disabled,
          balance !== 10
        )
        const alert = rendered.container.querySelector(
          '[data-slot="drawing-web-access"]'
        )
        if (balance === 10) assert.equal(alert, null)
        else {
          assert.ok(alert)
          assert.match(alert.textContent ?? '', /USD 10.00/)
          assert.match(
            alert.textContent ?? '',
            /API and MCP usage is billed normally, not free/
          )
          if (balance === null) {
            assert.match(alert.textContent ?? '', /balance unavailable/)
            assert.doesNotMatch(
              alert.textContent ?? '',
              /Current balance: USD 0/
            )
          } else {
            assert.match(
              alert.textContent ?? '',
              /Insufficient balance for web image generation/
            )
            assert.match(alert.textContent ?? '', /Current balance: USD 9.99/)
          }
        }
        assert.equal(
          button(rendered.container, 'Prepare image-2 API Key').disabled,
          false
        )
        assert.equal(button(rendered.container, 'Drawing MCP').disabled, false)
        await act(async () => {
          button(rendered.container, 'Prepare image-2 API Key').click()
          button(rendered.container, 'Drawing MCP').click()
          await flushEffects()
        })
        assert.equal(keyCalls, 1)
        assert.match(
          rendered.container.textContent ?? '',
          /image-2 API Key ready/
        )
        assert.ok(rendered.container.querySelector('a[href="/keys"]'))
        assert.ok(rendered.container.querySelector('#drawing-mcp-endpoint'))
      } finally {
        await act(async () => rendered.root.unmount())
        rendered.queryClient.clear()
      }
    })
  }

  test('exact USD 10 generates once, refreshes balance and keeps downloaded history available below the floor', async () => {
    let balance = 10
    const statusReads = mockWorkbench(() => balance)
    let calls = 0
    api.post = (async () => {
      calls++
      balance = 9.5
      return { data: { data: [{ b64_json: png }] } }
    }) as typeof api.post
    let rendered = await renderDrawing()
    try {
      await promptDrawing(rendered.container)
      await act(async () => {
        button(rendered.container, 'Generate image').click()
        await flushEffects()
      })
      await act(async () =>
        waitForCondition(
          () =>
            Boolean(rendered.container.querySelector('a[download]')) &&
            !rendered.container.textContent?.includes('Saving image bytes'),
          'image bytes were not saved'
        )
      )
      assert.equal(calls, 1)
      assert.ok(statusReads() >= 2)
      assert.equal(button(rendered.container, 'Generate image').disabled, true)
      assert.match(
        rendered.container.textContent ?? '',
        /Current balance: USD 9.50/
      )
      const stored = await createDrawingHistoryStore().load(1)
      assert.equal(stored.images.length, 1)
      assert.equal(stored.images[0].blob.size, atob(png).length)
      const firstURL = rendered.container
        .querySelector('figure img')
        ?.getAttribute('src')
      await act(async () => rendered.root.unmount())
      rendered.queryClient.clear()
      rendered = await renderDrawing()
      await act(async () =>
        waitForCondition(
          () => Boolean(rendered.container.querySelector('a[download]')),
          'history did not survive refresh'
        )
      )
      assert.notEqual(
        rendered.container.querySelector('figure img')?.getAttribute('src'),
        firstURL
      )
      assert.match(rendered.container.textContent ?? '', /A stored painting/)
      assert.equal(calls, 1)
      await act(async () => {
        button(rendered.container, 'Clear image history').click()
        await flushEffects()
      })
      assert.equal(rendered.container.querySelectorAll('figure').length, 0)
      assert.equal((await createDrawingHistoryStore().load(1)).images.length, 0)
      assert.equal(calls, 1)
    } finally {
      await act(async () => rendered.root.unmount())
      rendered.queryClient.clear()
    }
  })

  test('server denial produces a persistent web-only alert rather than a generation retry', async () => {
    let balance = 12
    mockWorkbench(() => balance)
    let calls = 0
    api.post = (async () => {
      calls++
      balance = 8
      throw {
        response: {
          status: 403,
          data: {
            error: {
              code: 'WEB_DRAWING_MINIMUM_BALANCE',
              message: 'server threshold',
            },
            drawing_web_access: {
              minimum_balance_usd: 10,
              balance_usd: 8,
              allowed: false,
            },
          },
        },
      }
    }) as typeof api.post
    const rendered = await renderDrawing()
    try {
      await promptDrawing(rendered.container)
      await act(async () => {
        button(rendered.container, 'Generate image').click()
        await flushEffects()
      })
      assert.match(
        rendered.container.textContent ?? '',
        /Insufficient balance for web image generation/
      )
      assert.match(
        rendered.container.textContent ?? '',
        /Current balance: USD 8.00/
      )
      assert.doesNotMatch(
        rendered.container.textContent ?? '',
        /Request failed/
      )
      assert.equal(button(rendered.container, 'Generate image').disabled, true)
      assert.equal(button(rendered.container, 'Drawing MCP').disabled, false)
      assert.equal(calls, 1)
    } finally {
      await act(async () => rendered.root.unmount())
      rendered.queryClient.clear()
    }
  })

  test('cache failure never turns generation success into Request failed or repeats the paid request', async () => {
    mockWorkbench(() => 20)
    let calls = 0
    api.post = (async () => {
      calls++
      Object.defineProperty(globalThis, 'indexedDB', {
        configurable: true,
        value: undefined,
      })
      return { data: { data: [{ b64_json: png }] } }
    }) as typeof api.post
    const rendered = await renderDrawing()
    try {
      await promptDrawing(rendered.container)
      await act(async () => {
        button(rendered.container, 'Generate image').click()
        await flushEffects()
      })
      assert.ok(rendered.container.querySelector('a[download]'))
      assert.match(
        rendered.container.textContent ?? '',
        /Generation succeeded, but some image bytes could not be saved/
      )
      assert.match(
        rendered.container.textContent ?? '',
        /Not saved in this browser/
      )
      assert.doesNotMatch(
        rendered.container.textContent ?? '',
        /Request failed/
      )
      assert.equal(calls, 1)
    } finally {
      await act(async () => rendered.root.unmount())
      rendered.queryClient.clear()
    }
  })

  test('account switch and logout cannot display or save a late response for a previous user', async () => {
    mockWorkbench(() => 20)
    let finish: (value: unknown) => void = () => {
      throw new Error('Generation did not start')
    }
    api.post = (() =>
      new Promise<unknown>((resolve) => {
        finish = resolve
      })) as typeof api.post
    const rendered = await renderDrawing()
    try {
      await promptDrawing(rendered.container)
      await act(async () => {
        button(rendered.container, 'Generate image').click()
        await flushEffects()
        useAuthStore
          .getState()
          .auth.setUser({ id: 2, username: 'second-user', role: 10 })
      })
      await act(async () => {
        finish({ data: { data: [{ b64_json: png }] } })
        await flushEffects()
      })
      assert.equal(rendered.container.querySelectorAll('figure').length, 0)
      assert.equal(
        rendered.container.querySelector<HTMLTextAreaElement>(
          '#drawing-prompt-input'
        )?.value,
        ''
      )
      assert.equal((await createDrawingHistoryStore().load(1)).images.length, 0)
      assert.equal((await createDrawingHistoryStore().load(2)).images.length, 0)
      await act(async () => useAuthStore.getState().auth.reset())
      assert.equal(rendered.container.textContent, '')
    } finally {
      await act(async () => rendered.root.unmount())
      rendered.queryClient.clear()
    }
  })
})

describe('Drawing wait experience, cancel control, and prompt draft restoration', () => {
  for (const returnBeforeCompletion of [true, false]) {
    test(`persists a request across navigation (return before completion: ${returnBeforeCompletion})`, async () => {
      mockWorkbench(() => 20)
      let resolvePost!: (value: unknown) => void
      let calls = 0
      let signal: AbortSignal | undefined
      api.post = ((
        _url: string,
        _body: unknown,
        config: { signal?: AbortSignal }
      ) => {
        calls++
        signal = config.signal
        return new Promise<unknown>((resolve) => {
          resolvePost = resolve
        })
      }) as typeof api.post
      let rendered = await renderDrawing()
      let mounted = true
      const unmount = async () => {
        if (!mounted) return
        await act(async () => rendered.root.unmount())
        rendered.queryClient.clear()
        mounted = false
      }
      try {
        await promptDrawing(rendered.container)
        await act(async () => {
          button(rendered.container, 'Generate image').click()
          await flushEffects()
          button(rendered.container, 'Stop waiting').click()
          await flushEffects()
        })
        assert.equal(signal?.aborted, false)
        await unmount()
        if (returnBeforeCompletion) {
          rendered = await renderDrawing()
          mounted = true
          assert.equal(
            button(rendered.container, 'Generate image').disabled,
            true
          )
        }
        await act(async () => {
          resolvePost({ data: { data: [{ b64_json: png }] } })
        })
        await waitForDrawingState(
          async () =>
            (await createDrawingHistoryStore().load(1)).images.length === 1,
          'background generation did not persist its image'
        )
        if (!returnBeforeCompletion) {
          rendered = await renderDrawing()
          mounted = true
        }
        const historyRestored = () =>
          Boolean(
            rendered.container.querySelector(
              '[aria-label="Image history"] img[alt="A stored painting"]'
            )
          ) && !button(rendered.container, 'Generate image').disabled
        await waitForDrawingState(
          historyRestored,
          'saved image did not appear after navigation'
        )
        assert.equal(
          button(rendered.container, 'Generate image').disabled,
          false
        )
        assert.equal(calls, 1)
        await unmount()
        // Fresh session restores from IndexedDB rather than an old component.
        rendered = await renderDrawing()
        mounted = true
        await waitForDrawingState(
          historyRestored,
          'fresh session did not restore image history'
        )
      } finally {
        await unmount()
      }
    })
  }

  test('shows truthful wait status and allows stopping waiting while keeping prompt', async () => {
    mockWorkbench(() => 20)
    let resolvePost: ((value: unknown) => void) | null = null
    api.post = (() =>
      new Promise<unknown>((resolve) => {
        resolvePost = resolve
      })) as typeof api.post

    const rendered = await renderDrawing()
    try {
      await promptDrawing(rendered.container)
      const promptInput = rendered.container.querySelector<HTMLTextAreaElement>(
        '#drawing-prompt-input'
      )
      assert.ok(promptInput)
      await setTextareaValue(promptInput, 'A cyberpunk neon cat in rain')

      await act(async () => {
        button(rendered.container, 'Generate image').click()
        await flushEffects()
      })

      // Truthful waiting state: shows Request submitted · Waiting and does NOT show contradictory text
      assert.match(
        rendered.container.textContent ?? '',
        /Request submitted · Waiting/
      )
      assert.doesNotMatch(
        rendered.container.textContent ?? '',
        /Your request is ready to run/
      )

      // Stop waiting button is clearly present
      const stopBtn = button(rendered.container, 'Stop waiting')
      assert.ok(stopBtn)

      // Click Stop waiting
      await act(async () => {
        stopBtn.click()
        await flushEffects()
      })

      // Stops waiting and displays clear explanation
      assert.match(
        rendered.container.textContent ?? '',
        /Generation continues in this tab/
      )

      // Prompt is preserved!
      assert.equal(promptInput.value, 'A cyberpunk neon cat in rain')

      // The paid request is still in flight, so duplicate submission is blocked.
      const genBtn = button(rendered.container, 'Generate image')
      assert.ok(genBtn)
      assert.equal(genBtn.disabled, true)

      const resolver = resolvePost as ((value: unknown) => void) | null
      if (resolver) {
        await act(async () => {
          resolver({ data: { data: [{ b64_json: png }] } })
          await flushEffects()
        })
        assert.equal(
          (await createDrawingHistoryStore().load(1)).images.length,
          1
        )
        assert.equal(
          button(rendered.container, 'Generate image').disabled,
          false
        )
        assert.doesNotMatch(
          rendered.container.textContent ?? '',
          /Generation continues in this tab/
        )
      }
    } finally {
      await act(async () => rendered.root.unmount())
      rendered.queryClient.clear()
    }
  })

  test('restores saved prompt draft upon mounting', async () => {
    mockWorkbench(() => 20)
    const { saveDrawingDraft } = await import('./drawing-task-state')
    saveDrawingDraft(1, { prompt: 'Persisted draft of a red lighthouse' })

    const rendered = await renderDrawing()
    try {
      await act(
        async () =>
          await waitForCondition(
            () => rendered.container.querySelectorAll('select').length === 5,
            'drawing controls did not render'
          )
      )
      const promptInput = rendered.container.querySelector<HTMLTextAreaElement>(
        '#drawing-prompt-input'
      )
      assert.ok(promptInput)
      assert.equal(promptInput.value, 'Persisted draft of a red lighthouse')
    } finally {
      await act(async () => rendered.root.unmount())
      rendered.queryClient.clear()
    }
  })

  test('displays error details with HTTP status badge and copy button on 500 error, keeping prompt intact', async () => {
    mockWorkbench(() => 20)
    api.post = (() =>
      Promise.reject({
        response: {
          status: 500,
          data: { error: { message: 'Internal engine error during sampling' } },
        },
      })) as typeof api.post

    const rendered = await renderDrawing()
    try {
      await promptDrawing(rendered.container)
      const promptInput = rendered.container.querySelector<HTMLTextAreaElement>(
        '#drawing-prompt-input'
      )
      assert.ok(promptInput)
      await setTextareaValue(promptInput, 'Mountain sunrise')

      await act(async () => {
        button(rendered.container, 'Generate image').click()
        await flushEffects()
      })

      // Error message and HTTP status badge rendered
      assert.match(
        rendered.container.textContent ?? '',
        /Internal engine error during sampling/
      )
      assert.match(rendered.container.textContent ?? '', /HTTP 500/)

      // Prompt is preserved
      assert.equal(promptInput.value, 'Mountain sunrise')

      // Copy error details button is available
      const copyBtn = button(rendered.container, 'Copy error details')
      assert.ok(copyBtn)

      // Retry button is available
      const retryBtn = button(rendered.container, 'Retry')
      assert.ok(retryBtn)
    } finally {
      await act(async () => rendered.root.unmount())
      rendered.queryClient.clear()
    }
  })
})
