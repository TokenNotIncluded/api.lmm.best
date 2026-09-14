#!/usr/bin/env node
/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/

import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import path from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url))
const script = path.join(scriptDirectory, 'operator-persona-suite.mjs')
const repository = path.resolve(scriptDirectory, '../../..')
const outputDirectory = path.join(repository, 'apps/web/scripts')

function run(environment) {
  return spawnSync(process.execPath, [script], {
    cwd: repository,
    env: {
      ...process.env,
      PERSONA_REVIEW_URL: 'http://127.0.0.1:4174',
      PERSONA_DEPLOY_WORKSPACE: repository,
      PERSONA_OUTPUT_DIR: outputDirectory,
      ...environment,
    },
    encoding: 'utf8',
  })
}

test('rejects a production origin before reading workspace credentials', () => {
  const result = run({ PERSONA_REVIEW_URL: 'https://api.lmm.best' })
  assert.notEqual(result.status, 0)
  assert.match(
    `${result.stdout}\n${result.stderr}`,
    /PERSONA_REVIEW_URL must be exactly http:\/\/127\.0\.0\.1:4174/
  )
})

test('rejects a local run without a marker-owned deployment workspace', () => {
  const result = run({})
  assert.notEqual(result.status, 0)
  assert.match(
    `${result.stdout}\n${result.stderr}`,
    /deployment marker is missing|deployment workspace marker is missing|ENOENT: no such file or directory/
  )
})

test('rejects an alternate local port', () => {
  const result = run({ PERSONA_REVIEW_URL: 'http://127.0.0.1:4175' })
  assert.notEqual(result.status, 0)
  assert.match(
    `${result.stdout}\n${result.stderr}`,
    /PERSONA_REVIEW_URL must be exactly http:\/\/127\.0\.0\.1:4174/
  )
})
