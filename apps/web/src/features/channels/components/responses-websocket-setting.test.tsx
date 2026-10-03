/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { useForm } from 'react-hook-form'
import { I18nextProvider } from 'react-i18next'

import { Form } from '@/components/ui/form'
import type { SystemStatus } from '@/features/auth/types'
import { getCapabilitySafeStatus } from '@/lib/backend-capabilities'

import {
  CHANNEL_FORM_DEFAULT_VALUES,
  transformFormDataToUpdatePayload,
  type ChannelFormValues,
} from '../lib/channel-form'
import { ResponsesWebSocketSetting } from './responses-websocket-setting'

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  fallbackLng: 'en',
  resources: { en: { translation: {} } },
  keySeparator: false,
})

const supportedBackend = { backend_capabilities: { responses_websocket: true } }

function renderSetting(
  type: number,
  status: SystemStatus | null | undefined,
  enabled?: boolean
) {
  let savedSetting = ''
  function SettingForm() {
    const form = useForm<ChannelFormValues>({
      defaultValues: {
        ...CHANNEL_FORM_DEFAULT_VALUES,
        type,
        responses_websocket_enabled: enabled,
      },
    })
    savedSetting =
      transformFormDataToUpdatePayload(form.getValues(), 42).setting || ''
    return (
      <Form {...form}>
        <ResponsesWebSocketSetting
          control={form.control}
          channelType={type}
          status={status}
        />
      </Form>
    )
  }
  const html = renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <SettingForm />
    </I18nextProvider>
  )
  return { html, savedSetting }
}

describe('Responses WebSocket channel control', () => {
  test('shows only supported types and uses their unset defaults', () => {
    for (const type of [1, 57, 58, 59, 60, 61]) {
      const { html } = renderSetting(type, supportedBackend)
      assert.match(html, /role="switch"/)
      assert.match(
        html,
        new RegExp(`aria-checked="${[1, 57, 61].includes(type)}"`)
      )
      assert.doesNotMatch(html, /data-disabled=""/)
    }
    assert.doesNotMatch(
      renderSetting(3, supportedBackend, true).html,
      /Responses WebSocket|role="switch"/
    )
  })

  test('shows the converter-free route requirement for Advanced Custom', () => {
    assert.match(
      renderSetting(58, supportedBackend).html,
      /Requires a \/v1\/responses route with no converter\./
    )
    assert.doesNotMatch(
      renderSetting(60, supportedBackend).html,
      /no converter/
    )
  })

  test('keeps explicit false off on a capable backend', () => {
    const { html, savedSetting } = renderSetting(1, supportedBackend, false)
    assert.match(html, /aria-checked="false"/)
    assert.doesNotMatch(html, /data-disabled=""/)
    assert.equal(JSON.parse(savedSetting).responses_websocket_enabled, false)
  })

  test('disables the control without a live capability while preserving the saved value', () => {
    for (const status of [
      null,
      { version: 'rust' },
      { backend_capabilities: { responses_websocket: false } },
      getCapabilitySafeStatus(supportedBackend, false),
    ]) {
      const { html, savedSetting } = renderSetting(58, status, true)
      assert.match(html, /aria-checked="false"/)
      assert.match(html, /data-disabled=""/)
      assert.match(
        html,
        /Responses WebSocket is unavailable on the current backend\./
      )
      assert.equal(JSON.parse(savedSetting).responses_websocket_enabled, true)
    }
    const live = renderSetting(
      58,
      getCapabilitySafeStatus(supportedBackend, true),
      true
    ).html
    assert.match(live, /aria-checked="true"/)
    assert.doesNotMatch(live, /data-disabled=""/)
  })
})
