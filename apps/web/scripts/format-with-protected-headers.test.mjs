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
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath, pathToFileURL } from 'node:url'

const target = new URL('./format-with-protected-headers.mjs', import.meta.url)
const script = fileURLToPath(target)
const header = '/*\nCopyright (C) 2026 QuantumNous\n*/\n'

function check(t, mode, source, replacement, formatterExit = 0) {
  const root = mkdtempSync(join(tmpdir(), 'lmm-format-test-'))
  const bin = mkdtempSync(join(tmpdir(), 'lmm-format-bin-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  t.after(() => rmSync(bin, { recursive: true, force: true }))
  const path = join(root, 'fixture.ts')
  writeFileSync(path, header + source)
  const encoded = JSON.stringify(replacement)
  const mock = [
    `require('node:fs').writeFileSync('fixture.ts', ${encoded})`,
    `process.exit(${formatterExit})`,
    '',
  ].join('\n')
  const mockPath = join(bin, 'formatter.cjs')
  writeFileSync(mockPath, mock)
  // Resolve only the formatter launch to a real Node child process. A shebang
  // executable on PATH is not portable to Windows and may hit an installed tool.
  const preload = join(bin, 'formatter-launch.mjs')
  writeFileSync(
    preload,
    [
      "import childProcess from 'node:child_process'",
      "import { syncBuiltinESMExports } from 'node:module'",
      "import assert from 'node:assert/strict'",
      'const spawn = childProcess.spawnSync',
      'childProcess.spawnSync = (command, args, options) => {',
      "  assert.equal(command, 'oxfmt')",
      "  assert.deepEqual(args, ['-c', '.oxfmtrc.json', '--ignore-path', '.gitignore', '--write', '.'])",
      `  return spawn(process.execPath, [${JSON.stringify(mockPath)}], options)`,
      '}',
      'syncBuiltinESMExports()',
    ].join('\n')
  )
  const result = spawnSync(
    process.execPath,
    ['--import', pathToFileURL(preload).href, script, mode],
    {
      cwd: root,
      encoding: 'utf8',
      timeout: 10000,
    }
  )
  assert.ifError(result.error)
  return { ...result, source: readFileSync(path, 'utf8') }
}

test('check reports exact first difference and restores protected bytes', (t) => {
  const result = check(t, '--check', 'const value=1\n', 'const value = 1\n')
  assert.equal(result.status, 1)
  assert.match(result.stderr, /First difference at line 4/)
  assert.match(result.stderr, /before: "const value=1"/)
  assert.match(result.stderr, /after: {2}"const value = 1"/)
  assert.equal(result.source, `${header}const value=1\n`)
})

test('formatter failure still fails and restores original bytes', (t) => {
  const result = check(t, '--check', 'original\n', 'partial change\n', 7)
  assert.equal(result.status, 7)
  assert.equal(result.source, `${header}original\n`)
})

test('clean check stays successful without invented differences', (t) => {
  const result = check(t, '--check', 'const value = 1\n', 'const value = 1\n')
  assert.equal(result.status, 0)
  assert.equal(result.stderr, '')
})

test('write preserves header and applies formatting', (t) => {
  const result = check(t, '--write', 'const value=1\n', 'const value = 1\n')
  assert.equal(result.status, 0)
  assert.equal(result.source, `${header}const value = 1\n`)
})

test('diagnostic lines are bounded and cannot emit workflow commands', (t) => {
  const source = `::error::${'x'.repeat(1000)}\n`
  const result = check(t, '--check', source, 'fixed\n')
  assert.equal(result.status, 1)
  assert.match(result.stderr, /First difference at line 4/)
  assert.doesNotMatch(result.stderr, /^::/m)
  assert.ok(result.stderr.length < 600)
  assert.equal(result.source, header + source)
})
