/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createInstance } from 'i18next'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import {
  getI18n,
  initReactI18next,
  I18nextProvider,
  useTranslation,
} from 'react-i18next'

test('English landing scope does not replace the user language outside the homepage', async () => {
  const application = createInstance()
  await application.use(initReactI18next).init({
    lng: 'zhCN',
    resources: {
      zhCN: { translation: { Home: '首页' } },
      ja: { translation: { Home: 'ホーム' } },
    },
    react: { useSuspense: false },
  })
  const { default: landing } = await import('./landing-i18n')
  assert.equal(
    getI18n(),
    application,
    'landing initialization must not set the global React i18n instance'
  )
  function Label() {
    return createElement('span', null, useTranslation().t('Home'))
  }
  const render = (instance: typeof application) =>
    renderToStaticMarkup(
      createElement(I18nextProvider, { i18n: instance }, createElement(Label))
    )
  assert.equal(render(landing), '<span>Home</span>')
  assert.equal(render(application), '<span>首页</span>')
  await application.changeLanguage('ja')
  assert.equal(render(landing), '<span>Home</span>')
  assert.equal(render(application), '<span>ホーム</span>')
  assert.equal(application.language, 'ja')
})
