/* Copyright (C) 2026 LIghtJUNction; licensed under AGPL-3.0-or-later. */
import { readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { assistantOpenUIPrompt } from '../src/components/ai-elements/openui/library'
const path = fileURLToPath(
  new URL(
    '../../api-go/controller/assistant_openui_prompt.txt',
    import.meta.url
  )
)
const expected = assistantOpenUIPrompt()
if (process.argv.includes('--check')) {
  if (readFileSync(path, 'utf8') !== expected)
    throw new Error(
      'OpenUI prompt is stale. Run bun run openui:generate in apps/web and commit the generated file.'
    )
  console.log('OpenUI component and Go prompt contracts match.')
} else {
  writeFileSync(path, expected)
  console.log('Generated the Go assistant OpenUI prompt.')
}
