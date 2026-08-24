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

const domWindow = new Window()
const domGlobals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLTextAreaElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const

for (const key of domGlobals) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const i18next = (await import('i18next')).default
const { initReactI18next } = await import('react-i18next')
await i18next.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        JSON: 'JSON',
        'Invalid JSON': 'Invalid JSON',
        'Copied to clipboard': 'Copied to clipboard',
        'Failed to copy': 'Failed to copy',
        'Format JSON': 'Format JSON',
        Example: 'Example',
        Field: 'Field',
        Type: 'Type',
        Required: 'Required',
        Optional: 'Optional',
        Rules: 'Rules',
        'Configuration example': 'Configuration example',
        'Field specification': 'Field specification',
        'Fill Template': 'Fill Template',
        Copy: 'Copy',
        Cancel: 'Cancel',
        Replace: 'Replace',
        'Discard unsaved JSON changes?': 'Discard unsaved JSON changes?',
        'Continuing will replace the unsaved JSON currently in the editor.':
          'Continuing will replace the unsaved JSON currently in the editor.',
      },
    },
  },
})
const { JsonCodeEditor } = await import('../../json-code-editor')
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

type RenderedEditor = {
  container: HTMLDivElement
  root: ReturnType<typeof createRoot>
}

async function renderEditor(
  props: React.ComponentProps<typeof JsonCodeEditor>
): Promise<RenderedEditor> {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  await act(async () => {
    root.render(<JsonCodeEditor {...props} />)
  })

  return { container, root }
}

async function unmountEditor(rendered: RenderedEditor) {
  await act(async () => rendered.root.unmount())
  rendered.container.remove()
}

describe('JsonCodeEditor component', () => {
  after(() => {
    domWindow.close()
  })

  test('forwards form attributes and lifecycle callbacks to the textarea', async () => {
    const blurCalls: number[] = []
    const refValues: Array<HTMLTextAreaElement | null> = []
    const rendered = await renderEditor({
      value: '{"model":"gpt"}',
      onChange: () => undefined,
      id: 'json-input',
      name: 'model_config',
      placeholder: '{"model":"gpt"}',
      disabled: true,
      'aria-describedby': 'model-help',
      'aria-invalid': true,
      'data-form-root': 'settings-form',
      onBlur: () => blurCalls.push(1),
      textareaRef: (element) => refValues.push(element),
    })
    const textarea = rendered.container.querySelector('textarea')

    assert.ok(textarea)
    assert.equal(textarea.id, 'json-input')
    assert.equal(textarea.name, 'model_config')
    assert.equal(textarea.placeholder, '{"model":"gpt"}')
    assert.equal(textarea.disabled, true)
    const validationStatus = rendered.container.querySelector('[role="status"]')
    assert.ok(validationStatus)
    assert.equal(validationStatus.getAttribute('aria-live'), 'polite')
    assert.deepEqual(textarea.getAttribute('aria-describedby')?.split(' '), [
      'model-help',
      validationStatus.id,
    ])
    assert.equal(textarea.getAttribute('aria-invalid'), 'true')
    assert.equal(textarea.getAttribute('data-form-root'), 'settings-form')

    await act(async () => textarea.dispatchEvent(new Event('blur')))
    assert.deepEqual(blurCalls, [1])
    assert.equal(refValues[0], textarea)

    await unmountEditor(rendered)
    assert.equal(refValues.at(-1), null)
  })

  test('emits user edits and synchronizes a controlled value', async () => {
    const changes: string[] = []
    const rendered = await renderEditor({
      value: '{"count":1}',
      onChange: (value) => changes.push(value),
    })
    const textarea = rendered.container.querySelector('textarea')

    assert.ok(textarea)
    await act(async () => {
      textarea.value = '{"count":2}'
      textarea.dispatchEvent(new Event('input', { bubbles: true }))
    })
    assert.deepEqual(changes, ['{"count":2}'])

    await act(async () => {
      rendered.root.render(
        <JsonCodeEditor
          value='{"count":3}'
          onChange={(value) => changes.push(value)}
        />
      )
    })
    assert.equal(textarea.value, '{"count":3}')

    await unmountEditor(rendered)
  })

  test('formats valid JSON through the public toolbar action', async () => {
    const changes: string[] = []
    const rendered = await renderEditor({
      value: '{"model":{"ratio":2}}',
      onChange: (value) => changes.push(value),
    })
    const formatButton = [
      ...rendered.container.querySelectorAll('button'),
    ].find((button) => button.textContent?.includes('Format JSON'))

    assert.ok(formatButton)
    await act(async () => formatButton.click())
    assert.deepEqual(changes, ['{\n  "model": {\n    "ratio": 2\n  }\n}'])

    await unmountEditor(rendered)
  })

  test('shows a safe example and fills it without changing the editor contract', async () => {
    const changes: string[] = []
    const example = '{\n  "default": 1\n}'
    const rendered = await renderEditor({
      value: '',
      onChange: (value) => changes.push(value),
      example,
    })

    const details = rendered.container.querySelector('details')
    assert.ok(details)
    assert.equal(details.textContent?.includes(example), true)
    const fillButton = [...details.querySelectorAll('button')].find((button) =>
      button.textContent?.includes('Fill Template')
    )
    assert.ok(fillButton)

    await act(async () => fillButton.click())
    assert.deepEqual(changes, [example])

    await unmountEditor(rendered)
  })

  test('copies an example and confirms before replacing populated JSON', async () => {
    const changes: string[] = []
    const clipboardWrites: string[] = []
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: {
        writeText: async (value: string) => {
          clipboardWrites.push(value)
        },
      },
    })
    const example = '{\n  "default": 1\n}'
    const rendered = await renderEditor({
      value: '{"existing":true}',
      onChange: (value) => changes.push(value),
      example,
    })
    const details = rendered.container.querySelector('details')
    assert.ok(details)
    const buttons = [...details.querySelectorAll('button')]
    const copyButton = buttons.find((button) => button.textContent === 'Copy')
    const fillButton = buttons.find((button) =>
      button.textContent?.includes('Fill Template')
    )
    assert.ok(copyButton)
    assert.ok(fillButton)

    await act(async () => {
      copyButton.click()
      await Promise.resolve()
    })
    assert.deepEqual(clipboardWrites, [example])
    assert.deepEqual(changes, [])

    await act(async () => fillButton.click())
    assert.deepEqual(changes, [])
    const replacementAlert = details.querySelector('[role="alert"]')
    assert.ok(replacementAlert)
    assert.equal(
      replacementAlert.textContent?.includes('Discard unsaved JSON changes?'),
      true
    )

    const cancelButton = [...replacementAlert.querySelectorAll('button')].find(
      (button) => button.textContent === 'Cancel'
    )
    assert.ok(cancelButton)
    await act(async () => cancelButton.click())
    assert.equal(details.querySelector('[role="alert"]'), null)
    assert.deepEqual(changes, [])

    await act(async () => fillButton.click())
    const confirmButton = [
      ...details.querySelectorAll<HTMLButtonElement>('[role="alert"] button'),
    ].find((button) => button.textContent === 'Replace')
    assert.ok(confirmButton)
    await act(async () => confirmButton.click())
    assert.deepEqual(changes, [example])

    await unmountEditor(rendered)
  })

  test('renders an accessible field specification with types and constraints', async () => {
    const rendered = await renderEditor({
      value: '{"enabled":true}',
      onChange: () => undefined,
      example: '{\n  "enabled": true\n}',
      specificationDefaultOpen: true,
      specification: {
        rootType: 'ExampleConfig',
        fields: [
          {
            path: 'enabled',
            type: 'boolean',
            required: true,
            rules: 'default: false',
            example: 'true',
          },
          {
            path: 'label',
            type: 'string',
            required: false,
          },
        ],
      },
    })

    const details = [...rendered.container.querySelectorAll('details')].find(
      (element) => element.textContent?.includes('Field specification')
    )
    assert.ok(details)
    assert.equal(details.open, true)
    assert.equal(details.textContent?.includes('ExampleConfig'), true)
    assert.equal(details.textContent?.includes('default: false'), true)
    assert.equal(details.textContent?.includes('Required'), true)
    assert.equal(details.textContent?.includes('Optional'), true)

    const scrollRegion = details.querySelector('[role="region"]')
    assert.ok(scrollRegion)
    assert.equal(scrollRegion.getAttribute('tabindex'), '0')

    const table = details.querySelector('table')
    assert.ok(table)
    assert.equal(
      table.querySelector('caption')?.textContent,
      'Field specification'
    )
    assert.equal(table.querySelector('th[scope="row"]')?.textContent, 'enabled')

    const mobileList = details.querySelector(
      'ul[aria-label="Field specification"]'
    )
    assert.ok(mobileList)
    assert.equal(mobileList.querySelector('li code')?.textContent, 'enabled')
    assert.equal(mobileList.textContent?.includes('default: false'), true)

    await unmountEditor(rendered)
  })
})
