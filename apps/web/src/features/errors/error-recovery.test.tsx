/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
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
import type { ReactNode } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'

import appI18n from '@/i18n/config'
import { useAuthStore } from '@/stores/auth-store'

import { ForbiddenError } from './forbidden'
import { GeneralError } from './general-error'
import { MaintenanceError } from './maintenance-error'
import { NotFoundError } from './not-found-error'
import { UnauthorisedError } from './unauthorized-error'

async function renderError(node: ReactNode) {
  if (!appI18n.isInitialized) {
    await new Promise<void>((resolve) => appI18n.on('initialized', resolve))
  }
  await appI18n.changeLanguage('en')
  useAuthStore.getState().auth.setUser(null)
  const root = createRootRoute({ component: Outlet })
  const page = createRoute({
    getParentRoute: () => root,
    path: '/',
    component: () => node,
  })
  const router = createRouter({
    routeTree: root.addChildren([page]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
    isServer: true,
  })
  await router.load()
  return renderToStaticMarkup(<RouterProvider router={router} />)
}

test('server errors retain recovery and reporting actions without a game', async () => {
  const html = await renderError(<GeneralError />)
  assert.match(html, /aria-label="500"/)
  assert.match(html, /The request fell over/)
  assert.match(html, />Retry</)
  assert.match(html, />Back to Home</)
  assert.match(html, /issues" target="_blank" rel="noopener noreferrer"/)
  assert.doesNotMatch(
    html,
    /Signal tuner|signal-tuner|error-editorial-play|>Play</
  )

  const limited = await renderError(
    <GeneralError error={{ response: { status: 429 } }} />
  )
  assert.match(limited, /aria-label="429"/)
  assert.match(limited, /Too many requests/)
  assert.match(limited, />Retry</)
  assert.doesNotMatch(limited, /signal-tuner/)
})

test('other error states retain their normal recovery controls without the shared toy', async () => {
  for (const [node, status, action] of [
    [<UnauthorisedError key='unauthorized' />, '401', 'Sign in to get started'],
    [<ForbiddenError key='forbidden' />, '403', 'Sign in to get started'],
    [<NotFoundError key='not-found' />, '404', 'Back to Home'],
    [<MaintenanceError key='maintenance' />, '503', 'Retry'],
  ] as const) {
    const html = await renderError(node)
    assert.ok(html.includes(`aria-label="${status}"`), status)
    assert.ok(html.includes(`>${action}<`), status)
    assert.doesNotMatch(
      html,
      /Signal tuner|signal-tuner|error-editorial-play|>Play</
    )
  }
  const minimal = await renderError(<GeneralError minimal />)
  assert.match(minimal, /Reload the page to try again/)
  assert.doesNotMatch(minimal, /aria-label="500"|>Retry<|signal-tuner/)
})
