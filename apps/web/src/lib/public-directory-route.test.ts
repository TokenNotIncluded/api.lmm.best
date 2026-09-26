/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

import { isPublicDirectoryPath } from './public-directory-route'

test('the directory is public with or without a trailing slash', () => {
  assert.equal(isPublicDirectoryPath('/ai-directory'), true)
  assert.equal(isPublicDirectoryPath('/ai-directory/'), true)
})

test('private routes, descendants and similar prefixes remain protected', () => {
  for (const pathname of [
    '/',
    '/dashboard',
    '/wallet',
    '/system-settings/site/ai-directory',
    '/system-settings/site/ai-directory-ads',
    '/ai-directory/ads',
    '/ai-directory/private',
    '/ai-directory-other',
    '/ai-directory//',
  ]) {
    assert.equal(isPublicDirectoryPath(pathname), false, pathname)
  }
})

test('the public layout and guard use the same exact path boundary', () => {
  const route = readFileSync(
    new URL('../routes/_authenticated/route.tsx', import.meta.url),
    'utf8'
  )
  assert.match(
    route,
    /if \(isPublicDirectoryPath\(location.pathname\)\) return/
  )
  assert.match(route, /if \(isPublicDirectoryPath\(pathname\)\)/)
  assert.match(route, /<ForgePublicShell>/)
  assert.match(route, /if \(!auth.user \|\| !auth.accessToken\)/)
  assert.match(route, /!isConsoleActivated\(auth.user\)/)
  assert.match(route, /!isContributorRoute\(location.pathname\)/)
})

test('public directory rendering does not wait for an account session', () => {
  const root = readFileSync(
    new URL('../routes/__root.tsx', import.meta.url),
    'utf8'
  )
  const publicPaths = root.match(
    /const NON_BLOCKING_PUBLIC_PATHS = \[([\s\S]*?)\] as const/
  )?.[1]
  assert.ok(publicPaths?.includes("'/ai-directory'"))
})
