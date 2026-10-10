/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { test } from 'node:test'

import editorComposition from './forge-ambient.generated.json'

type FakeRenderer = ReturnType<typeof fakeRenderer>
let nextRenderer: FakeRenderer
const uniformInputs: Array<{ id: string; props: Record<string, unknown> }> = []

function fakeRenderer() {
  const events: string[] = []
  let finish!: () => void
  let reject!: () => void
  const initializing = new Promise<void>((resolve, fail) => {
    finish = resolve
    reject = fail
  })
  const callbacks = {
    ready: null as (() => void) | null,
    unavailable: null as (() => void) | null,
    deviceLost: null as (() => void) | null,
  }
  return {
    events,
    finish,
    reject,
    callbacks,
    failure: null as string | null,
    initialize(options: {
      observeElement: boolean
      colorSpace: string
      enablePerformanceTracking: boolean
    }) {
      assert.deepEqual(
        {
          observe: options.observeElement,
          color: options.colorSpace,
          tracking: options.enablePerformanceTracking,
        },
        { observe: false, color: 'srgb', tracking: false }
      )
      events.push('initialize')
      return initializing
    },
    cleanup() {
      events.push('cleanup')
    },
    setOnReady(callback: (() => void) | null) {
      callbacks.ready = callback
    },
    setOnUnavailable(callback: (() => void) | null) {
      callbacks.unavailable = callback
    },
    setOnDeviceLost(callback: (() => void) | null) {
      callbacks.deviceLost = callback
    },
    getFailureReason() {
      return this.failure
    },
    stopAnimation() {
      events.push('stop')
    },
    startAnimation() {
      events.push('start')
    },
    setResolutionScale(scale: number) {
      events.push(`resolution:${scale}`)
    },
    setFrameRateCap(fps: number) {
      events.push(`fps:${fps}`)
    },
    registerNode(id: string) {
      events.push(`register:${id}`)
    },
    updateUniformValue(id: string, key: string) {
      events.push(`update:${id}:${key}`)
    },
    updateNodeMetadata(id: string) {
      events.push(`metadata:${id}`)
    },
    resize(width: number, height: number) {
      events.push(`resize:${width}:${height}`)
    },
  }
}

mock.module('shaders/core', () => ({
  shaderRendererGPU: () => nextRenderer,
  createGpuUniformsMap: (
    _definition: unknown,
    props: Record<string, unknown>,
    id: string
  ) => {
    uniformInputs.push({ id, props })
    return {}
  },
  rootPassthrough: { fragment: () => null },
}))
mock.module('shaders/core/MeshGradient', () => ({
  default: { props: {}, fragment: () => null },
}))
mock.module('shaders/core/Grid', () => ({
  default: {
    props: {
      cells: { default: 14 },
      color: { default: '#ffffff' },
      thickness: { default: 0.01 },
      softness: { default: 0 },
    },
    fragment: () => null,
  },
}))
const { createForgeShader } = await import('./shader-runtime')
const palette = {
  ground: '#111111',
  primary: '#eeeeee',
  muted: '#333333',
  ink: '#ffffff',
}
const settle = async () => {
  await Promise.resolve()
  await Promise.resolve()
}

function mount(variant: 'home' | 'assistant' = 'home') {
  nextRenderer = fakeRenderer()
  const renderer = nextRenderer
  const notifications: string[] = []
  const canvas = {
    getContext: () => ({
      unconfigure() {
        renderer.events.push('unconfigure')
      },
    }),
  } as unknown as HTMLCanvasElement
  const handle = createForgeShader(canvas, variant, palette, {
    ready: () => notifications.push('ready'),
    unavailable: () => notifications.push('unavailable'),
  })
  return { renderer, handle, notifications }
}

test('frame and resolution limits are applied before component registration and resume', async () => {
  const { renderer, handle, notifications } = mount()
  renderer.finish()
  await settle()
  assert.deepEqual(renderer.events.slice(0, 8), [
    'initialize',
    'stop',
    'resolution:0.8',
    'fps:24',
    'register:forge-root',
    'register:forge-field',
    'register:forge-lines',
    'start',
  ])
  renderer.callbacks.ready?.()
  assert.deepEqual(notifications, ['ready'])
  handle.pause()
  handle.resize(200, 100)
  handle.resume()
  assert.deepEqual(renderer.events.slice(-3), [
    'stop',
    'resize:200:100',
    'start',
  ])
  await handle.destroy()
})

test('the actual editor source drives geometry and motion while site colors replace its stops', async () => {
  const start = uniformInputs.length
  const { renderer, handle } = mount()
  renderer.finish()
  await settle()
  const meshInput = uniformInputs
    .slice(start)
    .find(({ id }) => id === 'forge-field')?.props
  const gridInput = uniformInputs
    .slice(start)
    .find(({ id }) => id === 'forge-lines')?.props
  assert.ok(meshInput)
  assert.ok(gridInput)
  assert.equal(meshInput.count, editorComposition.meshGradient.count)
  assert.equal(meshInput.seed, editorComposition.meshGradient.seed)
  assert.equal(meshInput.speed, editorComposition.meshGradient.speed)
  assert.equal(meshInput.drift, editorComposition.meshGradient.drift)
  assert.equal(meshInput.colorA, palette.ground)
  assert.equal(meshInput.colorB, palette.primary)
  assert.equal(meshInput.stops, null)
  assert.equal(gridInput.thickness, editorComposition.grid.thickness)
  assert.equal(gridInput.softness, editorComposition.grid.softness)
  assert.equal(gridInput.color, palette.ink)
  assert.equal('opacity' in gridInput, false)
  await handle.destroy()
})

test('cleanup during pending GPU initialization releases the late root and suppresses callbacks', async () => {
  const { renderer, handle, notifications } = mount()
  const ready = renderer.callbacks.ready
  const unavailable = renderer.callbacks.unavailable
  const stopped = handle.destroy()
  assert.deepEqual(renderer.events, ['initialize', 'cleanup'])
  ready?.()
  unavailable?.()
  assert.deepEqual(notifications, [])
  renderer.finish()
  await stopped
  assert.equal(renderer.events.includes('register:forge-root'), false)
  assert.equal(renderer.events.at(-1), 'unconfigure')
  assert.ok(renderer.events.filter((entry) => entry === 'cleanup').length >= 2)
  const count = renderer.events.length
  handle.pause()
  handle.resume()
  handle.resize(500, 400)
  handle.update('store', palette)
  assert.equal(renderer.events.length, count)
})

test('pause requested before initialization is retained and intent has a smaller frame budget', async () => {
  const { renderer, handle } = mount('assistant')
  handle.pause()
  renderer.finish()
  await settle()
  assert.ok(renderer.events.includes('fps:18'))
  assert.equal(renderer.events.includes('start'), false)
  handle.resume()
  assert.equal(renderer.events.at(-1), 'start')
  await handle.destroy()
})

test('unsupported and failed initialization do not register or start an effect', async () => {
  for (const rejected of [false, true]) {
    const { renderer, handle, notifications } = mount()
    if (rejected) renderer.reject()
    else {
      renderer.failure = 'unsupported'
      renderer.finish()
    }
    await settle()
    assert.deepEqual(notifications, ['unavailable'])
    assert.equal(
      renderer.events.some((entry) => entry.startsWith('register:')),
      false
    )
    assert.equal(renderer.events.includes('start'), false)
    await handle.destroy()
  }
})

test('chapter and theme changes update the existing composition without another GPU initialization', async () => {
  const { renderer, handle } = mount()
  renderer.finish()
  await settle()
  handle.update('ecosystem', { ...palette, primary: '#123456' })
  assert.equal(
    renderer.events.filter((entry) => entry === 'initialize').length,
    1
  )
  assert.ok(renderer.events.includes('update:forge-field:colorB'))
  assert.ok(renderer.events.includes('metadata:forge-lines'))
  await handle.destroy()
})
