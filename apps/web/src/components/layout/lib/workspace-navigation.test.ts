/*
Copyright (C) 2023-2026 QuantumNous

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

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { NavGroup } from '../types'
import { getWorkspaceCopy } from './workspace-copy'
import {
  filterWorkspaceNavigation,
  selectWorkspaceShortcuts,
} from './workspace-navigation'

function navigation(): NavGroup[] {
  return [
    {
      id: 'general',
      title: 'General',
      items: [
        { title: 'API Keys', url: '/keys' },
        { title: 'Models', url: '/pricing', interaction: 'model-panel' },
        { title: 'Usage Logs', url: '/usage-logs/common' },
        { title: 'Unavailable', url: '/disabled', disabled: true },
      ],
    },
    {
      id: 'personal',
      title: 'Personal',
      items: [{ title: 'Wallet', url: '/wallet' }],
    },
    {
      id: 'settings',
      title: 'Settings',
      items: [
        {
          title: 'Billing',
          items: [
            { title: 'Rates', url: '/settings/rates' },
            { title: 'Hidden', url: '/settings/hidden', disabled: true },
          ],
        },
      ],
    },
  ]
}

test('blank search preserves registry identity', () => {
  const groups = navigation()
  assert.equal(filterWorkspaceNavigation(groups, '  '), groups)
})

test('case-insensitive search matches all title and URL tokens', () => {
  const result = filterWorkspaceNavigation(navigation(), 'GENERAL api')
  assert.equal(result.length, 1)
  assert.deepEqual(
    result[0].items.map((item) => item.url),
    ['/keys']
  )
})

test('Chinese and accented labels are searchable', () => {
  const groups: NavGroup[] = [
    {
      title: '个人',
      items: [
        { title: '账户密钥', url: '/keys' },
        { title: 'Crédit', url: '/wallet' },
      ],
    },
  ]
  const chinese = filterWorkspaceNavigation(groups, '密钥')
  const french = filterWorkspaceNavigation(groups, 'credit')
  assert.equal(chinese[0].items[0].url, '/keys')
  assert.equal(french[0].items[0].url, '/wallet')
})

test('nested results are flattened without changing their destination', () => {
  const items = filterWorkspaceNavigation(navigation(), 'rates')[0].items
  assert.equal(items.length, 1)
  assert.equal(items[0].title, 'Billing / Rates')
  assert.equal(items[0].url, '/settings/rates')
  assert.equal(items[0].items, undefined)
})

test('unknown queries do not synthesize destinations', () => {
  assert.deepEqual(filterWorkspaceNavigation(navigation(), '/admin/root'), [])
})

test('disabled parents and children cannot become search results', () => {
  const groups = navigation()
  groups[2].items[0].disabled = true
  assert.deepEqual(filterWorkspaceNavigation(groups, 'rates'), [])
  assert.deepEqual(filterWorkspaceNavigation(navigation(), 'hidden'), [])
  assert.deepEqual(filterWorkspaceNavigation(navigation(), 'Unavailable'), [])
})

test('model-panel and dynamic preset interactions survive filtering', () => {
  const groups = navigation()
  groups[0].items.push({ title: 'Chat presets', type: 'chat-presets' })
  const model = filterWorkspaceNavigation(groups, 'model')[0].items[0]
  const preset = filterWorkspaceNavigation(groups, 'presets')[0].items[0]
  assert.equal(model.interaction, 'model-panel')
  assert.equal(preset.type, 'chat-presets')
})

test('filtering and quick access do not mutate their input', () => {
  const groups = navigation()
  const before = JSON.stringify(groups)
  for (const group of groups) {
    for (const item of group.items) {
      if (item.items) {
        item.items.forEach(Object.freeze)
        Object.freeze(item.items)
      }
      Object.freeze(item)
    }
    Object.freeze(group.items)
    Object.freeze(group)
  }
  Object.freeze(groups)
  filterWorkspaceNavigation(groups, 'billing')
  selectWorkspaceShortcuts(groups)
  assert.equal(JSON.stringify(groups), before)
})

test('quick access preserves priority and original entry identity', () => {
  const groups = navigation()
  const shortcuts = selectWorkspaceShortcuts(groups)
  assert.deepEqual(
    shortcuts.map((item) => item.url),
    ['/keys', '/pricing', '/wallet', '/usage-logs/common']
  )
  assert.equal(shortcuts[0], groups[0].items[0])
  assert.equal(shortcuts[1].interaction, 'model-panel')
})

test('omitted or disabled destinations do not return as shortcuts', () => {
  const groups = navigation()
  groups[0].items = groups[0].items.filter((item) => item.url !== '/keys')
  groups[1].items[0].disabled = true
  assert.deepEqual(
    selectWorkspaceShortcuts(groups).map((item) => item.url),
    ['/pricing', '/usage-logs/common']
  )
  assert.deepEqual(selectWorkspaceShortcuts([]), [])
})

test('shortcuts preserve parent and child role restrictions', () => {
  const groups: NavGroup[] = [
    {
      title: 'Restricted',
      items: [
        { title: 'Keys', url: '/keys', requiredRole: 10 },
        {
          title: 'Billing',
          requiredRole: 100,
          items: [{ title: 'Wallet', url: '/wallet' }],
        },
      ],
    },
  ]
  assert.deepEqual(selectWorkspaceShortcuts(groups), [])
  assert.deepEqual(
    selectWorkspaceShortcuts(groups, 10).map((item) => item.url),
    ['/keys']
  )
  assert.equal(selectWorkspaceShortcuts(groups, 100).length, 2)
})

test('external URLs, query variants and duplicate routes are excluded', () => {
  const groups: NavGroup[] = [
    {
      title: 'Links',
      items: [
        { title: 'External', url: 'https://example.test/keys' },
        { title: 'Query', url: '/keys?admin=1' },
        { title: 'Keys', url: '/keys' },
        { title: 'Duplicate', url: '/keys' },
      ],
    },
  ]
  assert.deepEqual(
    selectWorkspaceShortcuts(groups).map((item) => item.title),
    ['Keys']
  )
})

test('seven locales provide the same nonempty copy contract', () => {
  const english = getWorkspaceCopy('en')
  for (const language of ['en', 'zhCN', 'zhTW', 'fr', 'ru', 'ja', 'vi']) {
    const copy = getWorkspaceCopy(language)
    assert.deepEqual(Object.keys(copy), Object.keys(english))
    assert.ok(Object.values(copy).every((value) => value.length > 0))
  }
})

test('locale aliases and prototype names fall back safely', () => {
  assert.equal(getWorkspaceCopy('zh-Hant-HK'), getWorkspaceCopy('zhTW'))
  assert.equal(getWorkspaceCopy('zh_CN'), getWorkspaceCopy('zhCN'))
  assert.equal(getWorkspaceCopy('fr-FR'), getWorkspaceCopy('fr'))
  for (const locale of ['', 'xx', 'constructor', '__proto__', 'toString']) {
    assert.equal(getWorkspaceCopy(locale), getWorkspaceCopy('en'))
  }
})
