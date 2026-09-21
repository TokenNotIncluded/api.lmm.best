/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const source = path.join(root, 'packaging/common/lmm-api/edge-policy/service-unavailable.html')
const target = path.join(root, 'packaging/common/lmm-api/edge-policy/nginx/lmm-api-locations.conf')
const begin = '    # BEGIN GENERATED LMM BEST ERROR DOCUMENT'
const end = '    # END GENERATED LMM BEST ERROR DOCUMENT'
const html = fs.readFileSync(source, 'utf8').trimEnd()
if (html.replaceAll('$request_id', '').includes('$')) throw new Error('only the nginx-generated request ID may be interpolated')
const parts = []
let current = '', bytes = 0
for (const character of html) {
  const size = Buffer.byteLength(character)
  if (bytes + size > 2800) { parts.push(current); current = ''; bytes = 0 }
  current += character
  bytes += size
}
if (current) parts.push(current)
const quote = (value) => value.replaceAll('\\', '\\\\').replaceAll("'", "\\'")
const generated = [begin,
  '    # Generated from ../service-unavailable.html; keep parameters below nginx limits.',
  ...parts.map((value, index) => `    set $lmm_error_html_${index} '${quote(value)}';`),
  `    return 503 "${parts.map((_, index) => '$lmm_error_html_' + index).join('')}";`, end].join('\n')
const original = fs.readFileSync(target, 'utf8')
const start = original.indexOf(begin), finish = original.indexOf(end)
if (start < 0 || finish < start) throw new Error('generated document markers are missing')
const expected = original.slice(0, start) + generated + original.slice(finish + end.length)
if (process.argv.includes('--check')) {
  if (original !== expected) { console.error('nginx error document is out of sync'); process.exitCode = 1 }
} else fs.writeFileSync(target, expected)
