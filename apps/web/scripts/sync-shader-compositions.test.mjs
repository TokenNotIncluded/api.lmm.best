/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'

import {
  buildShaderArtifact,
  DERIVED_FILE,
  extractShaderComposition,
  FORGE_SHADER_ID,
  syncShaderCompositions,
} from './sync-shader-compositions.mjs'

const officialSource = await readFile(
  new URL(
    '../src/components/shaders/generated/ForgeAmbient.tsx',
    import.meta.url
  ),
  'utf8'
)
const hash = (source) =>
  `sha256:${createHash('sha256').update(source).digest('hex')}`
const sourceFile = 'src/components/shaders/generated/ForgeAmbient.tsx'
// Keep mutation fixtures independent of future legitimate editor updates.
const sampleSource = `import { Shader, Grid, MeshGradient } from 'shaders/react'
export default function ShaderEffect() {
  return (
    <Shader>
      <MeshGradient count={4} drift={0.16} seed={17} smoothness={3} speed={0.1}
        stops={[{ color: "#101816", position: 0 }, { color: "#122c27", position: 0.26 }, { color: "#176451", position: 0.52 }, { color: "#194339", position: 0.76 }, { color: "#15201d", position: 1 }]}
        swirl={0.12} variation={0.2} />
      <Grid cells={14} color="#beddd4" opacity={0.12} softness={0.4} thickness={0.014} />
    </Shader>
  )
}`

test('extracts the actual official export and preserves its literal artistic values', () => {
  const result = extractShaderComposition(officialSource)
  const sample = extractShaderComposition(sampleSource)
  assert.equal(sample.meshGradient.count, 4)
  assert.equal(sample.meshGradient.stops[2].color, '#176451')
  assert.equal(sample.meshGradient.stops[2].position, 0.52)
  assert.equal(sample.grid.cells, 14)
  assert.equal(sample.grid.opacity, 0.12)
  const artifact = buildShaderArtifact(officialSource, {
    shaderId: FORGE_SHADER_ID,
    file: sourceFile,
    officialHash: hash(officialSource),
  })
  assert.equal(artifact.source.officialHash, `sha256:${artifact.source.sha256}`)
  assert.deepEqual(artifact.meshGradient, result.meshGradient)
})

test('supports static signed numbers, null stops and explicit literal color objects', () => {
  const source = `import { Grid, MeshGradient, Shader } from 'shaders/react'
export default function Test() { return <Shader>
  <MeshGradient swirl={-0.3} stops={null} colorSpace="oklab" colorA={{r: 0.1, g: 0.2, b: 0.3}} />
  <Grid rotation={45} cellColor="transparent" />
</Shader> }`
  const result = extractShaderComposition(source)
  assert.equal(result.meshGradient.swirl, -0.3)
  assert.equal(result.meshGradient.stops, null)
  assert.deepEqual(result.meshGradient.colorA, { r: 0.1, g: 0.2, b: 0.3 })
  assert.equal(result.grid.rotation, 45)
})

test('rejects evaluated code, hooks, spreads, unsafe props and unsupported structure', () => {
  const variants = [
    sampleSource.replace('count={4}', 'count={getCount()}'),
    sampleSource.replace('count={4}', 'count={4 + 1}'),
    sampleSource.replace('count={4}', '{...props}'),
    sampleSource.replace('count={4}', 'onClick={() => alert(1)}'),
    sampleSource.replace('<Shader>', '<Shader style={{width: 10}}>'),
    sampleSource.replace('return (', 'const value = useState(1); return ('),
    sampleSource.replace('count={4}', 'count={4} count={5}'),
    sampleSource.replace('<Grid', '<Unknown'),
    sampleSource.replace('count={4}', 'count={Infinity}'),
    sampleSource.replace(
      'color="#beddd4"',
      'color="url(https://example.test/image)"'
    ),
  ]
  for (const source of variants) {
    assert.throws(() => extractShaderComposition(source))
  }
})

test('rejects invalid artistic ranges and malformed color stops', () => {
  for (const source of [
    sampleSource.replace('count={4}', 'count={4.5}'),
    sampleSource.replace('count={4}', 'count={100}'),
    sampleSource.replace('opacity={0.12}', 'opacity={1.2}'),
    sampleSource.replace('position: 0.26', 'position: -0.2'),
    sampleSource.replace('position: 0.26', 'position: 0.8'),
    sampleSource.replace('position: 0.26', 'position: 0.26, extra: 1'),
    sampleSource.replace('smoothness={3}', 'smoothness={"3"}'),
  ]) {
    assert.throws(() => extractShaderComposition(source))
  }
})

test('check detects stale derived data after a legitimate CLI update and never mutates it', async (t) => {
  const webRoot = await mkdtemp(join(tmpdir(), 'forge-shader-sync-'))
  t.after(() => rm(webRoot, { recursive: true, force: true }))
  await mkdir(join(webRoot, 'src/components/shaders/generated'), {
    recursive: true,
  })
  const writeInstalled = async (source) => {
    await writeFile(join(webRoot, sourceFile), source)
    await writeFile(
      join(webRoot, 'shaders.lock.json'),
      JSON.stringify({
        version: 1,
        shaders: {
          [FORGE_SHADER_ID]: {
            file: sourceFile,
            framework: 'react',
            hash: hash(source),
          },
        },
      })
    )
  }
  await writeInstalled(sampleSource)
  await assert.rejects(
    syncShaderCompositions({ webRoot, check: true }),
    /stale/
  )
  await syncShaderCompositions({ webRoot })
  const before = await readFile(join(webRoot, DERIVED_FILE), 'utf8')
  assert.equal(
    (await syncShaderCompositions({ webRoot, check: true })).changed,
    false
  )
  await writeInstalled(sampleSource.replace('drift={0.16}', 'drift={0.24}'))
  await assert.rejects(
    syncShaderCompositions({ webRoot, check: true }),
    /stale/
  )
  assert.equal(await readFile(join(webRoot, DERIVED_FILE), 'utf8'), before)
  await syncShaderCompositions({ webRoot })
  assert.equal(
    JSON.parse(await readFile(join(webRoot, DERIVED_FILE), 'utf8')).meshGradient
      .drift,
    0.24
  )
  assert.equal(
    (await syncShaderCompositions({ webRoot, check: true })).changed,
    false
  )
})

test('untracked local edits and unsafe lock paths fail before producing an artifact', async (t) => {
  assert.throws(
    () =>
      buildShaderArtifact(officialSource, {
        shaderId: FORGE_SHADER_ID,
        file: sourceFile,
        officialHash: hash(`${officialSource}\n`),
      }),
    /official CLI lock hash/
  )
  const webRoot = await mkdtemp(join(tmpdir(), 'forge-shader-sync-path-'))
  t.after(() => rm(webRoot, { recursive: true, force: true }))
  for (const file of [
    '/tmp/source.tsx',
    'src/components/shaders/generated/../../secret.tsx',
  ]) {
    await writeFile(
      join(webRoot, 'shaders.lock.json'),
      JSON.stringify({
        version: 1,
        shaders: { [FORGE_SHADER_ID]: { file, framework: 'react' } },
      })
    )
    await assert.rejects(
      syncShaderCompositions({ webRoot }),
      /installed by the official React CLI/
    )
  }
})
