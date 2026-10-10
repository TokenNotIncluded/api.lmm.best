/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, describe, test } from 'node:test'

import { Window } from 'happy-dom'

import type { PricingModel } from '../types'

const domWindow = new Window({ url: 'https://console.example.test/pricing' })
domWindow.document.write(
  '<!doctype html><html><head></head><body></body></html>'
)
Object.defineProperty(domWindow.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
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
const { renderToStaticMarkup } = await import('react-dom/server')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { ENDPOINT_TEMPLATES } = await import('@/features/models/constants')
const { api } = await import('@/lib/api')
const {
  ENDPOINT_TYPES,
  getEndpointTypeLabel,
  getEndpointTypeLabels,
  isNativeSessionEndpointType,
} = await import('../constants')
const { ModelDetailsApi } = await import('./model-details-api')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

type EndpointMap = Record<string, { path?: string; method?: string }>

const nativeEndpoints = [
  {
    type: 'live',
    label: 'Live session',
    path: '/v1/live/sessions',
    models: ['gpt-live-1'],
  },
  {
    type: 'realtime_transcription',
    label: 'Realtime transcription',
    path: '/v1/realtime?intent=transcription',
    models: ['gpt-live-transcribe', 'gpt-realtime-whisper'],
  },
  {
    type: 'realtime_translation',
    label: 'Realtime translation',
    path: '/v1/realtime/translations',
    models: ['gpt-realtime-translate'],
  },
] as const

function model(modelName: string, endpointTypes: string[]): PricingModel {
  return {
    id: 1,
    model_name: modelName,
    quota_type: 0,
    model_ratio: 1,
    completion_ratio: 1,
    enable_groups: ['default'],
    supported_endpoint_types: endpointTypes,
  }
}

function apiTab(
  pricingModel: PricingModel,
  endpointMap: EndpointMap,
  queryClient: InstanceType<typeof QueryClient>
) {
  return (
    <QueryClientProvider client={queryClient}>
      <I18nextProvider i18n={i18n}>
        <ModelDetailsApi model={pricingModel} endpointMap={endpointMap} />
      </I18nextProvider>
    </QueryClientProvider>
  )
}

function renderApi(pricingModel: PricingModel, endpointMap: EndpointMap) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  try {
    return renderToStaticMarkup(apiTab(pricingModel, endpointMap, queryClient))
  } finally {
    queryClient.clear()
  }
}

function assertNativeClientHelp(html: string) {
  assert.match(html, /Native session client required/)
  assert.match(
    html,
    /This model requires a native session client\. Synchronous channel tests do not apply\./
  )
  assert.match(html, /Authentication/)
  assert.match(html, /Authorization: Bearer &lt;TOKEN&gt;/)
  assert.doesNotMatch(
    html,
    /Code samples|cURL|Python|TypeScript|JavaScript|role="tablist"|role="textbox"|&lt;YOUR_API_KEY&gt;|with the API key from your token settings\./
  )
}

after(() => domWindow.close())

describe('native session public API details', () => {
  test('exposes distinct native labels, filters and GET endpoint templates', () => {
    const labels = getEndpointTypeLabels(i18n.t)
    for (const endpoint of nativeEndpoints) {
      assert.ok(Object.values(ENDPOINT_TYPES).includes(endpoint.type))
      assert.equal(isNativeSessionEndpointType(endpoint.type), true)
      assert.equal(getEndpointTypeLabel(endpoint.type, i18n.t), endpoint.label)
      assert.equal(labels[endpoint.type], endpoint.label)
      assert.deepEqual(ENDPOINT_TEMPLATES[endpoint.type], {
        method: 'GET',
        path: endpoint.path,
      })
    }
    for (const endpointType of [
      'openai',
      'systemone',
      'moderation',
      'unknown',
    ]) {
      assert.equal(isNativeSessionEndpointType(endpointType), false)
    }
  })

  for (const endpoint of nativeEndpoints) {
    for (const modelName of endpoint.models) {
      test(`${modelName} shows its native WebSocket path`, () => {
        const html = renderApi(
          model(modelName, [endpoint.type]),
          ENDPOINT_TEMPLATES
        )
        assertNativeClientHelp(html)
        assert.ok(html.includes(endpoint.label))
        assert.ok(html.includes(`GET ${endpoint.path} (WebSocket)`))
      })
    }
  }

  test('native metadata wins over chat fallback for arbitrary model aliases', () => {
    for (const endpoint of nativeEndpoints) {
      const html = renderApi(
        model('account-session', ['openai', endpoint.type]),
        ENDPOINT_TEMPLATES
      )
      assertNativeClientHelp(html)
      assert.ok(html.includes(`GET ${endpoint.path} (WebSocket)`))
      assert.doesNotMatch(html, /\/v1\/chat\/completions/)
    }
  })

  test('model names alone do not invent or override endpoint metadata', () => {
    for (const endpoint of nativeEndpoints) {
      for (const modelName of endpoint.models) {
        const withoutMetadata = renderApi(
          model(modelName, []),
          ENDPOINT_TEMPLATES
        )
        assert.match(withoutMetadata, /Authentication/)
        assert.doesNotMatch(
          withoutMetadata,
          /Native session client required|Code samples|cURL|role="textbox"/
        )

        const judgmentAlias = renderApi(
          model(modelName, ['systemone']),
          ENDPOINT_TEMPLATES
        )
        assert.match(judgmentAlias, /Code samples|TypeSafe \(Jev\)/)
        assert.match(judgmentAlias, /POST \/typesafe\/v1\/systemone/)
        assert.doesNotMatch(judgmentAlias, /Native session client required/)
      }
    }
  })

  test('missing native paths retain client guidance without inventing a request', () => {
    for (const endpoint of nativeEndpoints) {
      for (const endpointMap of [
        { openai: ENDPOINT_TEMPLATES.openai },
        {
          openai: ENDPOINT_TEMPLATES.openai,
          [endpoint.type]: { method: 'GET' },
        },
      ]) {
        const html = renderApi(
          model('account-session', ['openai', endpoint.type]),
          endpointMap
        )
        assertNativeClientHelp(html)
        assert.doesNotMatch(html, /\/v1\/|WebSocket/)
      }
    }
  })

  test('native connection information follows configured paths and methods', () => {
    const html = renderApi(model('account-session', ['live']), {
      live: { path: '/tenant/{model}/session', method: 'GET' },
    })
    assertNativeClientHelp(html)
    assert.match(html, /GET \/tenant\/account-session\/session \(WebSocket\)/)
    assert.doesNotMatch(html, /\/v1\/live\/sessions|\{model\}/)
  })

  test('native paths without an explicit method use GET', () => {
    const html = renderApi(
      model('account-session', ['realtime_transcription']),
      {
        realtime_transcription: {
          path: '/custom/realtime?intent=transcription',
        },
      }
    )
    assertNativeClientHelp(html)
    assert.match(
      html,
      /GET \/custom\/realtime\?intent=transcription \(WebSocket\)/
    )
    assert.doesNotMatch(html, /POST/)
  })

  test('ordinary OpenAI models retain language tabs and an actual chat cURL payload', async () => {
    const originalGet = api.get
    api.get = (async (url: string) => {
      assert.equal(url, '/api/status')
      return { data: { success: true, data: {} } }
    }) as typeof api.get
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    try {
      await act(async () => {
        root.render(
          apiTab(
            model('ordinary-chat', ['openai']),
            ENDPOINT_TEMPLATES,
            queryClient
          )
        )
      })
      const code = container.querySelector('[role="textbox"] .cm-content')
      assert.ok(code)
      assert.match(
        code.textContent ?? '',
        /curl https:\/\/console\.example\.test\/v1\/chat\/completions/
      )
      assert.match(
        code.textContent ?? '',
        /Authorization: Bearer \$NEW_API_KEY/
      )
      assert.match(code.textContent ?? '', /"model": "ordinary-chat"/)
      assert.match(code.textContent ?? '', /"messages": \[/)
      assert.match(code.textContent ?? '', /"role": "user"/)
      assert.match(container.textContent ?? '', /Code samples/)
      assert.match(container.textContent ?? '', /<YOUR_API_KEY>/)
      assert.match(container.textContent ?? '', /Authentication/)
      assert.equal(container.querySelectorAll('[role="tab"]').length, 4)
      assert.doesNotMatch(
        container.textContent ?? '',
        /Native session client required|Synchronous channel tests/
      )
    } finally {
      await act(async () => root.unmount())
      queryClient.clear()
      container.remove()
      api.get = originalGet
    }
  })
})
