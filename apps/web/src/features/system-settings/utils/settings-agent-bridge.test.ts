/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'

import {
  inspectSettingsAgentForms,
  registerSettingsAgentForm,
  stageSettingsAgentForm,
  type SettingsAgentField,
} from './settings-agent-bridge'

const cleanups: (() => void)[] = []
afterEach(() => {
  cleanups.splice(0).forEach((close) => close())
})
function fixture(validate = async () => true) {
  const values: Record<string, unknown> = {
    SystemName: 'Original',
    DataExportInterval: 60,
    SecretKey: 'do-not-expose',
  }
  const fields: SettingsAgentField[] = ['SystemName', 'DataExportInterval']
  let notified = 0
  const close = registerSettingsAgentForm({
    owner: 10,
    path: '/system-settings/site/system-info',
    fields,
    read: (name) => values[name],
    write: (name, value) => {
      values[name] = value
    },
    validate,
    isSaving: () => false,
    changed: (name) =>
      values[name] !== (name === 'SystemName' ? 'Original' : 60),
    notify: () => {
      notified++
    },
  })
  cleanups.push(close)
  const [{ id }] = inspectSettingsAgentForms(
    10,
    '/system-settings/site/system-info'
  )
  const controller = new AbortController()
  let allowed = true
  const run = (changes: Record<string, unknown>) =>
    stageSettingsAgentForm({
      id,
      owner: 10,
      path: '/system-settings/site/system-info',
      changes,
      signal: controller.signal,
      assertAccess: () => {
        if (!allowed) throw new Error('access lost')
      },
    })
  return {
    values,
    run,
    controller,
    close,
    revoke: () => {
      allowed = false
    },
    notifications: () => notified,
  }
}

test('only opted-in fields are returned for the same account and mounted page', () => {
  const page = fixture()
  assert.equal(
    inspectSettingsAgentForms(99, '/system-settings/site/system-info').length,
    0
  )
  assert.equal(inspectSettingsAgentForms(10, '/keys').length, 0)
  const listed = inspectSettingsAgentForms(
    10,
    '/system-settings/site/system-info'
  )
  assert.equal(listed[0].fields.length, 2)
  assert.ok(!JSON.stringify(listed).includes('do-not-expose'))
  page.close()
  assert.equal(
    inspectSettingsAgentForms(10, '/system-settings/site/system-info').length,
    0
  )
})

test('staging validates the existing form and never persists or submits', async () => {
  let validations = 0
  const page = fixture(async () => {
    validations++
    return true
  })
  const result = await page.run({
    SystemName: 'Mobile studio',
    DataExportInterval: 15,
  })
  assert.equal(result.persisted, false)
  assert.equal(page.values.SystemName, 'Mobile studio')
  assert.equal(validations, 1)
  assert.equal(page.notifications(), 1)
  assert.equal(page.values.SecretKey, 'do-not-expose')
})

for (const changes of [
  {},
  { SecretKey: 'leak' },
  { SystemName: 'Changed', Unknown: true },
  { SystemName: '' },
  { SystemName: 'x'.repeat(81) },
  { SystemName: 'bad\nvalue' },
  { DataExportInterval: 0 },
  { DataExportInterval: Infinity },
  { DataExportInterval: 1.5 },
  { DataExportInterval: 1441 },
  { DataExportInterval: '60' },
  JSON.parse('{"__proto__":true}'),
]) {
  test(`invalid request does not partially edit the form: ${JSON.stringify(changes)}`, async () => {
    const page = fixture()
    await assert.rejects(page.run(changes), /Provide|Unsupported/)
    assert.equal(page.values.SystemName, 'Original')
    assert.equal(page.values.DataExportInterval, 60)
    assert.equal(page.notifications(), 0)
  })
}

test('validation failure restores the previous draft', async () => {
  const page = fixture(async () => false)
  await assert.rejects(
    page.run({ SystemName: 'Invalid for this form' }),
    /rejected/
  )
  assert.equal(page.values.SystemName, 'Original')
})

test('already cancelled requests leave the form unchanged', async () => {
  const page = fixture()
  page.controller.abort()
  await assert.rejects(page.run({ SystemName: 'Cancelled' }))
  assert.equal(page.values.SystemName, 'Original')
})

for (const action of ['cancel', 'revoke', 'close', 'human'] as const) {
  test(`async validation handles ${action} without leaking a successful result`, async () => {
    let complete!: (value: boolean) => void
    const page = fixture(
      () =>
        new Promise<boolean>((resolve) => {
          complete = resolve
        })
    )
    const pending = page.run({ SystemName: 'Proposed' })
    await assert.rejects(page.run({ SystemName: 'Another' }), /busy/)
    if (action === 'cancel') page.controller.abort()
    if (action === 'revoke') page.revoke()
    if (action === 'close') page.close()
    if (action === 'human') {
      page.values.SystemName = 'Human edit'
      page.controller.abort()
    }
    complete(true)
    await assert.rejects(pending)
    if (action !== 'close') {
      assert.equal(
        page.values.SystemName,
        action === 'human' ? 'Human edit' : 'Original'
      )
    }
    assert.equal(page.notifications(), 0)
  })
}
