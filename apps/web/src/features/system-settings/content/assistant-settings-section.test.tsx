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
import { after, describe, test } from 'node:test'

import { Window } from 'happy-dom'

import type { AssistantSettingsFormValues } from './assistant-settings-schema'

const domWindow = new Window({
  url: 'https://console.example.test/admin/system-settings/content/assistant',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
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
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { AssistantSettingsSection } =
  await import('./assistant-settings-section')
const { ConversationStartersEditor } =
  await import('./assistant-settings-section')
const { assistantSettingsSchema } = await import('./assistant-settings-schema')
const {
  ASSISTANT_REASONING_EFFORTS,
  ASSISTANT_SEARCH_PROVIDERS,
  normalizeAssistantSearchProvider,
} = await import('../types')

const assistantSearchURLByProvider: Partial<
  Record<(typeof ASSISTANT_SEARCH_PROVIDERS)[number], string>
> = {
  generic_http: 'https://search.example/api/search',
  mcp_streamable_http: 'https://search.example/mcp',
}

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

const baseValues = {
  AssistantEnabled: true,
  AssistantGroup: 'default',
  AssistantModel: 'deepseek-v4-flash',
  AssistantReasoningEffort: 'auto',
  AssistantStreamEnabled: true,
  AssistantTemperature: 0.2,
  AssistantMaxTokens: 900,
  AssistantAgentLoopEnabled: true,
  AssistantMaxSteps: 6,
  AssistantTimeoutSeconds: 45,
  AssistantCacheEnabled: true,
  AssistantCacheTTLMinutes: 1440,
  AssistantPersona: '',
  AssistantSystemPrompt: '',
  AssistantPreConversationPresets: '',
  AssistantSearchProvider: 'none',
  AssistantSearchURL: '',
  AssistantSearchAPIKey: '',
  AssistantSearchMCPTool: '',
  AssistantSkills: '',
  AssistantSkillFiles: '[]',
  AssistantRegistrationAutoSuspendEnabled: true,
  AssistantRegistrationDailySuspendCap: 5,
  AssistantL1AutoReviewEnabled: false,
  AssistantL1AutoReviewGroup: '',
  AssistantL1AutoReviewModel: '',
  AssistantL1AutoReviewPrompt: '',
  AssistantL1AutoReviewMinConfidence: 0.98,
  AssistantL1AutoApprovalUserIDs: '',
  AssistantReviewEnabled: true,
  AssistantReviewWindowDays: 30,
  AssistantReviewIntervalHours: 24,
  AssistantReviewProbability: 0,
  AssistantReviewGroup: 'default',
  AssistantReviewModel: 'deepseek-v4-flash',
  AssistantReviewReasoningEffort: 'auto',
  AssistantReviewGroupPolicies: '{}',
  AssistantRetentionEnabled: true,
  AssistantActiveRetentionDays: 90,
  AssistantArchivedRetentionDays: 30,
  AssistantSecurityRetentionDays: 180,
  AssistantRetentionIntervalHours: 24,
} as const

async function renderSettings(
  provider: (typeof ASSISTANT_SEARCH_PROVIDERS)[number],
  overrides: Partial<AssistantSettingsFormValues> = {}
) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const queryClient = new QueryClient({
    defaultOptions: { mutations: { retry: false } },
  })

  const render = (next: Partial<AssistantSettingsFormValues> = {}) => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <AssistantSettingsSection
            defaultValues={{
              ...baseValues,
              AssistantSearchProvider: provider,
              AssistantSearchURL: assistantSearchURLByProvider[provider] ?? '',
              AssistantSearchMCPTool:
                provider === 'mcp_streamable_http' ? 'web_search' : '',
              ...overrides,
              ...next,
            }}
          />
        </I18nextProvider>
      </QueryClientProvider>
    )
  }
  await act(async () => render())

  return {
    container,
    queryClient,
    rerender: (next: Partial<AssistantSettingsFormValues>) =>
      act(async () => {
        render(next)
        await flushEffects()
      }),
    cleanup: async () => {
      await act(async () => root.unmount())
      container.remove()
      queryClient.clear()
    },
  }
}

async function flushEffects() {
  await new Promise((resolve) => setTimeout(resolve, 20))
}

after(() => domWindow.close())

describe('assistant search provider settings', () => {
  test('edits, reorders, deletes, and restores conversation starters through one JSON field', async () => {
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    const values: string[] = []
    function Harness() {
      const [value, setValue] = useState(
        '[{"id":"one","label":"One","prompt":"Prompt one"}]'
      )
      return (
        <ConversationStartersEditor
          value={value}
          disabled={false}
          onChange={(next) => {
            values.push(next)
            setValue(next)
          }}
        />
      )
    }
    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <Harness />
        </I18nextProvider>
      )
    })
    const editor = container.querySelector(
      '[data-testid="assistant-conversation-starters-editor"]'
    )
    assert.ok(editor)
    const inputs = editor.querySelectorAll('input')
    const prompt = editor.querySelector('textarea') as HTMLTextAreaElement
    const setValue = (
      element: HTMLInputElement | HTMLTextAreaElement,
      value: string
    ) => {
      const setter = Object.getOwnPropertyDescriptor(
        Object.getPrototypeOf(element),
        'value'
      )?.set
      setter?.call(element, value)
    }
    await act(async () => {
      setValue(inputs[0], 'Edited label')
      inputs[0].dispatchEvent(new Event('change', { bubbles: true }))
      setValue(prompt, 'Edited prompt')
      prompt.dispatchEvent(new Event('change', { bubbles: true }))
    })
    // The default copy is what every language falls back to.
    const edited = JSON.parse(values.at(-1) ?? '[]')[0]
    assert.equal(edited.label.default, 'Edited label')
    assert.equal(edited.prompt.default, 'Edited prompt')
    // A per-language override is stored under its locale key.
    const frenchLabel = editor.querySelector(
      'input[aria-label="Français label"]'
    ) as HTMLInputElement | null
    assert.ok(frenchLabel)
    await act(async () => {
      setValue(frenchLabel, 'Libellé personnalisé')
      frenchLabel.dispatchEvent(new Event('change', { bubbles: true }))
    })
    assert.equal(
      JSON.parse(values.at(-1) ?? '[]')[0].label.fr,
      'Libellé personnalisé'
    )
    const add = [...container.querySelectorAll('button')].find((button) =>
      button.textContent?.includes('Add starter')
    )
    assert.ok(add)
    await act(async () => {
      add.click()
      await flushEffects()
    })
    assert.equal(JSON.parse(values.at(-1) ?? '[]').length, 2)
    // The second starter starts as the new custom entry in the emitted value.
    const added = JSON.parse(values.at(-1) ?? '[]')
    assert.equal(added[0].id, 'one')
    assert.equal(added[1].id.startsWith('custom_'), true)
    assert.equal(added[1].label.default, '')
    const deleteButtons = [
      ...container.querySelectorAll('button[aria-label="Delete starter"]'),
    ] as HTMLButtonElement[]
    assert.equal(deleteButtons.length, 2)
    await act(async () => {
      deleteButtons[1].click()
      await flushEffects()
    })
    const afterDelete = JSON.parse(values.at(-1) ?? '[]')
    assert.equal(afterDelete.length, 1)
    assert.equal(afterDelete[0].id, 'one')
    const restore = [...container.querySelectorAll('button')].find((button) =>
      button.textContent?.includes('Restore defaults')
    )
    assert.ok(restore)
    await act(async () => restore.click())
    assert.equal(values.at(-1), '')
    await act(async () => root.unmount())
    container.remove()
  })
  test('uses bounded built-in registration rules without a separate reviewer', () => {
    assert.equal(assistantSettingsSchema.safeParse(baseValues).success, true)
    for (const cap of [-1, 6, 1.5, Number.NaN, Infinity]) {
      assert.equal(
        assistantSettingsSchema.safeParse({
          ...baseValues,
          AssistantRegistrationDailySuspendCap: cap,
        }).success,
        false
      )
    }
    assert.equal(
      assistantSettingsSchema.safeParse({
        ...baseValues,
        AssistantRegistrationDailySuspendCap: 0,
      }).success,
      true
    )
    assert.equal(
      assistantSettingsSchema.safeParse({
        ...baseValues,
        AssistantL1AutoReviewEnabled: true,
      }).success,
      true
    )
  })

  for (const outcome of ['loaded', 'empty', 'error'] as const) {
    test(`loads the registration inbox with ${outcome} evidence`, async () => {
      const originalGet = api.get
      const requests: string[] = []
      api.get = (async (url: string) => {
        requests.push(url)
        if (url === '/api/group/') return { data: { data: ['default'] } }
        if (url === '/api/assistant/models') {
          return { data: { data: ['deepseek-v4-flash'] } }
        }
        assert.equal(url, '/api/assistant/admin/registration-events')
        if (outcome === 'error') throw new Error('offline')
        return {
          data: {
            success: true,
            data:
              outcome === 'empty'
                ? []
                : [
                    {
                      id: 1,
                      user_id: 7,
                      action: 'notify',
                      created_at: 1,
                      policy_version: 'registration-v1',
                      evidence: '{"decision":{"alert":true}}',
                    },
                  ],
          },
        }
      }) as typeof api.get
      const rendered = await renderSettings('none')
      try {
        await act(flushEffects)
        const panel = rendered.container.querySelector(
          '[data-testid="assistant-registration-guard-settings"]'
        )
        assert.ok(panel)
        assert.match(panel.textContent ?? '', /Registration protection/)
        assert.equal(
          panel.querySelector('[role="switch"]')?.getAttribute('aria-checked'),
          'true'
        )
        assert.equal(
          panel.querySelector('input[type="number"]')?.getAttribute('max'),
          '5'
        )
        assert.equal(
          rendered.container.querySelector(
            '[data-testid="assistant-l1-review-settings"]'
          ),
          null
        )
        assert.equal(
          rendered.container.querySelector(
            '[data-testid="assistant-review-route-fields"]'
          ),
          null
        )
        // The connection tab loads its selector once, not a separate risk-review model.
        assert.equal(
          requests.filter((url) => url === '/api/assistant/models').length,
          1
        )
        if (outcome === 'error') {
          assert.match(panel.textContent ?? '', /Unable to load risk inbox/)
        }
        if (outcome === 'empty') {
          assert.match(
            panel.textContent ?? '',
            /No recorded registration alerts/
          )
        }
        if (outcome === 'loaded') {
          assert.match(panel.textContent ?? '', /registration-v1/)
        }
      } finally {
        api.get = originalGet
        await rendered.cleanup()
      }
    })
  }

  test('saves the bounded suspension switch through the bulk settings endpoint', async () => {
    const originalGet = api.get
    const originalPost = api.post
    let capturedValues: Record<string, string> | undefined
    api.get = (async (url: string) => ({
      data: { success: true, data: url === '/api/group/' ? ['default'] : [] },
    })) as typeof api.get
    api.post = (async (
      url: string,
      body: { values?: Record<string, string> }
    ) => {
      assert.equal(url, '/api/option/bulk')
      capturedValues = body.values
      return { data: { success: true } }
    }) as typeof api.post
    const rendered = await renderSettings('none')
    try {
      const toggle = rendered.container.querySelector<HTMLButtonElement>(
        '[data-testid="assistant-registration-guard-settings"] [role="switch"]'
      )
      const form = rendered.container.querySelector('form')
      assert.ok(toggle)
      assert.ok(form)
      await act(async () => {
        toggle.click()
        await flushEffects()
      })
      await act(async () => {
        form.dispatchEvent(
          new Event('submit', { bubbles: true, cancelable: true })
        )
        await flushEffects()
        await flushEffects()
      })
      assert.deepEqual(capturedValues, {
        AssistantRegistrationAutoSuspendEnabled: 'false',
      })
    } finally {
      api.get = originalGet
      api.post = originalPost
      await rendered.cleanup()
    }
  })

  test('validates bounded conversation retention settings', () => {
    assert.equal(assistantSettingsSchema.safeParse(baseValues).success, true)
    for (const invalid of [
      { AssistantReviewWindowDays: 0 },
      { AssistantReviewIntervalHours: 169 },
      { AssistantTemperature: -0.1 },
      { AssistantTemperature: 2.1 },
      { AssistantMaxTokens: 63 },
      { AssistantMaxTokens: 8193 },
      { AssistantActiveRetentionDays: 6 },
      { AssistantArchivedRetentionDays: 0 },
      { AssistantSecurityRetentionDays: 29 },
      { AssistantRetentionIntervalHours: 169 },
    ]) {
      assert.equal(
        assistantSettingsSchema.safeParse({ ...baseValues, ...invalid })
          .success,
        false
      )
    }
  })

  test('renders response delivery controls with bounded AI settings', async () => {
    const { container, cleanup } = await renderSettings('none')
    try {
      assert.ok(container.querySelector('input[name="AssistantTemperature"]'))
      assert.ok(container.querySelector('input[name="AssistantMaxTokens"]'))
      assert.match(container.textContent ?? '', /Stream responses/)
      assert.match(container.textContent ?? '', /Stream tokens incrementally/)
    } finally {
      await cleanup()
    }
  })

  test('accepts every supported reasoning effort for primary and review routes', () => {
    assert.deepEqual(ASSISTANT_REASONING_EFFORTS, [
      'auto',
      'none',
      'minimal',
      'low',
      'medium',
      'high',
      'xhigh',
      'max',
    ])
    for (const effort of ASSISTANT_REASONING_EFFORTS) {
      assert.equal(
        assistantSettingsSchema.safeParse({
          ...baseValues,
          AssistantReasoningEffort: effort,
          AssistantReviewReasoningEffort: effort,
        }).success,
        true,
        effort
      )
    }
    assert.equal(
      assistantSettingsSchema.safeParse({
        ...baseValues,
        AssistantReviewReasoningEffort: 'ultra',
      }).success,
      false
    )
  })

  test('accepts supported providers and rejects unknown values', () => {
    for (const provider of ASSISTANT_SEARCH_PROVIDERS) {
      const result = assistantSettingsSchema.safeParse({
        ...baseValues,
        AssistantSearchProvider: provider,
      })
      assert.equal(result.success, true, provider)
    }

    const invalid = assistantSettingsSchema.safeParse({
      ...baseValues,
      AssistantSearchProvider: 'unknown-provider',
    })
    assert.equal(invalid.success, false)
  })

  test('maps legacy search URL settings to custom HTTP and empty settings to none', () => {
    assert.equal(
      normalizeAssistantSearchProvider(
        undefined,
        'https://search.example/api/search'
      ),
      'generic_http'
    )
    assert.equal(normalizeAssistantSearchProvider(undefined, ''), 'none')
    assert.equal(
      normalizeAssistantSearchProvider('mcp_streamable_http', ''),
      'mcp_streamable_http'
    )
  })

  test('shows only the custom URL for generic HTTP search', async () => {
    const { container, cleanup } = await renderSettings('generic_http')
    assert.ok(
      container.querySelector('input[name="AssistantSearchURL"]'),
      'custom HTTP URL should be visible'
    )
    assert.equal(
      container.querySelector('input[name="AssistantSearchMCPTool"]'),
      null
    )
    assert.match(container.textContent ?? '', /q query parameter/)
    await cleanup()
  })

  test('shows the MCP endpoint and optional tool name for MCP search', async () => {
    const { container, cleanup } = await renderSettings('mcp_streamable_http')
    assert.ok(container.querySelector('input[name="AssistantSearchURL"]'))
    assert.ok(container.querySelector('input[name="AssistantSearchMCPTool"]'))
    assert.match(container.textContent ?? '', /Streamable HTTP/)
    await cleanup()
  })

  test('shows the official provider description without a custom URL', async () => {
    const { container, cleanup } = await renderSettings('exa')
    assert.equal(
      container.querySelector('input[name="AssistantSearchURL"]'),
      null
    )
    assert.match(container.textContent ?? '', /official Exa Search API/)
    await cleanup()
  })

  test('retains aggregate reporting but not a separate sampled review model', async () => {
    const rendered = await renderSettings('none')
    try {
      assert.equal(
        rendered.container.querySelector(
          '[data-testid="assistant-review-route-fields"]'
        ),
        null
      )
      assert.equal(
        rendered.container.querySelector(
          'input[name="AssistantReviewProbability"]'
        ),
        null
      )
      assert.ok(
        rendered.container.querySelector(
          'input[name="AssistantReviewWindowDays"]'
        )
      )
    } finally {
      await rendered.cleanup()
    }
  })

  test('saves changed assistant options through one bulk mutation', async () => {
    const originalGet = api.get
    const originalPost = api.post
    let capturedURL = ''
    let capturedValues: Record<string, string> | undefined
    api.get = (async (url: string) => {
      if (url === '/api/group/') {
        return { data: { data: ['default'] } }
      }
      throw new Error(`unexpected GET ${url}`)
    }) as typeof api.get
    api.post = (async (
      url: string,
      body: { values?: Record<string, string> }
    ) => {
      capturedURL = url
      capturedValues = body.values
      return { data: { success: true, message: '' } }
    }) as typeof api.post

    const rendered = await renderSettings('none')
    try {
      const maxStepsInput = rendered.container.querySelector<HTMLInputElement>(
        'input[name="AssistantMaxSteps"]'
      )
      const form = rendered.container.querySelector('form')
      assert.ok(maxStepsInput)
      assert.ok(form)
      await act(async () => {
        const valueSetter = Object.getOwnPropertyDescriptor(
          HTMLInputElement.prototype,
          'value'
        )?.set
        assert.ok(valueSetter)
        valueSetter.call(maxStepsInput, '7')
        maxStepsInput.dispatchEvent(new Event('input', { bubbles: true }))
        maxStepsInput.dispatchEvent(new Event('change', { bubbles: true }))
        form.dispatchEvent(
          new Event('submit', { bubbles: true, cancelable: true })
        )
        await flushEffects()
        await flushEffects()
      })

      assert.equal(capturedURL, '/api/option/bulk')
      assert.equal(capturedValues?.AssistantMaxSteps, '7')
      assert.equal(Object.keys(capturedValues ?? {}).length, 1)
    } finally {
      api.get = originalGet
      api.post = originalPost
      await rendered.cleanup()
    }
  })

  test('loads model IDs for the selected group automatically and permits an explicit refresh', async () => {
    const waitForState = async (ready: () => boolean) => {
      const deadline = Date.now() + 5000
      while (!ready() && Date.now() < deadline) {
        await act(flushEffects)
      }
      assert.ok(ready(), 'model request and rendered control must settle')
    }
    const originalGet = api.get
    const modelRequests: string[] = []
    api.get = (async (url: string) => {
      if (url === '/api/group/') {
        return {
          data: { data: ['default', '国产[Kimi/Deepseek/GLM]'] },
        }
      }
      if (url === '/api/assistant/models') {
        modelRequests.push(url)
        return {
          data: { data: ['deepseek-v4-flash-0731'] },
        }
      }
      throw new Error(`unexpected GET ${url}`)
    }) as typeof api.get

    const rendered = await renderSettings('none')
    try {
      await waitForState(() => modelRequests.length === 1)
      assert.equal(modelRequests.length, 1)

      const groupTrigger =
        rendered.container.querySelectorAll<HTMLButtonElement>(
          'button[role="combobox"]'
        )[0]
      assert.ok(groupTrigger)
      await waitForState(() => !groupTrigger.disabled)
      await act(async () => {
        groupTrigger.click()
        await flushEffects()
      })
      await waitForState(() =>
        [...document.querySelectorAll('[role="option"]')].some((option) =>
          option.textContent?.includes('国产')
        )
      )
      const domesticOption = [
        ...document.querySelectorAll('[role="option"]'),
      ].find((option) => option.textContent?.includes('国产'))
      assert.ok(domesticOption)
      await act(async () => {
        ;(domesticOption as HTMLElement).click()
        await flushEffects()
      })

      const getModelListButton =
        rendered.container.querySelector<HTMLButtonElement>(
          '[data-testid="assistant-get-model-list"]'
        )
      assert.ok(getModelListButton)
      await waitForState(
        () => modelRequests.length === 2 && !getModelListButton.disabled
      )
      assert.equal(getModelListButton.disabled, false)

      await act(async () => {
        getModelListButton.click()
        await flushEffects()
      })

      await waitForState(
        () => modelRequests.length === 3 && !getModelListButton.disabled
      )
      assert.deepEqual(modelRequests, Array(3).fill('/api/assistant/models'))
      const modelTrigger =
        rendered.container.querySelectorAll<HTMLButtonElement>(
          'button[role="combobox"]'
        )[1]
      assert.ok(modelTrigger)
      assert.equal(modelTrigger.disabled, false)
      assert.match(modelTrigger.textContent ?? '', /Select a model ID/)

      await act(async () => {
        modelTrigger.click()
        await flushEffects()
      })
      await waitForState(() =>
        [...document.querySelectorAll('[role="option"]')].some((option) =>
          option.textContent?.includes('deepseek-v4-flash-0731')
        )
      )
      const modelOption = [
        ...document.querySelectorAll('[role="option"]'),
      ].find((option) => option.textContent?.includes('deepseek-v4-flash-0731'))
      assert.ok(modelOption)
      await act(async () => {
        ;(modelOption as HTMLElement).click()
        await flushEffects()
      })
      assert.match(modelTrigger.textContent ?? '', /deepseek-v4-flash-0731/)
      assert.doesNotMatch(rendered.container.textContent ?? '', /Invalid input/)
    } finally {
      api.get = originalGet
      await rendered.cleanup()
    }
  })
})

describe('assistant settings workspace', () => {
  function mockReads() {
    const original = api.get
    api.get = (async (url: string) => ({
      data: {
        success: true,
        data:
          url === '/api/group/'
            ? ['default']
            : url === '/api/assistant/models'
              ? [baseValues.AssistantModel]
              : [],
      },
    })) as typeof api.get
    return () => {
      api.get = original
    }
  }
  const edit = async (
    node: HTMLInputElement | HTMLTextAreaElement,
    value: string
  ) => {
    await act(async () => {
      const prototype =
        node.tagName === 'TEXTAREA'
          ? window.HTMLTextAreaElement.prototype
          : window.HTMLInputElement.prototype
      const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set
      assert.ok(setter)
      setter.call(node, value)
      node.dispatchEvent(new Event('input', { bubbles: true }))
      node.dispatchEvent(new Event('change', { bubbles: true }))
      await flushEffects()
    })
  }

  test('only one group is exposed and keyboard navigation preserves an unsaved draft', async () => {
    const restore = mockReads()
    const page = await renderSettings('none')
    try {
      const tabs = [
        ...page.container.querySelectorAll<HTMLButtonElement>(
          '[data-settings-tab]'
        ),
      ]
      assert.equal(tabs.length, 6)
      assert.equal(
        page.container.querySelectorAll('[role="tabpanel"]:not([hidden])')
          .length,
        1
      )
      await act(async () => tabs[1].click())
      const prompt = page.container.querySelector<HTMLTextAreaElement>(
        'textarea[name="AssistantSystemPrompt"]'
      )!
      assert.ok(prompt)
      await edit(prompt, 'Keep my draft')
      await act(async () =>
        tabs[1].dispatchEvent(
          Object.assign(new Event('keydown', { bubbles: true }), {
            key: 'ArrowRight',
          })
        )
      )
      assert.equal(tabs[2].getAttribute('aria-selected'), 'true')
      assert.equal(
        page.container.querySelectorAll('[role="tabpanel"]:not([hidden])')
          .length,
        1
      )
      await act(async () => tabs[1].click())
      assert.equal(prompt.value, 'Keep my draft')
      assert.ok(page.container.textContent?.includes('Unsaved changes'))
      await page.rerender({
        AssistantSystemPrompt: 'Remote edit',
        AssistantTimeoutSeconds: 60,
      })
      assert.equal(prompt.value, 'Keep my draft')
      assert.equal(
        page.container.querySelector<HTMLInputElement>(
          'input[name="AssistantTimeoutSeconds"]'
        )?.value,
        '60'
      )
      const reset = page.container.querySelector<HTMLButtonElement>(
        '.assistant-settings-footer button'
      )!
      await act(async () => reset.click())
      assert.equal(prompt.value, 'Remote edit')
    } finally {
      await page.cleanup()
      restore()
    }
  })

  test('a save failure keeps the draft, while successful preset edits invalidate cached starters', async () => {
    const restore = mockReads()
    const originalPost = api.post
    let failed = true
    api.post = (async () => {
      if (failed) throw new Error('offline')
      return { data: { success: true } }
    }) as typeof api.post
    const page = await renderSettings('none')
    const presetsKey = [
      'assistant-pre-conversation-presets',
      'natural-v2',
      'en',
    ]
    const statusKey = ['assistant-status', 7, 'session']
    page.queryClient.setQueryData(presetsKey, { presets: [] })
    page.queryClient.setQueryData(statusKey, { enabled: true })
    try {
      await act(async () =>
        page.container
          .querySelector<HTMLButtonElement>(
            '[data-settings-tab="conversation"]'
          )!
          .click()
      )
      const preset = page.container.querySelector<HTMLTextAreaElement>(
        '[data-testid="assistant-conversation-starters-editor"] textarea'
      )!
      assert.ok(preset)
      await edit(preset, 'Custom starter from settings')
      const form = page.container.querySelector('form')
      assert.ok(form)
      const save = () =>
        act(async () => {
          form.dispatchEvent(
            new Event('submit', { bubbles: true, cancelable: true })
          )
          await flushEffects()
          await flushEffects()
        })
      await save()
      assert.equal(preset.value, 'Custom starter from settings')
      assert.ok(page.container.textContent?.includes('Unsaved changes'))
      assert.equal(
        page.queryClient.getQueryState(presetsKey)?.isInvalidated,
        false
      )
      failed = false
      await save()
      assert.equal(
        page.queryClient.getQueryState(presetsKey)?.isInvalidated,
        true
      )
      assert.equal(
        page.queryClient.getQueryState(statusKey)?.isInvalidated,
        true
      )
      assert.equal(
        page.container.querySelector<HTMLButtonElement>(
          '.assistant-settings-footer button[type="submit"]'
        )?.disabled,
        true
      )
    } finally {
      await page.cleanup()
      api.post = originalPost
      restore()
    }
  })
})
