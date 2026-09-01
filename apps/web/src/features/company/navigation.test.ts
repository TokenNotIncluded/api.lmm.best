/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { test } from 'node:test'

import {
  SIDEBAR_DEFAULT_ROUTE_ALLOWLIST,
  SIDEBAR_ROUTE_MODULE,
  SIDEBAR_ROUTE_SECTION,
  isSidebarRouteEnabledByModules,
} from '@/lib/sidebar-preferences'

test('company is an authenticated personal route with independent visibility', async () => {
  assert.equal(SIDEBAR_DEFAULT_ROUTE_ALLOWLIST.has('/company'), true)
  assert.equal(SIDEBAR_ROUTE_SECTION['/company'], 'personal')
  assert.deepEqual(SIDEBAR_ROUTE_MODULE['/company'], {
    section: 'personal',
    module: 'company',
  })
  assert.equal(
    isSidebarRouteEnabledByModules('/company', {
      personal: { enabled: true, company: false },
    }),
    false
  )

  const [routeSource, sidebarSource, dropdownSource] = await Promise.all([
    readFile(
      new URL('../../routes/_authenticated/company/index.tsx', import.meta.url),
      'utf8'
    ),
    readFile(
      new URL('../../hooks/use-sidebar-data.ts', import.meta.url),
      'utf8'
    ),
    readFile(
      new URL('../../components/profile-dropdown.tsx', import.meta.url),
      'utf8'
    ),
  ])

  assert.match(routeSource, /createFileRoute\('\/_authenticated\/company\/'\)/)
  assert.match(sidebarSource, /title: t\('Company'\)[\s\S]*url: '\/company'/)
  assert.match(dropdownSource, /useIsSidebarModuleVisible\('\/company'\)/)
  assert.match(dropdownSource, /navigate\(\{ to: '\/company' \}\)/)
})
