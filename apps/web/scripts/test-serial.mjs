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
/*
Copyright (C) 2026 LIghtJUNction
*/

import { spawnSync } from 'node:child_process'
import { readdirSync } from 'node:fs'
import { join, relative, resolve } from 'node:path'

const root = resolve(import.meta.dirname, '..')
const source = join(root, 'src')
const scripts = join(root, 'scripts')
const preload = join(scripts, 'test-preload.mjs')
const sourceTests = []
const scriptTests = []
const failures = []

function collect(directory, pattern, target) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const filePath = join(directory, entry.name)
    if (entry.isDirectory()) {
      collect(filePath, pattern, target)
    } else if (pattern.test(entry.name)) {
      target.push(relative(root, filePath))
    }
  }
}

function run(executable, args) {
  const result = spawnSync(executable, args, {
    cwd: root,
    stdio: ['ignore', 'inherit', 'inherit'],
  })
  if (result.error) console.error(result.error)
  if (result.error || result.status !== 0) {
    failures.push({ file: args.at(-1), status: result.status ?? 1 })
  }
}

collect(source, /\.test\.(?:ts|tsx)$/, sourceTests)
collect(scripts, /\.test\.mjs$/, scriptTests)
sourceTests.sort()
scriptTests.sort()

if (sourceTests.length === 0 && scriptTests.length === 0) {
  throw new Error('no web tests found')
}

for (const test of sourceTests) {
  run(process.execPath, [
    'test',
    '--preload',
    preload,
    '--timeout',
    '15000',
    test,
  ])
}

for (const test of scriptTests) {
  run('node', ['--test', test])
}

if (failures.length > 0) {
  console.error('Web test failures:', JSON.stringify(failures, null, 2))
  process.exit(1)
}

console.log(
  `web tests passed serially: ${sourceTests.length} source + ${scriptTests.length} script files`
)
