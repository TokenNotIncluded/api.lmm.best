import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  inspectProtectedGroupRules,
  replaceEnabledRuleGroups,
} from './protected-group-rules'

describe('protected group rule helpers', () => {
  test('collects groups from enabled rules only', () => {
    const value = JSON.stringify({
      version: 1,
      rules: [
        { id: 'a', enabled: true, groups: ['vip', 'default'] },
        { id: 'b', enabled: false, groups: ['disabled'] },
        { id: 'c', enabled: true, groups: ['default', 'pro'] },
      ],
    })

    assert.deepEqual(inspectProtectedGroupRules(value), {
      enabledRuleCount: 2,
      groups: ['default', 'pro', 'vip'],
      valid: true,
    })
  })

  test('replaces groups on every enabled rule and preserves other fields', () => {
    const value = JSON.stringify({
      version: 3,
      metadata: { owner: 'admin' },
      rules: [
        {
          id: 'enabled',
          enabled: true,
          groups: ['old'],
          patterns: ['secret'],
        },
        { id: 'disabled', enabled: false, groups: ['keep'] },
      ],
    })

    const updated = replaceEnabledRuleGroups(value, ['vip', 'default', 'vip'])
    assert.notEqual(updated, null)
    assert.deepEqual(JSON.parse(updated as string), {
      version: 3,
      metadata: { owner: 'admin' },
      rules: [
        {
          id: 'enabled',
          enabled: true,
          groups: ['default', 'vip'],
          patterns: ['secret'],
        },
        { id: 'disabled', enabled: false, groups: ['keep'] },
      ],
    })
  })

  test('preserves the legacy array document shape', () => {
    const value = JSON.stringify([
      { id: 'enabled', enabled: true, groups: ['old'] },
    ])

    const updated = replaceEnabledRuleGroups(value, ['FREE'])
    assert.notEqual(updated, null)
    assert.deepEqual(JSON.parse(updated as string), [
      { id: 'enabled', enabled: true, groups: ['FREE'] },
    ])
  })

  test('refuses invalid documents, empty assignments, and wildcard groups', () => {
    assert.deepEqual(inspectProtectedGroupRules('{'), {
      enabledRuleCount: 0,
      groups: [],
      valid: false,
    })
    assert.equal(replaceEnabledRuleGroups('{', ['vip']), null)
    assert.equal(
      replaceEnabledRuleGroups(
        JSON.stringify({ rules: [{ enabled: true, groups: ['vip'] }] }),
        []
      ),
      null
    )
    assert.equal(
      replaceEnabledRuleGroups(
        JSON.stringify({ rules: [{ enabled: true, groups: ['vip'] }] }),
        ['*']
      ),
      null
    )
    assert.equal(
      replaceEnabledRuleGroups(
        JSON.stringify({ rules: [{ enabled: false, groups: ['vip'] }] }),
        ['pro']
      ),
      null
    )
  })
})
