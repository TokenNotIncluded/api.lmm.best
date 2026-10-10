import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'

// Reuse the reviewed preparation exactly, adding scoped formatter/linter
// normalization before recording its content tree. This helper is temporary
// review infrastructure and is not part of the proposed merge.
let source = execFileSync('git', ['show', 'f5c730504ec0e690a44c33498bfd568cc51a2352:scripts/merge-stock-candidate.mjs'], { encoding: 'utf8' })
const marker = "execFileSync('bun', ['test', controlsTest.slice('apps/web/'.length)], { cwd: 'apps/web', stdio: 'inherit' })"
assert.equal(source.split(marker).length, 2)
source = source.replace(marker, `
execFileSync('bash', ['-euc', 'PATH="$PWD/node_modules/.bin:$PWD/../../node_modules/.bin:$PATH" oxlint --fix "$@"', 'stock-lint', commandsFile.slice('apps/web/'.length), reviewFile.slice('apps/web/'.length)], { cwd: 'apps/web', stdio: 'inherit' })
execFileSync('bash', ['-euc', 'PATH="$PWD/node_modules/.bin:$PWD/../../node_modules/.bin:$PATH" oxfmt --write "$@"', 'stock-format', commandsFile.slice('apps/web/'.length), reviewFile.slice('apps/web/'.length)], { cwd: 'apps/web', stdio: 'inherit' })
${marker}`)
await import(`data:text/javascript;base64,${Buffer.from(source).toString('base64')}`)
