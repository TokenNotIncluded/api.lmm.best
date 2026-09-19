/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
export type CorePoint = { x: number; y: number; z: number }
export type CoreFace = {
  points: CorePoint[]
  normal: CorePoint
  color: readonly [number, number, number]
  layer: number
  hinge: number
  shine: number
}
export type SignalPath = { points: CorePoint[]; color: CoreFace['color'] }
const graphite = [87, 87, 88] as const
const sage = [123, 140, 120] as const
const clay = [184, 117, 78] as const
const ivory = [204, 197, 183] as const
const ink = [106, 107, 101] as const
const point = (x: number, y: number, z: number): CorePoint => ({ x, y, z })
const layers = [-1.95, 0, 1.95]
const heads = [-0.99, -0.33, 0.33, 0.99]
const smooth = (a: number, b: number, p: number) => {
  const t = Math.min(1, Math.max(0, (p - a) / (b - a)))
  return t * t * (3 - 2 * t)
}
export function coreExpansion(progress: number) {
  return smooth(0.04, 0.38, progress) * (1 - smooth(0.76, 1, progress))
}

/** A reduced, illustrative decoder stack. Cell values are visual texture, not claimed model weights. */
export function createCoreMesh(): CoreFace[] {
  const faces: CoreFace[] = []
  const box = (
    x: number,
    y: number,
    z: number,
    w: number,
    h: number,
    d: number,
    color: CoreFace['color'],
    layer = 0
  ) => {
    const a = x - w / 2,
      b = x + w / 2,
      c = y - h / 2,
      e = y + h / 2,
      f = z - d / 2,
      g = z + d / 2
    const sides: [CorePoint[], CorePoint][] = [
      [
        [point(a, c, g), point(b, c, g), point(b, e, g), point(a, e, g)],
        point(0, 0, 1),
      ],
      [
        [point(b, c, f), point(a, c, f), point(a, e, f), point(b, e, f)],
        point(0, 0, -1),
      ],
      [
        [point(a, c, f), point(a, c, g), point(a, e, g), point(a, e, f)],
        point(-1, 0, 0),
      ],
      [
        [point(b, c, g), point(b, c, f), point(b, e, f), point(b, e, g)],
        point(1, 0, 0),
      ],
      [
        [point(a, e, g), point(b, e, g), point(b, e, f), point(a, e, f)],
        point(0, 1, 0),
      ],
      [
        [point(a, c, f), point(b, c, f), point(b, c, g), point(a, c, g)],
        point(0, -1, 0),
      ],
    ]
    for (const [points, normal] of sides) {
      faces.push({ points, normal, color, layer, hinge: 0, shine: 0.15 })
    }
  }
  const matrix = (
    x: number,
    y: number,
    z: number,
    cols: number,
    rows: number,
    step: number,
    color: CoreFace['color'],
    layer: number,
    depth = 0.032
  ) => {
    // Individual cells, recessed backing and a narrow continuous frame remain visible when enlarged.
    box(
      x,
      y,
      z - depth * 0.75,
      cols * step + 0.026,
      rows * step + 0.026,
      0.018,
      ivory,
      layer
    )
    for (let row = 0; row < rows; row++) {
      for (let col = 0; col < cols; col++) {
        const shade =
          0.8 + ((row * 7 + col * 11 + Math.round((layer + 2) * 9)) % 9) * 0.038
        const tint = color.map((c) => Math.min(255, Math.round(c * shade))) as [
          number,
          number,
          number,
        ]
        box(
          x + (col - (cols - 1) / 2) * step,
          y + (row - (rows - 1) / 2) * step,
          z,
          step * 0.89,
          step * 0.89,
          depth,
          tint,
          layer
        )
      }
    }
  }
  for (const base of layers) {
    // Layer normalization is a row of scalar cells below each attention block.
    matrix(0, base - 0.69, 0, 16, 1, 0.125, sage, base)
    for (let head = 0; head < heads.length; head++) {
      const x = heads[head]
      // Three distinct Q/K/V projections recede in depth for each head.
      for (let qkv = 0; qkv < 3; qkv++) {
        matrix(
          x,
          base - 0.33,
          -0.38 + qkv * 0.24,
          4,
          3,
          0.115,
          qkv === 1 ? sage : graphite,
          base,
          0.04
        )
      }
      // Causal attention matrix: lower triangle, with the mask retained as pale cells.
      for (let row = 0; row < 4; row++) {
        for (let col = 0; col < 4; col++) {
          box(
            x + (col - 1.5) * 0.105,
            base + 0.12 + (row - 1.5) * 0.105,
            0.16,
            0.094,
            0.094,
            0.035,
            col <= row ? graphite : ivory,
            base
          )
        }
      }
      box(x, base + 0.38, 0.16, 0.47, 0.035, 0.09, sage, base)
    }
    matrix(0, base + 0.57, 0.08, 16, 2, 0.125, graphite, base)
    // Feed-forward expansion and contraction: two dense rectangular tensors, not decorative loops.
    matrix(0, base + 0.91, -0.04, 20, 3, 0.112, graphite, base)
    matrix(0, base + 0.91, 0.22, 20, 3, 0.112, sage, base)
    box(1.46, base + 0.08, 0.05, 0.028, 1.52, 0.028, clay, base)
    box(0.76, base - 0.67, 0.05, 1.42, 0.028, 0.028, clay, base)
    box(0.76, base + 0.82, 0.05, 1.42, 0.028, 0.028, clay, base)
    // Add nodes and small junction contacts on the residual rail.
    for (const y of [base - 0.67, base + 0.82]) {
      box(1.46, y, 0.05, 0.082, 0.082, 0.082, clay, base)
    }
  }
  // Token embeddings and the output projection cap the full stack.
  matrix(0, -3.08, 0, 16, 3, 0.125, graphite, -3.08)
  matrix(0, 3.28, 0, 16, 2, 0.125, graphite, 3.28)
  for (let i = 0; i < 8; i++) {
    box(
      (i - 3.5) * 0.22,
      -3.38,
      0,
      0.16,
      0.08,
      0.07,
      i % 3 === 0 ? clay : ivory,
      -3.08
    )
    box(
      (i - 3.5) * 0.22,
      3.52,
      0,
      0.13,
      0.065,
      0.065,
      i === 5 ? clay : ivory,
      3.52
    )
  }
  return faces
}

/** Orthogonal data routes and per-head fan-outs preserve recognizable architectural relationships. */
export function createSignalPaths(): SignalPath[] {
  const paths: SignalPath[] = []
  const add = (points: CorePoint[], color: CoreFace['color'] = ink) =>
    paths.push({ points, color })
  for (const base of layers) {
    for (const x of heads) {
      for (let channel = 0; channel < 3; channel++) {
        const z = -0.38 + channel * 0.24
        add([
          point(x, base - 0.69, 0),
          point(x, base - 0.55, 0),
          point(x, base - 0.55, z),
          point(x, base - 0.33, z),
          point(x, base - 0.09, z),
          point(x, base - 0.09, 0.16),
          point(x, base + 0.12, 0.16),
        ])
      }
      for (let col = 0; col < 4; col++) {
        const a = x + (col - 1.5) * 0.105
        add([
          point(a, base + 0.33, 0.16),
          point(a, base + 0.42, 0.16),
          point(a * 0.7, base + 0.48, 0.08),
          point(a * 0.7, base + 0.57, 0.08),
        ])
      }
    }
    for (let i = 0; i < 12; i++) {
      const x = (i - 5.5) * 0.15
      add([
        point(x, base + 0.69, 0.08),
        point(x, base + 0.72, 0.08),
        point(x * 1.2, base + 0.75, -0.04),
        point(x * 1.2, base + 0.91, -0.04),
        point(x * 1.2, base + 1.1, 0.22),
      ])
    }
    add(
      [
        point(0, base - 0.69, 0.05),
        point(1.46, base - 0.69, 0.05),
        point(1.46, base + 0.82, 0.05),
        point(0, base + 0.82, 0.05),
      ],
      clay
    )
  }
  for (let i = 0; i < 8; i++) {
    const x = (i - 3.5) * 0.22
    add([point(x, -3.38, 0), point(x, -3.08, 0), point(x, -2.64, 0)])
    for (const base of [-1.95, 0]) {
      add([
        point(x, base + 1.1, 0.22),
        point(x, base + 1.22, 0.22),
        point(x, base + 1.22, 0),
        point(x, base + 1.26, 0),
      ])
    }
    add([
      point(x, 3.06, 0.22),
      point(x, 3.15, 0.22),
      point(x, 3.15, 0),
      point(x, 3.52, 0),
    ])
  }
  return paths
}

export function transformCorePoint(
  p: CorePoint,
  _face: Pick<CoreFace, 'layer' | 'hinge'>,
  progress: number
): CorePoint {
  const spread = coreExpansion(progress)
  return {
    x: p.x * (1 + spread * 0.12),
    y: p.y * (1 + spread * 0.12),
    z: p.z * (1 + spread * 2.8),
  }
}
