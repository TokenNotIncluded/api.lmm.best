/*
Copyright (C) 2026 LIghtJUNction

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

*/
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { describe, test } from 'node:test'

import type { NavGroup, NavLink } from '../types'
import {
  getConsolePageTitle,
  getConsoleSiteLinks,
  organizeConsoleNavigation,
} from './console-navigation'

const overview: NavLink = { title: 'Overview', url: '/dashboard/overview' }
const models: NavLink = {
  title: 'Models and pricing',
  url: '/pricing',
  interaction: 'model-panel',
}
const keys: NavLink = { title: 'API Keys', url: '/keys' }
const wallet: NavLink = { title: 'Wallet', url: '/wallet', badge: '2' }
const drawing: NavLink = { title: 'Drawing studio', url: '/drawing' }
const groups: NavGroup[] = [
  { id: 'general', title: 'Use AI', items: [drawing, models, overview] },
  { id: 'developer', title: 'Developers', items: [keys] },
  { id: 'personal', title: 'Account', items: [wallet] },
]

describe('console navigation organization', () => {
  test('promotes frequent tasks in a stable order without cloning entries', () => {
    const result = organizeConsoleNavigation(groups, 'Console')
    assert.equal(result[0].id, 'console-primary')
    assert.deepEqual(result[0].items, [overview, models, keys, wallet])
    assert.equal(result[0].items[1], models)
    assert.equal(result[0].items[3], wallet)
    assert.deepEqual(result[1].items, [drawing])
  })

  test('does not restore role- or module-filtered destinations', () => {
    const result = organizeConsoleNavigation(
      [{ id: 'general', title: 'AI', items: [drawing] }],
      'Console'
    )
    assert.deepEqual(result.flatMap((group) => group.items), [drawing])
    assert.equal(result.some((group) => group.id === 'console-primary'), false)
  })

  test('preserves every entry exactly once and never mutates source groups', () => {
    const original = JSON.stringify(groups)
    const result = organizeConsoleNavigation(groups, 'Console')
    const before = groups.flatMap((group) => group.items)
    const after = result.flatMap((group) => group.items)
    assert.equal(after.length, before.length)
    for (const item of before) assert.equal(after.filter((x) => x === item).length, 1)
    assert.equal(JSON.stringify(groups), original)
  })

  test('retains nested admin menus, role requirements and badges', () => {
    const admin: NavGroup = {
      id: 'admin',
      title: 'Admin',
      items: [{ title: 'Operations', requiredRole: 10, items: [wallet] }],
    }
    assert.deepEqual(organizeConsoleNavigation([admin], 'Console'), [admin])
  })

  test('leaves onboarding untouched and omits empty groups', () => {
    const onboarding = [{ ...groups[0], id: 'onboarding' }]
    assert.equal(organizeConsoleNavigation(onboarding, 'Console'), onboarding)
    assert.deepEqual(organizeConsoleNavigation([], 'Console'), [])
    assert.deepEqual(
      organizeConsoleNavigation([{ id: 'empty', title: '', items: [] }], 'Console'),
      []
    )
  })
})

describe('current location and secondary site links', () => {
  test('resolves query strings, detail routes and trailing slashes', () => {
    assert.equal(getConsolePageTitle(groups, '/keys/?page=2#list'), 'API Keys')
    assert.equal(getConsolePageTitle(groups, '/keys/123'), 'API Keys')
    assert.equal(getConsolePageTitle(groups, '/keys-other'), undefined)
  })

  test('prefers the specific destination and supports aliases and nested items', () => {
    const nested: NavGroup[] = [{ title: 'Admin', items: [
      { title: 'Subscriptions', url: '/subscriptions' },
      { title: 'Reset', url: '/subscriptions/reset' },
      { title: 'Logs', items: [{
        title: 'Task Logs', url: '/usage-logs/task', activeUrls: ['/usage-logs/drawing'],
      }] },
    ] }]
    assert.equal(getConsolePageTitle(nested, '/subscriptions/reset'), 'Reset')
    assert.equal(getConsolePageTitle(nested, '/usage-logs/drawing'), 'Task Logs')
  })

  test('removes duplicate site links without dropping external/disabled semantics', () => {
    const external = { title: 'Docs', href: 'https://example.invalid/docs', external: true }
    const disabled = { title: 'Keys unavailable', href: '/keys', disabled: true }
    assert.deepEqual(getConsoleSiteLinks([
      { title: 'Keys', href: '/keys' }, external, external, disabled,
    ], groups), [external, disabled])
  })
})

describe('single console navigation contract', () => {
  test('removes parallel top navigation and scopes sidebar-free mode to L0', () => {
    const source = readFileSync(
      new URL('../components/authenticated-layout.tsx', import.meta.url),
      'utf8'
    )
    assert.match(source, /showTopNav=\{false\}/)
    assert.match(source, /focusedOnboarding = assistantPage && !consoleActivated/)
    assert.match(source, /focusedOnboarding \? null : <AppSidebar/)
    assert.match(source, /showLanguageSwitcher=\{focusedOnboarding\}/)
    assert.match(source, /showConfigDrawer=\{focusedOnboarding\}/)
  })
})
