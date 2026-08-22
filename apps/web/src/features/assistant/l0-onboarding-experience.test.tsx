/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
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
  'HTMLFormElement',
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
  'FormData',
  'File',
  'FileReader',
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
const { AssistantLauncher } = await import('./assistant-launcher')
const { AssistantPanel } = await import('./assistant-panel')
const { getAssistantPromptValidation } =
  await import('./assistant-prompt-validation')

const originalGet = api.get
const originalMatchMedia = window.matchMedia
const originalInnerWidth = window.innerWidth
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

const restrictedStatus = {
  enabled: true,
  model: 'deepseek-v4-flash',
  developer_access_granted: false,
  funding: { mode: 'super_administrator' as const },
}

const generatedPresets = {
  generation: 1_786_500_000,
  version: 'generated-v1',
  presets: [
    {
      id: 'generated_support',
      label: 'Talk to support',
      prompt: 'Please connect me with human support.',
    },
  ],
}

async function flushEffects() {
  await new Promise((resolve) => setTimeout(resolve, 25))
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

async function renderPanel() {
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
          <AssistantPanel mode='rail' open onOpenChange={() => {}} />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flushEffects()
  })
  await act(flushEffects)
  return { queryClient, root }
}

async function renderLauncher() {
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
          <AssistantLauncher />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flushEffects()
  })
  await act(flushEffects)
  return { queryClient, root }
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

afterEach(() => {
  api.get = originalGet
  window.matchMedia = originalMatchMedia
  Object.defineProperty(window, 'innerWidth', {
    configurable: true,
    value: originalInnerWidth,
  })
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('L0 onboarding assistant experience', () => {
  test('allows short assistant messages but rejects a single punctuation mark', () => {
    assert.deepEqual(getAssistantPromptValidation('  甲  ', true), {
      characterCount: 1,
      invalid: false,
    })
    assert.deepEqual(getAssistantPromptValidation('  。  ', true), {
      characterCount: 1,
      invalid: true,
    })
    assert.deepEqual(getAssistantPromptValidation('?', false), {
      characterCount: 1,
      invalid: true,
    })
    assert.deepEqual(getAssistantPromptValidation('。好', true), {
      characterCount: 2,
      invalid: false,
    })
  })

  test('puts L0 onboarding and privacy guidance on the first assistant screen', async () => {
    api.get = (async (url: string) => {
      if (url === '/api/assistant/pre-conversation-presets') {
        return { data: { success: true, data: generatedPresets } }
      }
      assert.equal(url, '/api/assistant/status')
      return { data: { success: true, data: restrictedStatus } }
    }) as typeof api.get

    const rendered = await renderPanel()
    try {
      await act(async () =>
        waitForCondition(
          () =>
            document.querySelector('[data-testid="assistant-l0-welcome"]') !==
            null,
          'L0 welcome card did not render'
        )
      )

      assert.match(
        document.body.textContent ?? '',
        /Guidance for plans, setup, API keys, costs, and support\./
      )
      assert.equal(
        document.querySelector('[data-testid="assistant-onboarding-todo"]'),
        null
      )
      assert.match(
        document.body.textContent ?? '',
        /What would you like to do\?/
      )
      assert.doesNotMatch(
        document.body.textContent ?? '',
        /shielded private card|private card/
      )
      assert.ok(
        document.querySelector(
          'button:not([aria-label="Send"]):not([data-testid="assistant-collapse"])'
        )
      )

      const textarea = document.querySelector<HTMLTextAreaElement>('textarea')
      assert.ok(textarea)
      assert.equal(textarea.getAttribute('aria-label'), 'Ask AI assistant')
      assert.equal(textarea.required, true)
      assert.ok(textarea.minLength <= 0)
      assert.doesNotMatch(
        textarea.getAttribute('aria-describedby') ?? '',
        /assistant-l0-input-hint/
      )
      assert.match(document.body.textContent ?? '', /Talk to support/)
    } finally {
      await act(async () => rendered.root.unmount())
      rendered.queryClient.clear()
    }
  })

  test('allows a short L0 message and rejects a single punctuation mark', async () => {
    api.get = (async (url: string) => {
      assert.equal(url, '/api/assistant/status')
      return { data: { success: true, data: restrictedStatus } }
    }) as typeof api.get

    const rendered = await renderPanel()
    try {
      await act(async () =>
        waitForCondition(
          () => document.querySelector('textarea') !== null,
          'L0 composer did not render'
        )
      )
      const textarea = document.querySelector<HTMLTextAreaElement>('textarea')
      assert.ok(textarea)
      await setTextareaValue(textarea, '甲')

      const submit = document.querySelector<HTMLButtonElement>(
        'button[aria-label="Send"]'
      )
      assert.ok(submit)
      assert.equal(submit.disabled, false)
      assert.equal(textarea.getAttribute('aria-invalid'), 'false')
      assert.equal(document.querySelector('#assistant-l0-input-hint'), null)

      await setTextareaValue(textarea, '。')
      assert.equal(submit.disabled, true)
      assert.equal(textarea.getAttribute('aria-invalid'), 'true')
      assert.equal(
        document.querySelector('#assistant-l0-input-hint')?.textContent,
        'Please enter a message other than a single punctuation mark.'
      )
    } finally {
      await act(async () => rendered.root.unmount())
      rendered.queryClient.clear()
    }
  })

  test('keeps the overlay launcher out of layout flow with a safe-area inset', async () => {
    Object.defineProperty(window, 'innerWidth', {
      configurable: true,
      value: 390,
    })
    window.matchMedia = (() => ({
      matches: true,
      media: '(max-width: 767px)',
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    })) as typeof window.matchMedia
    api.get = (async (url: string) => {
      assert.equal(url, '/api/status')
      return {
        data: { success: true, data: { assistant: { enabled: true } } },
      }
    }) as typeof api.get

    const rendered = await renderLauncher()
    try {
      await act(async () =>
        waitForCondition(
          () =>
            document.querySelector(
              '[data-testid="assistant-mobile-launcher"]'
            ) !== null,
          'Mobile assistant launcher did not render'
        )
      )
      const launcher = document.querySelector<HTMLElement>(
        '[data-testid="assistant-mobile-launcher"]'
      )
      assert.ok(launcher)
      assert.match(launcher.className, /fixed/)
      assert.match(launcher.className, /pointer-events-none/)
      assert.match(launcher.className, /xl:hidden/)
      assert.match(
        launcher.className,
        /pb-\[max\(0\.375rem,env\(safe-area-inset-bottom\)\)\]/
      )
      assert.equal(
        launcher
          .querySelector<HTMLButtonElement>(
            '[data-testid="assistant-launcher"]'
          )
          ?.getAttribute('aria-haspopup'),
        'dialog'
      )
    } finally {
      await act(async () => rendered.root.unmount())
      rendered.queryClient.clear()
    }
  })
})
