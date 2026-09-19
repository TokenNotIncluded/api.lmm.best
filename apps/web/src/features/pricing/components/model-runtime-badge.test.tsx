/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import type { ModelRuntimeState } from '../types'
import { ModelRuntimeBadge } from './model-runtime-badge'
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
function render(state?: ModelRuntimeState) {
  return renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <ModelRuntimeBadge state={state} />
    </I18nextProvider>
  )
}
test('runtime badge never assumes availability without a routing snapshot', () => {
  assert.match(render(), /Runtime status unknown/)
  assert.doesNotMatch(render(), /Routable now/)
})
test('runtime labels distinguish operational notices, access and absence', () => {
  const status = (
    state: ModelRuntimeState['status'],
    source: ModelRuntimeState['source']
  ) => render({ status: state, source, observed_at: 100 })
  assert.match(
    status('maintenance', 'administrator_notice'),
    /Under maintenance/
  )
  assert.match(
    status('maintenance', 'administrator_notice'),
    /Administrator status notice/
  )
  assert.match(
    status('available', 'routing_configuration'),
    /Current routing configuration/
  )
  assert.match(status('no_access', 'account_permissions'), /No account access/)
  assert.match(
    status('not_listed', 'routing_configuration'),
    /Not in the model catalog/
  )
})
