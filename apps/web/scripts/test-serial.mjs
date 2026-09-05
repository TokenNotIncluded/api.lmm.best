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
const preload = join(root, 'scripts', 'test-preload.mjs')
const tests = []

function collect(directory) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) {
      collect(path)
    } else if (/\.test\.(?:ts|tsx)$/.test(entry.name)) {
      tests.push(relative(root, path))
    }
  }
}

collect(source)
tests.sort()
if (tests.length === 0) throw new Error('no web tests found')

for (const test of tests) {
  const result = spawnSync(
    process.execPath,
    ['test', '--preload', preload, '--timeout', '15000', test],
    { cwd: root, encoding: 'utf8' }
  )
  if (result.stdout) process.stdout.write(result.stdout)
  if (result.stderr) process.stderr.write(result.stderr)
  if (result.error) throw result.error
  if (result.status !== 0) process.exit(result.status ?? 1)
}

console.log(`web tests passed serially: ${tests.length} files`)
