import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { createInstance } from 'i18next'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import { AssistantToolCalls } from './assistant-tool-calls.js'
import { collapseAssistantToolTraces } from './assistant-tool-traces.js'

const testI18n = createInstance()
await testI18n.use(initReactI18next).init({
  lng: 'en',
  fallbackLng: 'en',
  resources: { en: { translation: {} } },
})

describe('assistant tool traces', () => {
  test('collapses duplicate failures and hides failures recovered by success', () => {
    const traces = collapseAssistantToolTraces([
      {
        name: 'calculate_math',
        status: 'output-error',
        errorCode: 'missing_math_expression',
      },
      {
        name: 'calculate_math',
        status: 'output-error',
        errorCode: 'missing_math_expression',
      },
      {
        name: 'calculate_math',
        status: 'output-available',
        input: { expression: '6 * 7' },
        result: 42,
      },
    ])

    assert.deepEqual(traces, [
      {
        name: 'calculate_math',
        status: 'output-available',
        input: { expression: '6 * 7' },
        result: 42,
      },
    ])
  })

  test('shows a recovered calculation result without no-parameter noise', () => {
    const markup = renderToStaticMarkup(
      createElement(
        I18nextProvider,
        { i18n: testI18n },
        createElement(AssistantToolCalls, {
          traces: [
            {
              name: 'calculate_math',
              status: 'output-error',
              errorCode: 'missing_math_expression',
            },
            {
              name: 'calculate_math',
              status: 'output-available',
              input: { expression: '6 * 7' },
              result: 42,
            },
            {
              name: 'get_service_facts',
              status: 'output-available',
            },
          ],
        })
      )
    )
    assert.match(markup, /6 \* 7 = 42/)
    assert.doesNotMatch(markup, /No parameters/)
    assert.doesNotMatch(markup, /Tool failed/)
  })

  test('keeps distinct successful calculations', () => {
    const traces = collapseAssistantToolTraces([
      {
        name: 'calculate_math',
        status: 'output-available',
        input: { expression: '6 * 7' },
        result: 42,
      },
      {
        name: 'calculate_math',
        status: 'output-available',
        input: { expression: '9 / 3' },
        result: 3,
      },
    ])
    assert.equal(traces.length, 2)
  })
})
