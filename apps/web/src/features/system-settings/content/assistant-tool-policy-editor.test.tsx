/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

import { DEFAULT_ASSISTANT_TOOL_POLICY } from './assistant-tool-policy'

const domWindow = new Window({
  url: 'https://console.example.test/system-settings/content/assistant',
})
for (const key of [
  'window',
  'document',
  'navigator',
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
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { AssistantToolPolicyEditor } =
  await import('./assistant-tool-policy-editor')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })

const catalog = {
  success: true,
  data: {
    groups: [
      {
        id: 'api_keys',
        label: 'API keys',
        tools: [
          {
            name: 'list_my_api_keys',
            label: 'List API keys',
            description: 'View key settings without credentials.',
            effect: 'read_only',
            access: 'user',
          },
          {
            name: 'request_create_key',
            label: 'Create an API key',
            description: 'Prepare creation after confirmation.',
            effect: 'confirmation',
            access: 'l1',
          },
        ],
      },
      {
        id: 'admin_changes',
        label: 'Administrator changes',
        tools: [
          {
            name: 'prepare_admin_config_change',
            label: 'Change server settings',
            description: 'Prepare an administrator confirmation.',
            effect: 'confirmation',
            access: 'root',
          },
        ],
      },
      {
        id: 'rewards',
        label: 'Rewards',
        tools: [
          {
            name: 'prepare_new_user_gift',
            label: 'Evaluate a welcome gift',
            description: 'Record a one-time eligibility decision.',
            effect: 'server_guarded',
            access: 'user',
          },
        ],
      },
    ],
  },
}

async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 20))
}

async function renderEditor(
  options: {
    value?: string
    active?: boolean
    disabled?: boolean
    get?: () => unknown | Promise<unknown>
  } = {}
) {
  const originalGet = api.get
  let requests = 0
  const changes: string[] = []
  api.get = (async (url: string) => {
    assert.equal(url, '/api/assistant/admin/tool-catalog')
    requests++
    return { data: options.get ? await options.get() : catalog }
  }) as typeof api.get
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  function Harness({
    active,
    disabled,
  }: {
    active: boolean
    disabled: boolean
  }) {
    const [value, setValue] = useState(
      options.value ?? DEFAULT_ASSISTANT_TOOL_POLICY
    )
    return (
      <AssistantToolPolicyEditor
        value={value}
        active={active}
        disabled={disabled}
        onChange={(next) => {
          changes.push(next)
          setValue(next)
        }}
      />
    )
  }
  const render = (active: boolean, disabled: boolean) =>
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <Harness active={active} disabled={disabled} />
        </I18nextProvider>
      </QueryClientProvider>
    )
  await act(async () => {
    render(options.active ?? true, options.disabled ?? false)
    await flush()
  })
  await act(flush)
  return {
    container,
    changes,
    get requests() {
      return requests
    },
    rerender: async (active: boolean, disabled = false) => {
      await act(async () => {
        render(active, disabled)
        await flush()
      })
      await act(flush)
    },
    cleanup: async () => {
      await act(async () => root.unmount())
      container.remove()
      queryClient.clear()
      api.get = originalGet
    },
  }
}

async function click(element: Element | null) {
  assert.ok(element instanceof HTMLElement)
  await act(async () => {
    element.click()
    await flush()
  })
}
function setInput(element: HTMLInputElement, value: string) {
  Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    'value'
  )?.set?.call(element, value)
  element.dispatchEvent(new Event('input', { bubbles: true }))
  element.dispatchEvent(new Event('change', { bubbles: true }))
}
after(() => domWindow.close())

test('fetches the authoritative catalog only when the tools panel becomes active', async () => {
  const rendered = await renderEditor({ active: false })
  try {
    assert.equal(rendered.requests, 0)
    await rendered.rerender(true)
    assert.equal(rendered.requests, 1)
    assert.match(rendered.container.textContent ?? '', /Enabled 4 of 4 tools/)
    assert.match(rendered.container.textContent ?? '', /Requires confirmation/)
    assert.match(
      rendered.container.textContent ?? '',
      /Writes after server checks/
    )
    assert.match(
      rendered.container.textContent ?? '',
      /L6 \(Super administrator\)/
    )
    await rendered.rerender(false)
    assert.equal(rendered.requests, 1)
  } finally {
    await rendered.cleanup()
  }
})

test('disabled groups prevent per-tool bypass and restore the individual override when enabled', async () => {
  const rendered = await renderEditor({
    value:
      '{"version":1,"groups":{"api_keys":false},"tools":{"list_my_api_keys":true,"request_create_key":false}}',
  })
  try {
    const list = rendered.container.querySelector(
      '[data-tool-name="list_my_api_keys"] [role="switch"]'
    ) as HTMLElement
    const create = rendered.container.querySelector(
      '[data-tool-name="request_create_key"] [role="switch"]'
    ) as HTMLElement
    assert.equal(list.getAttribute('aria-checked'), 'false')
    assert.equal(list.hasAttribute('data-disabled'), true)
    assert.equal(create.hasAttribute('data-disabled'), true)
    await click(list)
    assert.deepEqual(rendered.changes, [])
    assert.match(rendered.container.textContent ?? '', /Saved tool choice: On/)
    await click(
      rendered.container.querySelector(
        '[data-tool-group="api_keys"] [role="switch"]'
      )
    )
    assert.equal(list.hasAttribute('data-disabled'), false)
    assert.equal(list.getAttribute('aria-checked'), 'true')
    assert.equal(create.getAttribute('aria-checked'), 'false')
    const savedRaw = rendered.changes.at(-1)
    assert.ok(savedRaw)
    const saved = JSON.parse(savedRaw)
    assert.deepEqual(saved.tools, {
      list_my_api_keys: true,
      request_create_key: false,
    })
    await click(list)
    const toolChoiceRaw = rendered.changes.at(-1)
    assert.ok(toolChoiceRaw)
    assert.equal(JSON.parse(toolChoiceRaw).tools.list_my_api_keys, false)
    await click(
      rendered.container.querySelector(
        '[data-tool-group="api_keys"] [role="switch"]'
      )
    )
    await click(
      rendered.container.querySelector(
        '[data-tool-group="api_keys"] [role="switch"]'
      )
    )
    assert.equal(list.getAttribute('aria-checked'), 'false')
    assert.equal(create.getAttribute('aria-checked'), 'false')
  } finally {
    await rendered.cleanup()
  }
})

test('searches readable labels, identifiers and permissions without changing the policy', async () => {
  const rendered = await renderEditor()
  try {
    const search = rendered.container.querySelector(
      'input[aria-label="Search assistant tools"]'
    ) as HTMLInputElement
    await act(async () => {
      setInput(search, 'request_create_key')
      await flush()
    })
    assert.equal(
      rendered.container.querySelectorAll('[data-tool-name]').length,
      1
    )
    assert.ok(
      rendered.container.querySelector('[data-tool-name="request_create_key"]')
    )
    await act(async () => {
      setInput(search, 'L6 (Super administrator)')
      await flush()
    })
    assert.ok(
      rendered.container.querySelector(
        '[data-tool-name="prepare_admin_config_change"]'
      )
    )
    assert.equal(
      rendered.container.querySelectorAll('[data-tool-name]').length,
      1
    )
    await act(async () => {
      setInput(search, 'no match for this text')
      await flush()
    })
    assert.match(
      rendered.container.textContent ?? '',
      /No tools match your search/
    )
    assert.deepEqual(rendered.changes, [])
    assert.equal(rendered.requests, 1)
  } finally {
    await rendered.cleanup()
  }
})

test('malformed and unknown policy entries never silently enable tools; reset is an explicit draft edit', async () => {
  for (const value of [
    '{bad',
    '{"version":1,"tools":{"unknown":true}}',
    '{"version":1,"groups":{"unknown":false}}',
  ]) {
    const rendered = await renderEditor({ value })
    try {
      assert.match(
        rendered.container.querySelector('[role="alert"]')?.textContent ?? '',
        /saved tool policy is invalid/
      )
      const switches = [
        ...rendered.container.querySelectorAll<HTMLElement>('[role="switch"]'),
      ]
      assert.ok(switches.length > 0)
      assert.ok(
        switches.every(
          (element) =>
            element.hasAttribute('data-disabled') &&
            element.getAttribute('aria-checked') === 'false'
        )
      )
      assert.deepEqual(rendered.changes, [])
      await click(
        [...rendered.container.querySelectorAll('button')].find(
          (button) => button.textContent === 'Reset tool policy to defaults'
        ) ?? null
      )
      assert.deepEqual(rendered.changes, [DEFAULT_ASSISTANT_TOOL_POLICY])
      assert.equal(rendered.container.querySelector('[role="alert"]'), null)
    } finally {
      await rendered.cleanup()
    }
  }
})

test('malformed catalog responses show a retryable error without tool switches or draft changes', async () => {
  const rendered = await renderEditor({
    get: () => ({
      success: true,
      data: {
        groups: [
          {
            id: 'api_keys',
            label: 'Keys',
            tools: [
              {
                name: 'create_key',
                label: 'Create',
                description: 'Create',
                effect: 'unsafe',
                access: 'user',
              },
            ],
          },
        ],
      },
    }),
  })
  try {
    assert.match(
      rendered.container.querySelector('[role="alert"]')?.textContent ?? '',
      /Unable to load assistant tools/
    )
    assert.equal(rendered.container.querySelector('[role="switch"]'), null)
    assert.deepEqual(rendered.changes, [])
  } finally {
    await rendered.cleanup()
  }
})

test('a failed read can be retried without saving or replacing the current tool policy', async () => {
  let attempts = 0
  const rendered = await renderEditor({
    value: '{"version":1,"tools":{"request_create_key":false}}',
    get: () => {
      if (++attempts === 1) throw new Error('Temporary service failure')
      return catalog
    },
  })
  try {
    assert.equal(attempts, 1)
    await click(
      [...rendered.container.querySelectorAll('button')].find(
        (button) => button.textContent === 'Retry'
      ) ?? null
    )
    assert.equal(attempts, 2)
    assert.equal(rendered.container.querySelector('[role="alert"]'), null)
    assert.equal(
      rendered.container
        .querySelector('[data-tool-name="request_create_key"] [role="switch"]')
        ?.getAttribute('aria-checked'),
      'false'
    )
    assert.match(rendered.container.textContent ?? '', /Enabled 3 of 4 tools/)
    assert.deepEqual(rendered.changes, [])
  } finally {
    await rendered.cleanup()
  }
})

test('configures a disabled tool group without enabling it or saving early', async () => {
  const rendered = await renderEditor({
    value: JSON.stringify({
      version: 1,
      groups: { api_keys: false },
      tools: {},
    }),
  })
  try {
    await click(
      rendered.container.querySelector(
        '[aria-label="Configure Create an API key"]'
      )
    )
    const dialog = document.querySelector(
      '[data-testid="assistant-tool-configuration"]'
    )
    assert.ok(dialog)
    assert.match(dialog.textContent ?? '', /Changes stay in this draft/)
    const controls = dialog.querySelectorAll('[role="combobox"]')
    assert.equal(controls.length, 2)
    await click(controls[0])
    const options = Array.from(document.querySelectorAll('[role="option"]'))
    assert.equal(
      options.some((item) => item.textContent === 'L0'),
      false
    )
    await click(options.find((item) => item.textContent === 'L2') ?? null)
    const changed = JSON.parse(rendered.changes.at(-1) ?? '{}')
    assert.deepEqual(changed.rules.request_create_key, {
      min_level: 2,
      max_level: 6,
    })
    assert.equal(changed.groups.api_keys, false)
    await click(
      Array.from(dialog.querySelectorAll('button')).find(
        (button) => button.textContent === 'Reset tool rules'
      ) ?? null
    )
    const reset = JSON.parse(rendered.changes.at(-1) ?? '{}')
    assert.equal(reset.rules, undefined)
    assert.equal(reset.groups.api_keys, false)
    await click(
      Array.from(dialog.querySelectorAll('button')).find(
        (button) => button.textContent === 'Done'
      ) ?? null
    )
    assert.equal(
      document.querySelector('[data-testid="assistant-tool-configuration"]'),
      null
    )
    assert.equal(rendered.requests, 1)
  } finally {
    await rendered.cleanup()
  }
})

test('weekly discount dialog edits member ceilings but never grants administrator rewards', async () => {
  const withDiscount = structuredClone(catalog)
  withDiscount.data.groups[2].tools.push({
    name: 'prepare_weekly_discount',
    label: 'Weekly discount',
    description: 'Evaluate a discount.',
    effect: 'server_guarded',
    access: 'user',
  })
  const rendered = await renderEditor({ get: () => withDiscount })
  try {
    await click(
      rendered.container.querySelector(
        '[aria-label="Configure Weekly discount"]'
      )
    )
    const dialog = document.querySelector(
      '[data-testid="assistant-tool-configuration"]'
    )
    assert.ok(dialog)
    const inputs = dialog.querySelectorAll<HTMLInputElement>(
      'input[type="number"]'
    )
    assert.equal(inputs.length, 7)
    assert.equal(inputs[1].value, '10')
    assert.equal(inputs[5].disabled, true)
    assert.equal(inputs[5].value, '0')
    assert.equal(inputs[6].disabled, true)
    await act(async () => {
      setInput(inputs[1], '25')
      await flush()
    })
    const rule = JSON.parse(rendered.changes.at(-1) ?? '{}').rules
      .prepare_weekly_discount
    assert.equal(rule.discount_percent_by_level['1'], 25)
    const count = rendered.changes.length
    await act(async () => {
      setInput(inputs[1], '100')
      await flush()
    })
    assert.equal(rendered.changes.length, count)
    await act(async () => {
      setInput(inputs[1], '0')
      await flush()
    })
    assert.equal(
      JSON.parse(rendered.changes.at(-1) ?? '{}').rules.prepare_weekly_discount
        .discount_percent_by_level['1'],
      0
    )
  } finally {
    await rendered.cleanup()
  }
})
