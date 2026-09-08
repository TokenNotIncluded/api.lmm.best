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
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { AssistantSetupTool } = await import('./assistant-setup-tool')
const { ClientKeyImport } = await import('./client-key-import')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

async function flushEffects() {
  await new Promise((resolve) => setTimeout(resolve, 20))
}

function findButton(text: string): HTMLButtonElement {
  const button = [
    ...document.querySelectorAll<HTMLButtonElement>('button'),
  ].find((candidate) => candidate.textContent?.includes(text))
  assert.ok(button, `Could not find button containing ${text}`)
  return button
}

afterEach(() => document.body.replaceChildren())
after(() => domWindow.close())

describe('AssistantSetupTool', () => {
  test('covers each client and gives Linux truthful ChatGPT guidance', async () => {
    let createKeyCalls = 0
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)

    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <AssistantSetupTool
            rootUrl='https://api.example.test'
            openAIBaseUrl='https://api.example.test/v1'
            availableModels={['claude-sonnet-4-5', 'gpt-5.6-codex']}
            developerAccessGranted
            onCreateKey={() => {
              createKeyCalls += 1
            }}
            onRequestAccess={() => {}}
          />
        </I18nextProvider>
      )
      await flushEffects()
    })

    const platformButtons = ['Windows', 'macOS', 'Linux'].map(findButton)
    assert.equal(
      platformButtons.filter(
        (button) => button.getAttribute('aria-pressed') === 'true'
      ).length,
      1
    )

    await act(async () => {
      findButton('Windows').click()
      findButton('Claude Code').click()
      await flushEffects()
    })
    assert.match(
      container.textContent ?? '',
      /winget install Anthropic\.ClaudeCode/
    )
    assert.equal(findButton('Windows').getAttribute('aria-pressed'), 'true')
    assert.equal(findButton('Linux').getAttribute('aria-pressed'), 'false')
    const createKeyButton = findButton('Create API key')
    assert.equal(createKeyButton.disabled, false)

    await act(async () => {
      findButton('CC Switch').click()
      await flushEffects()
    })
    assert.match(
      container.textContent ?? '',
      /CC-Switch-v\{version\}-Windows\.msi/
    )
    assert.match(container.textContent ?? '', /CC Switch one-click import/)

    await act(async () => {
      findButton('Claude Desktop').click()
      await flushEffects()
    })
    assert.match(container.textContent ?? '', /Install Claude Desktop/)
    assert.match(container.textContent ?? '', /Anthropic Messages/)

    await act(async () => {
      findButton('Linux').click()
      await flushEffects()
    })
    assert.equal(findButton('Windows').getAttribute('aria-pressed'), 'false')
    assert.equal(findButton('Linux').getAttribute('aria-pressed'), 'true')
    assert.match(
      container.textContent ?? '',
      /CC Switch Desktop provider setup is not available on Linux/
    )

    await act(async () => {
      findButton('ChatGPT').click()
      await flushEffects()
    })
    assert.match(
      container.textContent ?? '',
      /The official ChatGPT desktop app is not available for Linux/
    )
    assert.match(container.textContent ?? '', /Use ChatGPT in your browser/)
    assert.match(
      container.textContent ?? '',
      /https:\/\/api\.example\.test\/v1/
    )
    assert.match(container.textContent ?? '', /claude-sonnet-4-5/)
    assert.equal(
      container.querySelector<HTMLAnchorElement>(
        'a[href="https://chatgpt.com/"]'
      )?.textContent,
      'Open ChatGPT in browser'
    )

    await act(async () => {
      findButton('OpenAI-compatible clients').click()
      await flushEffects()
    })
    assert.match(
      container.textContent ?? '',
      /Codex, Cursor, Open WebUI, and more/
    )
    assert.match(
      container.textContent ?? '',
      /https:\/\/api\.example\.test\/v1/
    )
    assert.match(container.textContent ?? '', /"api_key": "<YOUR_API_KEY>"/)
    assert.equal(
      container.querySelector<HTMLAnchorElement>(
        'a[href="https://developers.openai.com/codex/"]'
      )?.textContent,
      'Codex'
    )
    assert.equal(createKeyCalls, 0)

    await act(async () => {
      const modelSelect = container.querySelector<HTMLSelectElement>(
        'select[aria-label="Model ID"]'
      )
      assert.ok(modelSelect)
      modelSelect.value = 'gpt-5.6-codex'
      modelSelect.dispatchEvent(new Event('change', { bubbles: true }))
      findButton('Codex').click()
      await flushEffects()
    })
    assert.match(container.textContent ?? '', /model_provider = "lmm"/)
    assert.match(container.textContent ?? '', /wire_api = "responses"/)
    assert.match(container.textContent ?? '', /gpt-5\.6-codex/)

    await act(async () => root.unmount())
  })

  test('gives L0 an install-only guide without exposing connection values', async () => {
    let accessRequests = 0
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)

    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <AssistantSetupTool
            rootUrl='https://api.example.test'
            openAIBaseUrl='https://api.example.test/v1'
            availableModels={['deepseek-v4-flash']}
            developerAccessGranted={false}
            onCreateKey={() => {}}
            onRequestAccess={() => {
              accessRequests += 1
            }}
          />
        </I18nextProvider>
      )
      await flushEffects()
    })

    assert.match(container.textContent ?? '', /Ask for L1 access/)
    await act(async () => {
      findButton('Windows').click()
      findButton('Claude Code').click()
      await flushEffects()
    })
    assert.match(
      container.textContent ?? '',
      /winget install Anthropic\.ClaudeCode/
    )
    assert.equal(findButton('Windows').getAttribute('aria-pressed'), 'true')
    assert.doesNotMatch(container.textContent ?? '', /deepseek-v4-flash/)
    assert.doesNotMatch(container.textContent ?? '', /api\.example\.test/)
    assert.equal(container.querySelector('select[aria-label="Model ID"]'), null)
    assert.throws(() => findButton('Create API key'))

    await act(async () => {
      findButton('CC Switch').click()
      await flushEffects()
    })
    assert.match(
      container.textContent ?? '',
      /CC-Switch-v\{version\}-Windows\.msi/
    )
    assert.match(container.textContent ?? '', /CC Switch one-click import/)

    await act(async () => {
      findButton('Unlock L1 access').click()
      await flushEffects()
    })
    assert.equal(accessRequests, 1)

    await act(async () => root.unmount())
  })
})

test('mobile walkthrough exposes official Chatbox steps and keeps desktop commands out', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  let question = ''
  await act(async () => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <AssistantSetupTool
          rootUrl='https://console.example.test'
          openAIBaseUrl='https://console.example.test/v1'
          availableModels={['available-model']}
          developerAccessGranted
          onCreateKey={() => {}}
          onRequestAccess={() => {}}
          onAskQuestion={(value) => {
            question = value
          }}
        />
      </I18nextProvider>
    )
    await flushEffects()
  })
  assert.match(container.textContent ?? '', /Cherry Studio/)
  await act(async () => {
    findButton('Android').click()
    await flushEffects()
  })
  assert.equal(findButton('Android').getAttribute('aria-pressed'), 'true')
  assert.match(container.textContent ?? '', /Google Play or official APK/)
  assert.match(
    container.textContent ?? '',
    /API Path as \/v1\/chat\/completions/
  )
  assert.doesNotMatch(
    container.textContent ?? '',
    /winget|brew install|curl -fsSL/
  )
  assert.equal(
    container.querySelector('[role="tab"][data-value="cc-switch"]'),
    null
  )
  assert.ok(
    container.querySelector(
      'a[href="https://chatboxai.app/en/guide/getting-started/download"]'
    )
  )
  await act(async () => {
    findButton('iOS / iPadOS').click()
    await flushEffects()
  })
  assert.match(container.textContent ?? '', /App Store link/)
  await act(async () => {
    findButton('Walk me through this').click()
  })
  assert.match(question, /Chatbox on iOS \/ iPadOS/)
  assert.match(question, /never ask for my API key/)
  await act(async () => root.unmount())
})

test('public installation guide hides account connection values and private import fields', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <AssistantSetupTool
          rootUrl='https://private.example.test'
          openAIBaseUrl='https://private.example.test/v1'
          availableModels={['private-model']}
          developerAccessGranted={false}
          publicGuide
          onCreateKey={() => assert.fail('must not create a key')}
          onRequestAccess={() => {}}
        />
      </I18nextProvider>
    )
    await flushEffects()
  })
  assert.match(container.textContent ?? '', /Start with the installation/)
  await act(async () => {
    findButton('CC Switch').click()
    await flushEffects()
  })
  assert.doesNotMatch(
    container.textContent ?? '',
    /private.example.test|private-model/
  )
  assert.equal(container.querySelector('input[type="password"]'), null)
  assert.throws(() => findButton('Use an existing API key'))
  await act(async () => root.unmount())
})

test('private key import is explicit, temporary and cleared on cancel or invalid destination', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <ClientKeyImport
          rootUrl='https://untrusted.example.test'
          openAIBaseUrl='https://untrusted.example.test/v1'
          model='available-model'
          availableModels={['available-model']}
        />
      </I18nextProvider>
    )
  })
  assert.equal(container.querySelector('input'), null)
  await act(async () => {
    findButton('Use an existing API key').click()
  })
  const input = container.querySelector<HTMLInputElement>(
    'input[type="password"]'
  )
  assert.ok(input)
  assert.equal(findButton('Open CC Switch').disabled, true)
  const setInput = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setInput)
  await act(async () => {
    setInput.call(input, 'sk-private-import-only')
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  assert.equal(findButton('Open CC Switch').disabled, false)
  assert.doesNotMatch(container.textContent ?? '', /sk-private-import-only/)
  assert.doesNotMatch(container.innerHTML, /sk-private-import-only/)
  assert.equal(window.localStorage.length, 0)
  assert.equal(window.sessionStorage.length, 0)
  await act(async () => {
    findButton('Open CC Switch').click()
  })
  assert.equal(input.value, '')
  assert.match(container.textContent ?? '', /could not be opened/)
  await act(async () => {
    setInput.call(input, 'sk-another-private-key')
    input.dispatchEvent(new Event('input', { bubbles: true }))
    findButton('Cancel').click()
  })
  assert.equal(container.querySelector('input'), null)
  await act(async () => {
    findButton('Use an existing API key').click()
  })
  assert.equal(container.querySelector<HTMLInputElement>('input')?.value, '')
  await act(async () => root.unmount())
})

test('a confirmed import launches only the installed client and clears its input', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const calls: string[] = []
  const originalAssign = window.location.assign
  Object.defineProperty(window.location, 'assign', {
    configurable: true,
    value: (url: string) => calls.push(url),
  })
  try {
    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <ClientKeyImport
            rootUrl='https://console.example.test'
            openAIBaseUrl='https://console.example.test/v1'
            model='available-model'
            availableModels={['available-model']}
          />
        </I18nextProvider>
      )
    })
    await act(async () => {
      findButton('Use an existing API key').click()
    })
    const input = container.querySelector<HTMLInputElement>(
      'input[type="password"]'
    )
    assert.ok(input)
    const setter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(setter)
    await act(async () => {
      setter.call(input, 'sk-local-confirmation')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    assert.deepEqual(calls, [])
    assert.equal(window.localStorage.length, 0)
    await act(async () => {
      findButton('Open CC Switch').click()
    })
    assert.equal(calls.length, 1)
    const url = new URL(calls[0])
    assert.equal(url.protocol, 'ccswitch:')
    assert.equal(url.searchParams.get('apiKey'), 'sk-local-confirmation')
    assert.equal(
      url.searchParams.get('endpoint'),
      'https://console.example.test'
    )
    assert.equal(input.value, '')
    assert.doesNotMatch(container.innerHTML, /sk-local-confirmation/)
    assert.match(
      container.textContent ?? '',
      /cannot detect whether the app imported it/
    )
  } finally {
    Object.defineProperty(window.location, 'assign', {
      configurable: true,
      value: originalAssign,
    })
    await act(async () => root.unmount())
  }
})
