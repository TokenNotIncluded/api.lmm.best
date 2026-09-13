/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { spawnSync } from 'node:child_process'
import {
  cpSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  rmSync,
} from 'node:fs'
import { dirname, join } from 'node:path'
import { tmpdir } from 'node:os'
import { fileURLToPath } from 'node:url'

const repo = join(dirname(fileURLToPath(import.meta.url)), '..')
const tempRoot = mkdtempSync(join(tmpdir(), 'pi-lmm-provider-test-'))
const packageRoot = join(tempRoot, 'package')
const agentDir = join(tempRoot, 'agent')
const piBin = process.env.PI_BIN || 'pi'

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: options.cwd || packageRoot,
    env: { ...process.env, PI_CODING_AGENT_DIR: agentDir, PI_OFFLINE: '1' },
    encoding: 'utf8',
    timeout: 20_000,
    input: options.input,
  })
  if (result.error) throw result.error
  if (result.status !== 0) {
    throw new Error(`${command} ${args.join(' ')} exited with ${result.status}: ${result.stderr || result.stdout}`)
  }
  return result.stdout
}

try {
  mkdirSync(packageRoot, { recursive: true })
  cpSync(join(repo, 'src'), join(packageRoot, 'src'), { recursive: true })
  cpSync(join(repo, 'package.json'), join(packageRoot, 'package.json'))
  if (existsSync(join(packageRoot, 'node_modules'))) {
    throw new Error('isolated package unexpectedly contains node_modules')
  }

  run(piBin, ['install', packageRoot])
  const output = run(
    piBin,
    ['--mode', 'rpc', '--no-approve', '--no-session', '--no-context-files', '--no-tools', '--no-skills', '--no-prompt-templates', '--no-themes'],
    { input: '{"type":"get_commands"}\n' }
  )
  const response = output
    .split('\n')
    .map((line) => {
      try {
        return JSON.parse(line)
      } catch {
        return null
      }
    })
    .find((value) => value?.type === 'response' && value.command === 'get_commands')
  if (!response?.success) throw new Error('Pi did not return a successful get_commands response')
  const names = new Set(
    response.data.commands
      .filter((command) => command.source === 'extension')
      .map((command) => command.name)
  )
  for (const required of ['lmm-prices', 'lmm-revoke']) {
    if (!names.has(required)) throw new Error(`missing registered command: ${required}`)
  }
  console.log('Pi native loader registered lmm-prices and lmm-revoke')
} finally {
  rmSync(tempRoot, { recursive: true, force: true })
}
