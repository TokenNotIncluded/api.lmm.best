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

const repoRoot = fileURLToPath(new URL('../../../', import.meta.url))
const locales = ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi']

test('scoped translation writes run with only committed script dependencies', async () => {
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
        JSON.stringify({ translation: { 'Existing fixture': 'keep' } })
      )
    }
    execFileSync(
      process.execPath,
      ['scripts/add-missing-keys.mjs', '--only-passkey'],
      {
        cwd: fixture,
        stdio: 'pipe',
      }
    )
    let keys
    for (const locale of locales) {
      const { translation } = JSON.parse(
        await readFile(path.join(localeDir, `${locale}.json`), 'utf8')
      )
      assert.equal(translation['Existing fixture'], 'keep')
      const added = Object.keys(translation)
        .filter((key) => key !== 'Existing fixture')
        .sort()
      assert.ok(added.length > 0, `${locale} must receive scoped translations`)
      if (keys)
        assert.deepEqual(added, keys, `${locale} must receive the same keys`)
      else keys = added
      for (const key of added) assert.equal(typeof translation[key], 'string')
    }
  } finally {
    await rm(fixture, { recursive: true, force: true })
  }
})
