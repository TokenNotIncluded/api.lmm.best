import { resolve, dirname } from 'node:path'
import { mkdir, writeFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
const here = dirname(fileURLToPath(import.meta.url))
const out = resolve(process.env.REMOTE_BROWSER_ROOT || '/tmp/lmm-remote-browser')
await mkdir(out, { recursive: true })
const result = await Bun.build({ entrypoints: [resolve(here, 'browser.tsx')], outdir: out, target: 'browser', minify: false,
  plugins: [{ name: 'loopback-api-only', setup(build) { build.onResolve({ filter: /^@\/lib\/api$/ }, () => ({ path: resolve(here, 'api.ts') })) } }],
})
if (!result.success) { console.error(result.logs); process.exit(1) }
await writeFile(resolve(out, 'index.html'), `<!doctype html><html lang="zh"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Pi remote test</title><link rel="stylesheet" href="/browser.css"><body><main id="root"></main><script type="module" src="/browser.js"></script></body></html>`)
console.log(out)
