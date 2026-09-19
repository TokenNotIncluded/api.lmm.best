/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router'
import { renderToStaticMarkup } from 'react-dom/server'
import { useTranslation } from 'react-i18next'

import appI18n from './config'
import { RouteLanguageProvider } from './route-language-provider'

function Label() {
  return <span>{useTranslation().t('Home')}</span>
}

test('homepage footer and route share English without changing the next page language', async () => {
  await appI18n.changeLanguage('zhCN')
  const root = createRootRoute({
    component: () => (
      <>
        <footer>
          <Label />
        </footer>
        <Outlet />
      </>
    ),
  })
  const home = createRoute({
    getParentRoute: () => root,
    path: '/',
    component: Label,
  })
  const other = createRoute({
    getParentRoute: () => root,
    path: '/other',
    component: Label,
  })
  const router = createRouter({
    routeTree: root.addChildren([home, other]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
    InnerWrap: RouteLanguageProvider,
    isServer: true,
  })
  await router.load()
  const first = renderToStaticMarkup(<RouterProvider router={router} />)
  assert.match(first, /<footer><span>Home<\/span><\/footer>/)
  assert.equal(appI18n.language, 'zhCN')
  router.history.push('/other')
  await router.load()
  const second = renderToStaticMarkup(<RouterProvider router={router} />)
  assert.ok(second.includes(appI18n.t('Home')))
  assert.equal(appI18n.language, 'zhCN')
})
