/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
// Loopback fixture, NOT user authentication or a WIP backend implementation.
import { existsSync } from 'node:fs'
import { dirname, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

import {
  deriveRemoteKey,
  encryptRemote,
  decryptRemote,
  remoteAad,
} from '../../../../packages/pi-lmm-provider/src/remote-wire.ts'

const here = dirname(fileURLToPath(import.meta.url))
const repo = resolve(here, '../../../..')
const out = process.env.REMOTE_SAFETY_OUTPUT
if (!out) throw new Error('REMOTE_SAFETY_OUTPUT is required')
const source = (path) =>
  [
    `${path}.ts`,
    `${path}.tsx`,
    `${path}/index.ts`,
    `${path}/index.tsx`,
    path,
  ].find(existsSync)
const build = await Bun.build({
  entrypoints: [resolve(here, 'safety-entry.tsx')],
  outdir: out,
  target: 'browser',
  define: {
    'import.meta.env': JSON.stringify({
      DEV: false,
      PROD: true,
      MODE: 'production',
    }),
  },
  plugins: [
    {
      name: 'source-alias',
      setup(builder) {
        builder.onResolve({ filter: /^@\// }, ({ path }) => ({
          path: source(resolve(repo, 'apps/web/src', path.slice(2))),
        }))
      },
    },
  ],
})
if (!build.success) throw new Error(JSON.stringify(build.logs))
const id = 'remote-layout-fixture',
  device = 'fixture-device',
  pin = 'fixture-pin'
const key = await deriveRemoteKey(pin, id)
const metadata = await encryptRemote(
  key,
  {
    version: 1,
    runtime: 'Pi',
    started_at: Date.now(),
    directory: '/workspace/example',
    summary: '设计工作区',
    capabilities: ['prompt', 'prompt_images', 'abort'],
  },
  remoteAad(id, 'metadata', device)
)
const dist = resolve(repo, 'apps/web/dist')
const css = [
  ...new Bun.Glob('index.*.css').scanSync({ cwd: resolve(dist, 'static/css') }),
][0]
if (!css) throw new Error('Build apps/web before running the safety review')
const commands = [],
  acknowledgments = []
let refused = false,
  frozenAt
let generation = 'generation-one'
const server = Bun.serve({
  hostname: '127.0.0.1',
  port: 0,
  async fetch(request) {
    const url = new URL(request.url)
    if (url.pathname === '/fixture/freeze') {
      frozenAt = Date.now()
      return new Response('ok')
    }
    if (url.pathname === '/fixture/generation') {
      generation = 'generation-two'
      return new Response('ok')
    }
    if (url.pathname === '/fixture/refuse') {
      refused = true
      return new Response('ok')
    }
    if (url.pathname === '/fixture/commands') return Response.json(commands)
    const base = '/api/remote-control/v1/pi/sessions'
    if (
      url.pathname === `${base}/${id}/messages` &&
      request.method === 'POST'
    ) {
      if (refused) {
        refused = false
        return new Response('Refused', { status: 503 })
      }
      const input = await request.json()
      const command = await decryptRemote(
        key,
        input,
        remoteAad(id, 'message', 'controller')
      )
      commands.push(command)
      acknowledgments.push({ type: 'ack', command_id: command.id, ok: true })
      return Response.json({ success: true })
    }
    if (url.pathname === `${base}/${id}/messages`) {
      const events = [
        { type: 'user', id: 'user-1', content: '请帮我改进远程控制界面' },
        {
          type: 'assistant',
          id: 'assistant-1',
          content: '已改为对话优先的布局。发送图片前会在本地处理，再加密传输。',
        },
        ...Array.from({ length: 12 }, (_, i) => ({
          type: 'tool_call',
          id: `tool-${i}`,
          tool_name: i % 2 ? 'read' : 'edit',
          content: 'Synthetic tool output',
        })),
        ...acknowledgments,
        {
          type: 'state',
          id: 'state',
          created_at: frozenAt ?? Date.now(),
          busy: false,
          provider: 'fixture',
          model: 'vision',
          requests: [],
        },
      ]
      return Response.json({
        success: true,
        data: {
          messages: await Promise.all(
            events.map(async (event, index) => ({
              sequence: index + 1,
              sender: 'plugin',
              created_at: Date.now(),
              ...(await encryptRemote(
                key,
                event,
                remoteAad(id, 'message', 'plugin')
              )),
            }))
          ),
        },
      })
    }
    if (url.pathname === base) {
      return Response.json({
        success: true,
        data: {
          sessions: [
            {
              session_id: id,
              device_id: device,
              generation,
              metadata,
              expires_at: Math.floor(Date.now() / 1000) + 300,
              updated_at: Math.floor(Date.now() / 1000),
            },
          ],
        },
      })
    }
    if (url.pathname === '/safety-entry.js') {
      return new Response(Bun.file(resolve(out, 'safety-entry.js')))
    }
    if (url.pathname === '/style.css') {
      return new Response(Bun.file(resolve(dist, 'static/css', css)))
    }
    if (url.pathname.startsWith('/static/')) {
      const path = resolve(dist, `.${url.pathname}`)
      if (path.startsWith(dist + sep) && existsSync(path)) {
        return new Response(Bun.file(path))
      }
    }
    if (url.pathname !== '/') return new Response('Not found', { status: 404 })
    return new Response(
      '<!doctype html><html class="dark"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/style.css"><body class="bg-background text-foreground"><div id="root"></div><script type="module" src="/safety-entry.js"></script></body></html>',
      { headers: { 'content-type': 'text/html' } }
    )
  },
})
await Bun.write(resolve(out, 'origin'), server.url.origin)
console.log(server.url.origin)
process.on('SIGTERM', () => {
  server.stop(true)
  process.exit(0)
})
