import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { writeFileSync, readFileSync, mkdirSync } from 'node:fs'
import path from 'node:path'

const SOURCE = 'd3fb115c097e2eedb518b1f4b061351b468672ee'
const OLD_BASE = '7d54279f28ffbd35818d1008fdcedbef5c4fa1e4'
const BASE = 'e0573fb5a219233d11420c08f2919ff29182e936'
const output = process.env.STOCK_FINAL_OUTPUT
assert.ok(output)
mkdirSync(output, { recursive: true })
const git = (...args) => execFileSync('git', args, { encoding: 'utf8', maxBuffer: 32 << 20 }).trimEnd()
const show = (ref, file) => execFileSync('git', ['show', `${ref}:${file}`], { encoding: 'utf8', maxBuffer: 32 << 20 })
const api = (endpoint, payload) => JSON.parse(execFileSync('gh', ['api', '--method', 'POST', `repos/TokenNotIncluded/api.lmm.best/git/${endpoint}`, '--input', '-'], { input: JSON.stringify(payload), encoding: 'utf8', maxBuffer: 4 << 20 }))
const localeDirectory = 'apps/web/src/i18n/locales/'
const paths = git('diff', '--name-only', OLD_BASE, SOURCE).split('\n')
const upstream = new Set(git('diff', '--name-only', OLD_BASE, BASE).split('\n'))
for (const file of paths) {
  assert.ok(file.startsWith('apps/') || file === '.github/workflows/store-stock-review.yml')
  if (!file.startsWith(localeDirectory)) assert.ok(!upstream.has(file), `Concurrent source edit needs manual review: ${file}`)
}
const copySource = show(SOURCE, 'apps/web/scripts/store-stock-copy.mjs')
const { storeStockCopy } = await import(`data:text/javascript;base64,${Buffer.from(copySource).toString('base64')}`)
git('checkout', '--detach', BASE)
for (const file of paths.filter(file => !file.startsWith(localeDirectory))) {
  mkdirSync(path.dirname(file), { recursive: true })
  writeFileSync(file, show(SOURCE, file))
}
// Preserve the existing manual-review contract without removing any test job.
for (const name of ['pi-remote-control-review.yml', 'task-drawing-logs-review.yml']) {
  const file = `.github/workflows/${name}`
  const before = show(BASE, file)
  const after = before.replace(/on:\n[\s\S]*?\npermissions:/, 'on:\n  workflow_dispatch:\npermissions:')
  if (after !== before) {
    writeFileSync(file, after)
    paths.push(file)
  }
}
const audit = []
for (const [locale, copy] of Object.entries(storeStockCopy)) {
  const file = `${localeDirectory}${locale}.json`
  const before = show(BASE, file)
  const original = JSON.parse(before)
  for (const key of Object.keys(copy)) assert.ok(!Object.hasOwn(original.translation, key))
  const suffix = '\n  }\n}\n'
  assert.ok(before.endsWith(suffix))
  const entries = Object.entries(copy).map(([key, value]) => `    ${JSON.stringify(key)}: ${JSON.stringify(value)}`).join(',\n')
  const content = before.slice(0, -suffix.length) + ',\n' + entries + suffix
  assert.deepEqual(JSON.parse(content), { ...original, translation: { ...original.translation, ...copy } })
  writeFileSync(file, content)
  audit.push({ locale, added: Object.keys(copy), preservedExistingKeys: Object.keys(original.translation).length })
}
execFileSync('bun', ['install', '--frozen-lockfile'], { stdio: 'inherit' })
const formatFiles = ['scripts/store-stock-review.mjs', 'scripts/store-stock-copy.mjs',
  'src/features/store/seller-page.tsx', 'src/features/store/stock-status.ts',
  'src/features/store/stock-status.test.ts', 'src/features/store/catalogue-tags.tsx',
  'src/features/usage-logs/lib/async-task-logs.ts', 'src/features/usage-logs/lib/__tests__/safe-media-url-controls.test.ts']
execFileSync('bash', ['-euc', 'PATH="$PWD/node_modules/.bin:$PWD/../../node_modules/.bin:$PATH" oxfmt --write "$@"', 'stock-format', ...formatFiles], { cwd: 'apps/web', stdio: 'inherit' })
git('add', '--', ...paths)
const expectedTree = git('write-tree')
const changes = git('diff', '--cached', '--name-only').split('\n')
assert.ok(changes.every(file => paths.includes(file)))
const tree = []
for (const file of changes) {
  const content = readFileSync(file, 'utf8')
  const blob = api('blobs', { content, encoding: 'utf-8' })
  assert.equal(blob.sha, git('hash-object', file))
  tree.push({ path: file, mode: '100644', type: 'blob', sha: blob.sha })
}
// The Actions token cannot create a tree that modifies workflows. Do not try
// that write here. Test the local tree and hand its checked blobs to the user-
// authorized connector for a normal commit and PR merge.
const localCommit = git('-c', 'user.name=Stock regression review', '-c', 'user.email=stock-review@users.noreply.github.com', 'commit-tree', expectedTree, '-p', SOURCE, '-p', BASE, '-m', 'Local stock qualification candidate')
git('checkout', '--detach', localCommit)
assert.equal(git('rev-parse', 'HEAD^{tree}'), expectedTree)
writeFileSync(path.join(output, 'candidate.json'), JSON.stringify({ localCommit, tree: expectedTree, source: SOURCE, base: BASE, baseTree: git('rev-parse', `${BASE}^{tree}`), files: tree }, null, 2) + '\n')
writeFileSync(path.join(output, 'translation-audit.json'), JSON.stringify(audit, null, 2) + '\n')
writeFileSync(path.join(output, 'revision.txt'), localCommit + '\n')
writeFileSync(path.join(output, 'change-stat.txt'), git('diff', '--stat', BASE, 'HEAD') + '\n')
console.log(`Local tree ${expectedTree}; no repository branch or workflow updated`)
