/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
export type CorePoint = { x: number; y: number; z: number }
export type Rgb = readonly [number, number, number]

/** Pixel-console palette: idle navy wiring, blue forward pass, crimson backward pass. */
export const PALETTE = {
  /** Matches the stage background; hollow neurons are filled with it to hide wires. */
  ground: [5, 8, 18],
  wire: [34, 46, 88],
  idle: [48, 64, 118],
  lattice: [38, 52, 96],
  muted: [112, 128, 170],
  blue: [52, 118, 255],
  cyan: [94, 230, 255],
  pink: [255, 64, 128],
  crimson: [196, 30, 76],
  ink: [232, 238, 250],
} satisfies Record<string, Rgb>

/** Output vocabulary. The network "reads" a rasterized token and predicts which one it is. */
export const VOCABULARY = [
  'api',
  'λ',
  '{}',
  'lmm',
  'gpt',
  '</>',
  '∑',
  'tok',
  'ai',
  '42',
] as const

const CLOUD_WORDS = [
  'token',
  'embed',
  'attention',
  'softmax',
  'logits',
  '∇loss',
  'ReLU',
  'W·x+b',
  'context',
  'next',
  '0.82',
  '-0.31',
  '{ }',
  '[ ]',
  '::',
  '01',
  '∂L/∂w',
  'grad',
  'batch',
  'epoch',
  'lr=3e-4',
  'GELU',
  'KV',
  'Q·K',
  'prompt',
  '↗',
] as const

/** Every string a renderer may be asked to draw, so it can prepare a glyph atlas once. */
export const SCENE_TEXT: readonly string[] = [
  ...new Set<string>([...VOCABULARY, ...CLOUD_WORDS]),
]

export const Shape = {
  neuron: 0,
  pixel: 1,
  frame: 2,
  diamond: 3,
  glow: 4,
} as const
export type ShapeId = (typeof Shape)[keyof typeof Shape]

/** Renderers receive primitives in world space; `glow` primitives blend additively. */
export interface SceneSink {
  token(p: CorePoint, text: string, color: Rgb, alpha: number, size: number): void
  segment(
    a: CorePoint,
    b: CorePoint,
    color: Rgb,
    alpha: number,
    width: number,
    glow?: boolean,
    dash?: number
  ): void
  sprite(
    p: CorePoint,
    color: Rgb,
    alpha: number,
    size: number,
    shape: ShapeId,
    fill?: number,
    glow?: boolean
  ): void
  label(
    p: CorePoint,
    text: string,
    color: Rgb,
    alpha: number,
    size: number,
    align?: number
  ): void
}

export type Neuron = { id: number; layer: number; position: CorePoint }
export type Synapse = { from: number; to: number; weight: number }
type CloudToken = {
  text: string
  radius: number
  angle: number
  y: number
  color: Rgb
  alpha: number
  size: number
  phase: number
}
export type Network = {
  neurons: Neuron[]
  /** layers[0] is the input bitmap; 1–3 are hidden grids and 4 is the output column. */
  layers: number[][]
  /** gaps[g] joins layer g to g + 1 (g = 1…3). */
  gaps: Synapse[][]
  pixels: CorePoint[]
  lattice: { position: CorePoint; gap: number; seed: number }[]
  cloud: CloudToken[]
}

export const INPUT = { x: -3.4, cols: 28, rows: 16, cell: 0.062 } as const
const HIDDEN_X = [-1.62, -0.26, 1.1]
const OUTPUT_X = 2.42
const HIDDEN = { rows: 9, cols: 4, dy: 0.34, dz: 0.44 }
const OUTPUT_DY = 0.29
const BAR_X = OUTPUT_X + 0.52
const BAR_LENGTH = 0.44
export const PREDICTION = { x: 3.92, y: 0.36, w: 0.72, h: 0.6 } as const
export const LOSS = { x: 3.92, y: -0.98, z: 0 } as const
export const PIVOT: CorePoint = { x: 0.26, y: 0, z: 0 }

const point = (x: number, y: number, z: number): CorePoint => ({ x, y, z })
const lerp = (a: CorePoint, b: CorePoint, t: number): CorePoint => ({
  x: a.x + (b.x - a.x) * t,
  y: a.y + (b.y - a.y) * t,
  z: a.z + (b.z - a.z) * t,
})
const mix = (a: Rgb, b: Rgb, t: number): Rgb => [
  a[0] + (b[0] - a[0]) * t,
  a[1] + (b[1] - a[1]) * t,
  a[2] + (b[2] - a[2]) * t,
]
const clamp = (value: number) => Math.min(1, Math.max(0, value))
/** Smoothstep that also runs downhill when a > b. */
export const ramp = (a: number, b: number, value: number) => {
  const t = clamp((value - a) / (b - a))
  return t * t * (3 - 2 * t)
}
export function hash(n: number) {
  let x = Math.imul(n ^ 0x9e3779b9, 0x85ebca6b)
  x ^= x >>> 13
  x = Math.imul(x, 0xc2b2ae35)
  x ^= x >>> 16
  return (x >>> 0) / 4294967296
}

export function createNetwork(): Network {
  const neurons: Neuron[] = []
  const layers: number[][] = [[]]
  for (const x of HIDDEN_X) {
    const layer: number[] = []
    // Back columns first, so the default camera paints near neurons last.
    for (let col = 0; col < HIDDEN.cols; col++) {
      for (let row = 0; row < HIDDEN.rows; row++) {
        layer.push(neurons.length)
        neurons.push({
          id: neurons.length,
          layer: layers.length,
          position: point(
            x,
            ((HIDDEN.rows - 1) / 2 - row) * HIDDEN.dy,
            (col - (HIDDEN.cols - 1) / 2) * HIDDEN.dz
          ),
        })
      }
    }
    layers.push(layer)
  }
  const output: number[] = []
  VOCABULARY.forEach((_, row) => {
    output.push(neurons.length)
    neurons.push({
      id: neurons.length,
      layer: 4,
      position: point(OUTPUT_X, ((VOCABULARY.length - 1) / 2 - row) * OUTPUT_DY, 0),
    })
  })
  layers.push(output)

  const gaps: Synapse[][] = [[]]
  for (let g = 1; g <= 3; g++) {
    const source = layers[g],
      target = layers[g + 1]
    const synapses: Synapse[] = []
    const fan = g === 3 ? 3 : 4
    for (const from of source) {
      for (let k = 0; k < fan; k++) {
        const to = target[Math.floor(hash(from * 97 + k * 131 + g) * target.length)]
        if (synapses.some((s) => s.from === from && s.to === to)) continue
        synapses.push({ from, to, weight: hash(from * 57 + to * 11) * 2 - 1 })
      }
    }
    gaps.push(synapses)
  }

  const pixels: CorePoint[] = []
  for (let row = 0; row < INPUT.rows; row++) {
    for (let col = 0; col < INPUT.cols; col++) {
      pixels.push(
        point(
          INPUT.x + (col - (INPUT.cols - 1) / 2) * INPUT.cell,
          ((INPUT.rows - 1) / 2 - row) * INPUT.cell,
          0
        )
      )
    }
  }

  // A dotted weight lattice between layers, like a matrix seen edge-on.
  const lattice: Network['lattice'] = []
  const columns = [...HIDDEN_X, OUTPUT_X]
  for (let g = 1; g <= 3; g++) {
    const x = (columns[g - 1] + columns[g]) / 2
    for (let row = 0; row < 15; row++) {
      for (let col = 0; col < 6; col++) {
        lattice.push({
          position: point(x, (7 - row) * 0.19, (col - 2.5) * 0.26),
          gap: g,
          seed: lattice.length,
        })
      }
    }
  }

  const tints: Rgb[] = [
    PALETTE.muted,
    PALETTE.muted,
    PALETTE.muted,
    PALETTE.blue,
    PALETTE.cyan,
    PALETTE.pink,
  ]
  const cloud: CloudToken[] = []
  const words = SCENE_TEXT
  for (let i = 0; i < 64; i++) {
    const h = hash(i * 7919)
    cloud.push({
      text: words[i % words.length],
      radius: 4.2 + hash(i * 31) * 3.2,
      angle: i * 2.39996,
      y: (hash(i * 53) - 0.5) * 6.4,
      color: tints[Math.floor(h * tints.length)],
      alpha: 0.26 + hash(i * 17) * 0.34,
      size: 0.13 + hash(i * 13) * 0.1,
      phase: hash(i * 71) * Math.PI * 2,
    })
  }
  return { neurons, layers, gaps, pixels, lattice, cloud }
}

export const CYCLE = 6.4
/** Offset so a motionless frame (reduced motion, static fallback) shows backpropagation. */
const STILL = 0.77 * CYCLE

export function trainingClock(time: number) {
  const cycle = (time + STILL) / CYCLE
  const sample = Math.floor(cycle)
  const t = cycle - sample
  return {
    sample,
    t,
    intake: ramp(0, 0.1, t),
    reveal: ramp(0.05, 0.17, t),
    /** Forward wave front: 0 = input bitmap … 4 = output column. */
    forward: ramp(0.18, 0.52, t) * 4,
    predict: ramp(0.51, 0.58, t),
    /** Backward wave front: 5 = loss … 0 = input. Infinity before it starts. */
    backward: t < 0.6 ? Number.POSITIVE_INFINITY : 5 - ramp(0.6, 0.9, t) * 5.4,
    fade: ramp(0.92, 1, t),
  }
}

type Sample = {
  target: number
  predicted: number
  act: Float32Array
  grad: Float32Array
  bitmap: Uint8Array
  saliency: Float32Array
  taps: [pixel: number, neuron: number][]
  forward: Synapse[][]
  backward: Synapse[][]
}

type Raster = (text: string, cols: number, rows: number) => Uint8Array

/** Threshold a token into the input bitmap. Returns an empty bitmap without a 2D canvas. */
export function rasterizeToken(text: string, cols: number, rows: number) {
  const bitmap = new Uint8Array(cols * rows)
  try {
    const canvas = document.createElement('canvas')
    canvas.width = cols
    canvas.height = rows
    const ctx = canvas.getContext('2d')
    if (!ctx || typeof ctx.fillText !== 'function') return bitmap
    let size = 15
    const font = () =>
      `600 ${size}px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace`
    ctx.font = font()
    while (size > 7 && ctx.measureText(text).width > cols - 2) {
      size -= 1
      ctx.font = font()
    }
    ctx.fillStyle = '#fff'
    ctx.textAlign = 'center'
    ctx.textBaseline = 'middle'
    ctx.fillText(text, cols / 2, rows / 2 + 1)
    const data = ctx.getImageData(0, 0, cols, rows).data
    for (let i = 0; i < bitmap.length; i++) bitmap[i] = data[i * 4 + 3] > 128 ? 1 : 0
  } catch {
    // Decorative input only.
  }
  return bitmap
}

function createSample(net: Network, sample: number, raster: Raster): Sample {
  const target = (sample * 7 + 3) % VOCABULARY.length
  const wrong = sample % 4 === 2
  const predicted = wrong ? (target + 3) % VOCABULARY.length : target
  const act = new Float32Array(net.neurons.length)
  const grad = new Float32Array(net.neurons.length)
  for (const neuron of net.neurons) {
    const id = neuron.id
    if (neuron.layer === 4) {
      const row = id - net.layers[4][0]
      act[id] =
        row === predicted
          ? 0.94
          : row === target
            ? 0.56
            : hash(sample * 311 + row) * 0.3
      grad[id] =
        row === predicted && wrong
          ? 1
          : row === target
            ? wrong
              ? -1
              : -0.35
            : hash(sample * 17 + row) > 0.72
              ? 0.3
              : 0
      continue
    }
    const v = hash(sample * 977 + id * 13)
    act[id] = v > 0.46 ? 0.35 + ((v - 0.46) / 0.54) * 0.65 : 0
    const g = hash(sample * 1553 + id * 29)
    grad[id] =
      g > 0.52
        ? ((g - 0.52) / 0.48) *
          (hash(id * 3 + sample) > 0.5 ? 1 : -1) *
          (wrong ? 1 : 0.6)
        : 0
  }
  const strongest = (synapses: Synapse[], score: (s: Synapse) => number) =>
    synapses
      .map((s) => [s, score(s)] as const)
      .filter(([, value]) => value > 0.02)
      .sort((a, b) => b[1] - a[1])
      .slice(0, 22)
      .map(([s]) => s)
  const forward = net.gaps.map((gap) =>
    strongest(gap, (s) => act[s.from] * (0.4 + Math.abs(s.weight)))
  )
  const backward = net.gaps.map((gap) =>
    strongest(gap, (s) => Math.abs(grad[s.to] * s.weight) * (act[s.from] + 0.3))
  )
  const bitmap = raster(VOCABULARY[target], INPUT.cols, INPUT.rows)
  const lit = [...bitmap.keys()].filter((i) => bitmap[i])
  const active = net.layers[1].filter((id) => act[id] > 0)
  const taps: Sample['taps'] = []
  for (let i = 0; i < Math.min(12, lit.length) && active.length; i++) {
    taps.push([
      lit[Math.floor(hash(sample * 41 + i) * lit.length)],
      active[Math.floor(hash(sample * 43 + i) * active.length)],
    ])
  }
  // Pixels near the stroke receive gradient "noise", the way saliency looks.
  const saliency = new Float32Array(bitmap.length)
  for (let i = 0; i < bitmap.length; i++) {
    const col = i % INPUT.cols
    const near =
      bitmap[i] ||
      bitmap[i - 1] ||
      bitmap[i + 1] ||
      bitmap[i - INPUT.cols] ||
      bitmap[i + INPUT.cols] ||
      (col > 1 && bitmap[i - 2])
    const h = hash(sample * 7 + i * 3)
    if (near && h > 0.45) saliency[i] = (h - 0.45) / 0.55
  }
  return { target, predicted, act, grad, bitmap, saliency, taps, forward, backward }
}

/** Builds each training frame: token intake, forward pass, prediction and backpropagation. */
export function createTrainingScene(
  net: Network = createNetwork(),
  raster: Raster = rasterizeToken
) {
  let cached: Sample | null = null
  let cachedIndex = -1
  const sampleFor = (index: number) => {
    if (!cached || cachedIndex !== index) {
      cached = createSample(net, index, raster)
      cachedIndex = index
    }
    return cached
  }
  const neuron = (id: number) => net.neurons[id].position
  const barEnd = (row: number, value: number) =>
    point(BAR_X + 0.05 + value * BAR_LENGTH, neuron(net.layers[4][row]).y, 0)

  return (sink: SceneSink, time: number) => {
    const clock = trainingClock(time)
    const s = sampleFor(clock.sample)
    const live = 1 - clock.fade
    const forwardFade = 1 - 0.75 * ramp(0.58, 0.68, clock.t)

    // Token cloud: a slow orbit of vocabulary around the network.
    for (const [index, token] of net.cloud.entries()) {
      const angle = token.angle + time * 0.035
      sink.token(
        point(
          PIVOT.x + Math.cos(angle) * token.radius * 1.15,
          token.y + Math.sin(time * 0.3 + token.phase) * 0.12,
          -0.8 + Math.sin(angle) * token.radius * 0.8
        ),
        token.text,
        token.color,
        token.alpha * (index % 9 === clock.sample % 9 ? 1.4 : 1),
        token.size
      )
    }
    // The sample's token leaves the cloud and lands on the input bitmap.
    if (clock.reveal < 1) {
      const start = point(
        -5.6 + hash(clock.sample) * 2,
        2.4 + hash(clock.sample * 3) * 1.2,
        -2.6
      )
      const landing = point(INPUT.x, 0, 0.05)
      const e = clock.intake
      sink.token(
        lerp(start, landing, e * e * (3 - 2 * e)),
        VOCABULARY[s.target],
        PALETTE.cyan,
        (0.35 + e * 0.65) * (1 - clock.reveal),
        0.2 + e * 0.46
      )
    }

    // Input bitmap, its frame and the scan line that "draws" it.
    const half = {
      w: (INPUT.cols * INPUT.cell) / 2 + 0.06,
      h: (INPUT.rows * INPUT.cell) / 2 + 0.06,
    }
    const corners = [
      point(INPUT.x - half.w, -half.h, 0),
      point(INPUT.x + half.w, -half.h, 0),
      point(INPUT.x + half.w, half.h, 0),
      point(INPUT.x - half.w, half.h, 0),
    ]
    corners.forEach((corner, i) =>
      sink.segment(corner, corners[(i + 1) % 4], PALETTE.idle, 0.9, 1.5)
    )
    const backIntoInput = ramp(0.9, -0.1, clock.backward)
    s.bitmap.forEach((on, i) => {
      const p = net.pixels[i]
      const col = i % INPUT.cols
      const shown = on && col / INPUT.cols < clock.reveal * 1.05
      const heat = s.saliency[i] * backIntoInput
      if (shown) {
        sink.sprite(p, mix(PALETTE.ink, PALETTE.pink, heat * 0.5), live, INPUT.cell, Shape.pixel)
      } else if (heat > 0.05) {
        sink.sprite(p, mix(PALETTE.crimson, PALETTE.pink, heat), heat * live, INPUT.cell, Shape.pixel)
      } else {
        sink.sprite(p, PALETTE.lattice, 0.4, INPUT.cell * 0.36, Shape.pixel)
      }
    })
    if (clock.reveal > 0 && clock.reveal < 1) {
      const x = INPUT.x - half.w + clock.reveal * half.w * 2
      sink.segment(point(x, -half.h, 0.01), point(x, half.h, 0.01), PALETTE.cyan, 0.9, 2, true)
    }

    // Weight lattice: twinkles pink while its gap is being updated.
    for (const dot of net.lattice) {
      const updating =
        clock.backward <= dot.gap + 1 && clock.backward >= dot.gap - 0.4
      const flicker = updating && hash(dot.seed + Math.floor(time * 14) * 977) > 0.78
      sink.sprite(
        dot.position,
        flicker ? PALETTE.pink : PALETTE.lattice,
        flicker ? 0.9 * live : 0.42,
        0.034,
        Shape.pixel
      )
    }

    // Idle wiring.
    for (let g = 1; g <= 3; g++) {
      for (const synapse of net.gaps[g]) {
        sink.segment(neuron(synapse.from), neuron(synapse.to), PALETTE.wire, 0.55, 1)
      }
    }

    // Forward pass: edges grow from each active source behind the wave front.
    const forwardEdge = (a: CorePoint, b: CorePoint, gap: number, weight: number, strength: number) => {
      const u = clamp(clock.forward - gap)
      if (u <= 0) return
      const tail = clock.forward >= gap + 1 ? Math.exp(-(clock.forward - gap - 1) * 2.2) : 1
      const alpha = tail * (0.45 + 0.55 * strength) * live * forwardFade
      if (alpha < 0.02) return
      const head = lerp(a, b, u)
      const color = mix(PALETTE.blue, PALETTE.cyan, Math.abs(weight))
      sink.segment(a, head, color, alpha, Math.abs(weight) > 0.6 ? 2.6 : 1.8, true)
      if (u < 1) sink.sprite(head, PALETTE.cyan, 0.95 * live, 0.2, Shape.glow, 1, true)
    }
    const backwardEdge = (a: CorePoint, b: CorePoint, gap: number, sign: number, strength: number) => {
      // Travels from b (the later layer) back towards a.
      const u = clamp(gap + 1 - clock.backward)
      if (u <= 0) return
      const tail = clock.backward <= gap ? Math.exp(-(gap - clock.backward) * 2) : 1
      const alpha = tail * (0.5 + 0.5 * strength) * live
      if (alpha < 0.02) return
      const head = lerp(b, a, u)
      sink.segment(b, head, sign > 0 ? PALETTE.pink : PALETTE.cyan, alpha, strength > 0.6 ? 2.6 : 1.8, true)
      if (u < 1) sink.sprite(head, PALETTE.pink, 0.95 * live, 0.2, Shape.glow, 1, true)
    }
    for (const [pixel, target] of s.taps) {
      forwardEdge(net.pixels[pixel], neuron(target), 0, 0.5, 0.6)
      backwardEdge(net.pixels[pixel], neuron(target), 0, 1, 0.5)
    }
    for (let g = 1; g <= 3; g++) {
      for (const synapse of s.forward[g]) {
        forwardEdge(neuron(synapse.from), neuron(synapse.to), g, synapse.weight, s.act[synapse.from])
      }
      for (const synapse of s.backward[g]) {
        const value = s.grad[synapse.to] * synapse.weight
        backwardEdge(neuron(synapse.from), neuron(synapse.to), g, value, Math.abs(value))
      }
    }
    // Sampling squares on the tapped input pixels.
    const tapping = ramp(-0.2, 0.1, clock.forward) * ramp(1.6, 1, clock.forward)
    const blaming = ramp(1.2, 0.6, clock.backward) * live
    for (const [pixel] of s.taps) {
      if (tapping > 0.01) {
        sink.sprite(net.pixels[pixel], PALETTE.cyan, tapping * live, INPUT.cell * 2.2, Shape.frame)
      }
      if (blaming > 0.01) {
        sink.sprite(net.pixels[pixel], PALETTE.pink, blaming, INPUT.cell * 2.2, Shape.frame)
      }
    }

    // Neurons: hollow when idle, filled by activations, then by gradients.
    for (const n of net.neurons) {
      const d = n.layer
      const reach = ramp(d - 0.25, d + 0.05, clock.forward)
      const flash = Math.max(0, 1 - Math.abs(clock.forward - d) * 3.5) * (s.act[n.id] > 0 ? 1 : 0)
      const f = Math.min(1, s.act[n.id] * reach * forwardFade + flash * 0.4) * live
      const b = Math.abs(s.grad[n.id]) * ramp(d + 0.3, d - 0.05, clock.backward) * live
      let color = mix(PALETTE.idle, mix(PALETTE.blue, PALETTE.cyan, flash), Math.min(1, f * 1.6))
      color = mix(color, mix(PALETTE.crimson, PALETTE.pink, b), Math.min(1, b * 1.8))
      const size = d === 4 ? 0.24 : 0.28
      sink.sprite(n.position, color, 0.95, size, Shape.neuron, Math.max(f * (1 - b), b * 0.85))
      if (f > 0.55 && b < 0.2) {
        sink.sprite(n.position, PALETTE.blue, f * 0.35, size * 2.2, Shape.glow, 1, true)
      }
    }

    // Output column: labels, logit bars, dashed routes to the loss.
    VOCABULARY.forEach((text, row) => {
      const id = net.layers[4][row]
      const p = neuron(id)
      const chosen = row === s.predicted
      const value = s.act[id] * clock.predict * live
      sink.label(
        point(OUTPUT_X + 0.16, p.y, 0),
        text,
        chosen && clock.predict > 0.5 ? PALETTE.ink : PALETTE.muted,
        chosen ? 0.6 + 0.4 * clock.predict : 0.75,
        0.15
      )
      sink.segment(
        point(BAR_X, p.y, 0),
        barEnd(row, value),
        chosen ? PALETTE.cyan : PALETTE.blue,
        0.45 + value * 0.55,
        3,
        chosen
      )
      sink.segment(barEnd(row, 0), LOSS, PALETTE.wire, 0.7, 1, false, 3)
      const g = s.grad[id]
      if (g) {
        const u = clamp(5 - clock.backward)
        if (u > 0) {
          const head = lerp(LOSS, p, u)
          const tail = clock.backward <= 4 ? Math.exp(-(4 - clock.backward) * 2) : 1
          sink.segment(LOSS, head, g > 0 ? PALETTE.pink : PALETTE.cyan, tail * Math.abs(g) * live, 2, true)
          if (u < 1) sink.sprite(head, PALETTE.pink, 0.95 * live, 0.2, Shape.glow, 1, true)
        }
      }
    })

    // Prediction box and loss diamond.
    const correct = s.predicted === s.target
    const verdict = correct ? PALETTE.cyan : PALETTE.pink
    const box = [
      point(PREDICTION.x - PREDICTION.w / 2, PREDICTION.y - PREDICTION.h / 2, 0),
      point(PREDICTION.x + PREDICTION.w / 2, PREDICTION.y - PREDICTION.h / 2, 0),
      point(PREDICTION.x + PREDICTION.w / 2, PREDICTION.y + PREDICTION.h / 2, 0),
      point(PREDICTION.x - PREDICTION.w / 2, PREDICTION.y + PREDICTION.h / 2, 0),
    ]
    const shown = clock.predict * live
    box.forEach((corner, i) => {
      sink.segment(corner, box[(i + 1) % 4], PALETTE.idle, 0.9, 1.5)
      if (shown > 0.01) sink.segment(corner, box[(i + 1) % 4], verdict, shown * 0.8, 1.5, true)
    })
    if (shown > 0.01) {
      sink.segment(
        barEnd(s.predicted, s.act[net.layers[4][s.predicted]]),
        point(PREDICTION.x - PREDICTION.w / 2, PREDICTION.y, 0),
        verdict,
        shown * 0.6,
        1.5,
        true
      )
      sink.label(point(PREDICTION.x, PREDICTION.y, 0.02), VOCABULARY[s.predicted], verdict, shown, 0.34, 0.5)
    }
    const loss = clock.backward <= 5.2 ? Math.exp(-Math.abs(clock.backward - 4.6) * 1.4) : 0
    const severity = correct ? 0.45 : 1
    sink.sprite(LOSS, mix(PALETTE.crimson, PALETTE.pink, loss), 0.95, 0.3, Shape.diamond, 0.35 + loss * severity * 0.65)
    if (loss > 0.05) {
      sink.sprite(LOSS, PALETTE.pink, loss * severity * 0.55 * live, 0.9, Shape.glow, 1, true)
    }
  }
}

export type TrainingScene = ReturnType<typeof createTrainingScene>

export type Camera = {
  /** Column-major 3×3 rotation, ready for uniformMatrix3fv. */
  rotation: Float32Array
  /** CSS-pixel projection centre and world-unit size. */
  cx: number
  cy: number
  unit: number
  distance: number
  expand: number
  /** CSS-pixel rectangle behind the chapter copy, where cloud tokens recede. */
  quiet: readonly [number, number, number, number]
}

/** Scroll orbits the camera around the network; the pointer adds a small parallax. */
export function createCamera(
  time: number,
  pointer: { x: number; y: number },
  progress: number,
  width: number,
  height: number,
  layout: 'side' | 'center' = 'side'
): Camera {
  const narrow = width < 650
  const centered = layout === 'center'
  const orbit = ramp(0.06, 0.5, progress) - ramp(0.6, 0.98, progress)
  const ry = -0.5 + orbit * 0.78 + pointer.x * 0.24 + Math.sin(time * 0.09) * 0.05
  const rx = 0.2 + orbit * 0.12 + pointer.y * 0.12 + Math.sin(time * 0.07) * 0.02
  const [sy, cy, sx, cx] = [Math.sin(ry), Math.cos(ry), Math.sin(rx), Math.cos(rx)]
  // Columns are the rotated basis vectors: yaw around Y, then pitch around X.
  const rotation = new Float32Array([
    cy, sx * sy, -cx * sy,
    0, cx, sx,
    sy, -sx * cy, cx * cy,
  ])
  const zoom = narrow ? 1 - orbit * 0.1 : 1
  const unit = centered
    ? Math.min(width * 0.1, height * 0.16)
    : Math.min(width * (narrow ? 0.108 : 0.058), height * (narrow ? 0.07 : 0.118))
  return {
    rotation,
    cx: width * (centered ? 0.5 : narrow ? 0.5 + orbit * 0.03 : 0.285 + orbit * 0.035),
    cy: height * (centered ? 0.5 : narrow ? 0.27 : 0.53),
    unit: unit * zoom,
    distance: 12,
    expand: 1 + orbit * 1.5,
    quiet: centered
      ? [-1e4, -1e4, -1e4, -1e4]
      : narrow
        ? [0, height * 0.44, width, height]
        : [width * 0.53, 0, width, height],
  }
}

/** Projects a world point to CSS pixels; `scale` is the perspective factor. */
export function projectPoint(camera: Camera, p: CorePoint) {
  const m = camera.rotation
  const x = p.x - PIVOT.x,
    y = p.y - PIVOT.y,
    z = p.z * camera.expand - PIVOT.z
  const rx = m[0] * x + m[3] * y + m[6] * z
  const ry = m[1] * x + m[4] * y + m[7] * z
  const rz = m[2] * x + m[5] * y + m[8] * z
  const scale = camera.distance / Math.max(camera.distance - rz, 0.5)
  return {
    x: camera.cx + rx * camera.unit * scale,
    y: camera.cy - ry * camera.unit * scale,
    depth: rz,
    scale,
  }
}
