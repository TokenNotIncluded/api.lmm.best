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

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { AssistantSettingsSection } =
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
  AssistantSearchProvider: 'none',
  AssistantSearchURL: '',
  AssistantSearchAPIKey: '',
  AssistantSearchMCPTool: '',
  AssistantSkills: '',
  AssistantSkillFiles: '[]',
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
  provider: (typeof ASSISTANT_SEARCH_PROVIDERS)[number]
) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const queryClient = new QueryClient({
    defaultOptions: { mutations: { retry: false } },
  })

  await act(async () => {
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
            }}
          />
        </I18nextProvider>
      </QueryClientProvider>
    )
  })

  return {
    container,
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

  test('uses enum controls for the review group, model ID, and reasoning effort', async () => {
    const originalGet = api.get
    const requestedGroups: string[] = []
    api.get = (async (
      url: string,
      config?: { params?: { group?: string } }
    ) => {
      if (url === '/api/group/') {
        return { data: { data: ['default', 'review-premium'] } }
      }
      if (url === '/api/assistant/models') {
        requestedGroups.push(config?.params?.group ?? '')
        return { data: { data: ['review-model-live'] } }
      }
      throw new Error(`unexpected GET ${url}`)
    }) as typeof api.get

    const rendered = await renderSettings('none')
    try {
      await act(flushEffects)
      const routeFields = rendered.container.querySelector<HTMLElement>(
        '[data-testid="assistant-review-route-fields"]'
      )
      assert.ok(routeFields)
      assert.equal(
        routeFields.querySelector('input[name="AssistantReviewModel"]'),
        null
      )

      const getModelListButton = routeFields.querySelector<HTMLButtonElement>(
        '[data-testid="assistant-review-get-model-list"]'
      )
      assert.ok(getModelListButton)
      const routeComboboxes = routeFields.querySelectorAll<HTMLButtonElement>(
        'button[role="combobox"]'
      )
      assert.equal(routeComboboxes.length, 3)
      assert.equal(routeComboboxes[1]?.disabled, true)

      await act(async () => {
        getModelListButton.click()
        await flushEffects()
      })

      assert.deepEqual(requestedGroups, ['default'])
      assert.equal(routeComboboxes[1]?.disabled, false)
      await act(async () => {
        routeComboboxes[2]?.click()
        await flushEffects()
      })
      const effortOptions = new Set(
        [...document.querySelectorAll('[role="option"]')].map((option) =>
          option.textContent?.trim()
        )
      )
      for (const effort of ASSISTANT_REASONING_EFFORTS) {
        assert.ok(effortOptions.has(effort), effort)
      }
    } finally {
      api.get = originalGet
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

  test('loads model IDs only after the administrator requests the list', async () => {
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
      await act(flushEffects)
      assert.equal(modelRequests.length, 0)

      const groupTrigger =
        rendered.container.querySelectorAll<HTMLButtonElement>(
          'button[role="combobox"]'
        )[0]
      assert.ok(groupTrigger)
      await act(async () => {
        groupTrigger.click()
        await flushEffects()
      })
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
      assert.equal(getModelListButton.disabled, false)

      await act(async () => {
        getModelListButton.click()
        await flushEffects()
      })

      assert.deepEqual(modelRequests, ['/api/assistant/models'])
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
