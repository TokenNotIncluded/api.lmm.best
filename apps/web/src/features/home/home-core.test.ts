/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  createCamera,
  createNetwork,
  createTrainingScene,
  CYCLE,
  INPUT,
  LOSS,
  PALETTE,
  PREDICTION,
  projectPoint,
  SCENE_TEXT,
  trainingClock,
  type CorePoint,
  type SceneSink,
} from './home-core'
import { cinemaPosition } from './home-motion'

const glyph = (text: string, cols: number, rows: number) => {
  const bitmap = new Uint8Array(cols * rows)
  for (let i = 0; i < bitmap.length; i++) {
    bitmap[i] = (i + text.length) % 3 === 0 ? 1 : 0
  }
  return bitmap
}

function record(time: number) {
  const net = createNetwork()
  const scene = createTrainingScene(net, glyph)
  const points: CorePoint[] = []
  const colors: (readonly number[])[] = []
  const text: string[] = []
  const sink: SceneSink = {
    token: (p, value) => (points.push(p), text.push(value)),
    label: (p, value) => (points.push(p), text.push(value)),
    segment: (a, b, color, alpha) => {
      points.push(a, b)
      if (alpha > 0.3) colors.push(color)
    },
    sprite: (p, color, alpha) => {
      points.push(p)
      if (alpha > 0.3) colors.push(color)
    },
  }
  scene(sink, time)
  return { net, points, colors, text }
}

test('the network has an input bitmap, three hidden grids and one output per token', () => {
  const net = createNetwork()
  assert.equal(net.layers.length, 5)
  assert.equal(net.layers[4].length, 10)
  for (const layer of net.layers.slice(1, 4)) assert.equal(layer.length, 36)
  for (let g = 1; g <= 3; g++) {
    assert.ok(net.gaps[g].length > 60)
    for (const synapse of net.gaps[g]) {
      assert.ok(net.layers[g].includes(synapse.from))
      assert.ok(net.layers[g + 1].includes(synapse.to))
    }
  }
})

test('each training cycle runs forward before it propagates gradients backward', () => {
  const offset = trainingClock(0).t * CYCLE
  const at = (t: number) => trainingClock(t * CYCLE - offset)
  assert.equal(at(0.1).forward, 0)
  assert.equal(at(0.35).backward, Number.POSITIVE_INFINITY)
  assert.equal(at(0.55).forward, 4)
  assert.equal(at(0.55).backward, Number.POSITIVE_INFINITY)
  assert.ok(at(0.75).backward < 4 && at(0.75).backward > 0)
  assert.ok(at(0.95).backward < 0)
  // A motionless frame shows gradients flowing through the hidden layers.
  assert.ok(trainingClock(0).backward > 1 && trainingClock(0).backward < 3.5)
})

test('every emitted frame stays finite, uses known text and shows both passes', () => {
  let forwardSeen = false
  let backwardSeen = false
  for (let frame = 0; frame < 48; frame++) {
    const { points, colors, text } = record((frame / 48) * CYCLE * 2)
    for (const p of points) {
      assert.ok([p.x, p.y, p.z].every(Number.isFinite))
      assert.ok(Math.abs(p.x) < 10 && Math.abs(p.y) < 10 && Math.abs(p.z) < 10)
    }
    for (const value of text) assert.ok(SCENE_TEXT.includes(value), value)
    forwardSeen ||= colors.includes(PALETTE.cyan)
    backwardSeen ||= colors.includes(PALETTE.pink)
  }
  assert.ok(forwardSeen && backwardSeen)
})

test('the camera keeps the network on screen through the scroll orbit', () => {
  const net = createNetwork()
  for (const [w, h] of [
    [1440, 900],
    [390, 844],
  ]) {
    for (const progress of [0, 0.3, 0.5, 0.8, 1]) {
      const camera = createCamera(0, { x: 0, y: 0 }, progress, w, h)
      const input = {
        w: (INPUT.cols * INPUT.cell) / 2 + 0.06,
        h: (INPUT.rows * INPUT.cell) / 2 + 0.06,
      }
      const landmarks: CorePoint[] = [
        { x: INPUT.x - input.w, y: -input.h, z: 0 },
        { x: INPUT.x - input.w, y: input.h, z: 0 },
        { x: PREDICTION.x + PREDICTION.w / 2, y: PREDICTION.y, z: 0 },
        LOSS,
      ]
      for (const p of [
        ...net.neurons.map((neuron) => neuron.position),
        ...landmarks,
      ]) {
        const q = projectPoint(camera, p)
        assert.ok(q.x > 0 && q.x < w && q.y > 0 && q.y < h)
      }
    }
  }
})

test('scroll progress spans the pinned runway, not the whole section height', () => {
  assert.equal(cinemaPosition(80, 3000, 800, 80), 0)
  assert.equal(cinemaPosition(-1020, 3000, 800, 80), 0.5)
  assert.equal(cinemaPosition(-2120, 3000, 800, 80), 1)
  assert.equal(cinemaPosition(900, 3000, 800, 80), 0)
  assert.equal(cinemaPosition(-9000, 3000, 800, 80), 1)
})
