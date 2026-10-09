// One-time feature-branch preparation. Removed by the generated commit.
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { dirname } from 'node:path'
import { spawnSync } from 'node:child_process'
function patch(path, before, after) {
  const text = readFileSync(path, 'utf8')
  if (text.split(before).length !== 2) throw new Error(`Patch anchor is not unique: ${path}`)
  writeFileSync(path, text.replace(before, after))
}
function run(command, args, cwd) {
  const result = spawnSync(command, args, { cwd, stdio: 'inherit' })
  if (result.error) throw result.error
  if (result.status !== 0) throw new Error(`${command} exited ${result.status}`)
}
const web = 'apps/web'
const prefix = `${web}/src/components/ai-elements/`
patch(`${prefix}response.tsx`, "import { cn } from '@/lib/utils'", "import { cn } from '@/lib/utils'\nimport { ResponseStreamingContext } from './openui/streaming-context'")
patch(`${prefix}response.tsx`, '      {renderedContent}\n      {footnotes}', '      <ResponseStreamingContext.Provider value={props.final === false}>\n        {renderedContent}\n        {footnotes}\n      </ResponseStreamingContext.Provider>')
patch(`${prefix}response-renderer-blocks.tsx`, "import { cn } from '@/lib/utils'", "import { cn } from '@/lib/utils'\nimport { OpenUIBlock } from './openui/block'")
patch(`${prefix}response-renderer-blocks.tsx`, "  const lineCount = node.code.split('\\n').length\n\n  return (", "  const lineCount = node.code.split('\\n').length\n\n  const fallback = (")
patch(`${prefix}response-renderer-blocks.tsx`, '      <CodeBlockCopyButton />\n    </CodeBlock>\n  )\n}', "      <CodeBlockCopyButton />\n    </CodeBlock>\n  )\n  return language.trim().toLowerCase() === 'openui'\n    ? <OpenUIBlock key={key} code={node.code} fallback={fallback} />\n    : fallback\n}")
patch(`${web}/src/features/assistant/assistant-panel.tsx`, "  if (formatted) {\n    return (\n      <Response\n        className='max-w-full leading-7 break-words [&_pre]:max-w-full [&_pre]:overflow-x-auto'\n        final\n", "  if (formatted || props.entry.content.includes('```openui')) {\n    return (\n      <Response\n        className='max-w-full leading-7 break-words [&_pre]:max-w-full [&_pre]:overflow-x-auto'\n        final={!props.entry.streaming}\n")
patch('apps/api-go/controller/assistant.go', '\tprompt.WriteString(assistantSystemRules)', '\tprompt.WriteString("\\n\\n")\n\tprompt.WriteString(assistantOpenUISystemPrompt)\n\tprompt.WriteString(assistantSystemRules)')
const manifestPath = `${web}/package.json`
const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'))
manifest.scripts['openui:generate'] = 'bun scripts/generate-assistant-openui.ts'
manifest.scripts['openui:check'] = 'bun scripts/generate-assistant-openui.ts --check'
for (const name of ['build', 'build:check', 'test']) manifest.scripts[name] = `bun run openui:check && ${manifest.scripts[name]}`
writeFileSync(manifestPath, JSON.stringify(manifest, null, 2) + '\n')
run('bun', ['run', 'openui:generate'], web)
const paths = [
  'bun.lock', manifestPath,
  `${prefix}response.tsx`, `${prefix}response-renderer-blocks.tsx`,
  `${web}/src/features/assistant/assistant-panel.tsx`,
  `${prefix}openui/policy.ts`, `${prefix}openui/policy.test.ts`,
  `${prefix}openui/library.tsx`, `${prefix}openui/library.test.tsx`,
  `${prefix}openui/renderer.tsx`, `${prefix}openui/renderer.test.tsx`,
  `${prefix}openui/block.tsx`, `${prefix}openui/streaming-context.ts`,
  `${web}/scripts/generate-assistant-openui.ts`,
  'apps/api-go/controller/assistant.go',
  'apps/api-go/controller/assistant_openui.go',
  'apps/api-go/controller/assistant_openui_test.go',
  'apps/api-go/controller/assistant_openui_prompt.txt',
  'docs/assistant-openui.md',
]
const formattedPaths = paths.filter(path => path.startsWith(`${web}/`) && /\.(tsx?|json)$/.test(path))
const headers = new Map()
for (const path of formattedPaths) {
  const content = readFileSync(path, 'utf8')
  const header = content.match(/^\/\*\nCopyright \(C\)[\s\S]*?QuantumNous[\s\S]*?\*\/\n+/)?.[0]
  if (header) {
    headers.set(path, header)
    writeFileSync(path, content.slice(header.length))
  }
}
try {
  run('bun', ['x', '--no-install', 'oxfmt', '--write', ...formattedPaths.map(path => path.slice(web.length + 1))], web)
} finally {
  for (const [path, header] of headers) writeFileSync(path, header + readFileSync(path, 'utf8').replace(/^\n+/, ''))
}
run('gofmt', ['-w', ...paths.filter(path => path.endsWith('.go'))])
run('bun', ['run', 'openui:check'], web)
const output = process.env.RUNNER_TEMP + '/openui-source'
for (const path of paths) {
  mkdirSync(dirname(`${output}/${path}`), { recursive: true })
  writeFileSync(`${output}/${path}`, readFileSync(path))
}
console.log(`Prepared ${paths.length} explicitly allowed files. No runtime or production credentials used.`)
