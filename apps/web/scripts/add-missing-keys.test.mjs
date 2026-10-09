/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import {
  mkdtemp,
  mkdir,
  copyFile,
  readFile,
  writeFile,
  rm,
} from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'

import { balanceQueryCopy } from './balance-query-copy.mjs'
import { trustLevelResetCopy } from './trust-level-reset-copy.mjs'

const repoRoot = fileURLToPath(new URL('../../../', import.meta.url))
const locales = ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi']

async function verifyScopedTranslations(scope, expectedKeys) {
  const fixture = await mkdtemp(path.join(tmpdir(), 'lmm-i18n-scripts-'))
  try {
    const scripts = execFileSync(
      'git',
      ['ls-files', '-z', 'apps/web/scripts'],
      {
        cwd: repoRoot,
        encoding: 'utf8',
      }
    )
      .split('\0')
      .filter((file) => file.endsWith('.mjs'))
    await mkdir(path.join(fixture, 'scripts'), { recursive: true })
    for (const script of scripts) {
      await copyFile(
        path.join(repoRoot, script),
        path.join(fixture, 'scripts', path.basename(script))
      )
    }
    const localeDir = path.join(fixture, 'src/i18n/locales')
    await mkdir(localeDir, { recursive: true })
    for (const locale of locales) {
      await writeFile(
        path.join(localeDir, `${locale}.json`),
        JSON.stringify({
          translation: {
            'Existing fixture': 'keep',
            Account: `existing-${locale}`,
          },
        })
      )
    }
    execFileSync(process.execPath, ['scripts/add-missing-keys.mjs', scope], {
      cwd: fixture,
      stdio: 'pipe',
    })
    let keys
    for (const locale of locales) {
      const { translation } = JSON.parse(
        await readFile(path.join(localeDir, `${locale}.json`), 'utf8')
      )
      assert.equal(translation['Existing fixture'], 'keep')
      assert.equal(translation.Account, `existing-${locale}`)
      const added = Object.keys(translation)
        .filter((key) => !['Existing fixture', 'Account'].includes(key))
        .sort()
      assert.ok(added.length > 0, `${locale} must receive scoped translations`)
      if (expectedKeys) assert.deepEqual(added, expectedKeys)
      if (keys) {
        assert.deepEqual(added, keys, `${locale} must receive the same keys`)
      } else {
        keys = added
      }
      for (const key of added) assert.equal(typeof translation[key], 'string')
    }
  } finally {
    await rm(fixture, { recursive: true, force: true })
  }
}

for (const scope of ['--only-passkey', '--only-response-model']) {
  test(`scoped translation writes run with only committed script dependencies (${scope})`, () =>
    verifyScopedTranslations(scope))
}

const responsesWebSocketKeys = [
  'Allow persistent connections to /v1/responses.',
  'Requires a /v1/responses route with no converter.',
  'Responses WebSocket',
  'Responses WebSocket is unavailable on the current backend.',
].sort()
test('Responses WebSocket scope reproduces only its four keys in every locale', () =>
  verifyScopedTranslations(
    '--only-responses-websocket',
    responsesWebSocketKeys
  ))

const balanceQueryKeys = Object.keys(balanceQueryCopy.en).sort()
test('Automatic trust reset scope reproduces only its two keys in every locale', () =>
  verifyScopedTranslations(
    '--only-trust-level-reset',
    Object.keys(trustLevelResetCopy.en).sort()
  ))
test('Balance query scope reproduces only its 25 keys in every locale', () => {
  assert.equal(balanceQueryKeys.length, 25)
  return verifyScopedTranslations('--only-balance-query', balanceQueryKeys)
})
