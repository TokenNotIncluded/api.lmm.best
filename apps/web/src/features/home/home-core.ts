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
const copper = [206, 132, 89] as const
const ivory = [236, 229, 207] as const
const jade = [132, 164, 132] as const
const gold = [222, 181, 113] as const
const smooth = (a: number, b: number, p: number) => {
  const t = Math.min(1, Math.max(0, (p - a) / (b - a)))
  return t * t * (3 - 2 * t)
}
const point = (x: number, y: number, z: number): CorePoint => ({ x, y, z })
const heads = [-0.96, -0.32, 0.32, 0.96]

/** Conceptual token → parallel attention → feed-forward → output flow, not a model specification. */
export function createSignalPaths(): SignalPath[] {
  const paths: SignalPath[] = []
  for (let i = 0; i < 12; i++) {
    const inputX = (i - 5.5) * 0.19
    const headX = heads[i % heads.length]
    const z = ((i % 3) - 1) * 0.34
    const points = Array.from({ length: 49 }, (_, j) => {
      const t = j / 48
      const y = -1.72 + t * 3.44
      const branch = Math.sin(Math.PI * t) ** 2
      return point(
        inputX * (1 - branch) + headX * branch,
        y,
        z + Math.sin(t * Math.PI * 2 + i * 0.3) * 0.15 * branch
      )
    })
    paths.push({ points, color: i % 3 === 0 ? copper : ivory })
  }
  // Residual routes bypass the attention block and rejoin above it.
  for (const side of [-1, 1]) {
    paths.push({
      points: Array.from({ length: 65 }, (_, j) => {
        const t = j / 64
        return point(
          side * (0.45 + Math.sin(Math.PI * t) * 1.03),
          -1.25 + t * 2.65,
          -0.15 + Math.sin(t * Math.PI) * 0.15
        )
      }),
      color: gold,
    })
  }
  return paths
}

/** Sculpted attention ribbons and token facets; no external model or GPU runtime. */
export function createCoreMesh(): CoreFace[] {
  const faces: CoreFace[] = []
  const face = (
    points: CorePoint[],
    color: CoreFace['color'],
    layer = 0,
    hinge = 0,
    shine = 1
  ) => {
    faces.push({ points, normal: point(0, 0, 1), color, layer, hinge, shine })
  }
  const ribbon = (
    curve: (t: number) => CorePoint,
    width: number,
    color: CoreFace['color'],
    layer: number,
    hinge: number,
    segments = 48
  ) => {
    const rails = Array.from({ length: segments + 1 }, (_, i) => {
      const t = i / segments
      const p = curve(t)
      const twist = t * Math.PI * 2 + hinge * 0.6
      const dx = (Math.cos(twist) * width) / 2
      const dz = (Math.sin(twist) * width) / 2
      return [point(p.x - dx, p.y, p.z - dz), point(p.x + dx, p.y, p.z + dz)]
    })
    for (let i = 0; i < segments; i++) {
      face(
        [rails[i][0], rails[i + 1][0], rails[i + 1][1], rails[i][1]],
        color,
        layer,
        hinge
      )
      // A narrow folded rim catches light without a flat billboard outline.
      const rim = [rails[i][0], rails[i + 1][0]].map((p) =>
        point(p.x, p.y + 0.026, p.z)
      )
      face(
        [rails[i][0], rim[0], rim[1], rails[i + 1][0]],
        ivory,
        layer,
        hinge,
        0.6
      )
    }
  }
  const token = (
    x: number,
    y: number,
    z: number,
    r: number,
    color: CoreFace['color'],
    layer: number
  ) => {
    const equator = [
      point(x - r, y, z),
      point(x, y, z + r),
      point(x + r, y, z),
      point(x, y, z - r),
    ]
    for (let i = 0; i < 4; i++) {
      face(
        [equator[i], equator[(i + 1) % 4], point(x, y + r * 1.5, z)],
        color,
        layer
      )
      face(
        [equator[(i + 1) % 4], equator[i], point(x, y - r * 1.5, z)],
        color,
        layer
      )
    }
  }
  // Four parallel heads: open, twisted silk-like loops with distinguishable depths.
  for (let head = 0; head < heads.length; head++) {
    const x = heads[head]
    const z = head % 2 ? 0.17 : -0.17
    ribbon(
      (t) => {
        const a = t * Math.PI * 2
        return point(
          x + Math.cos(a) * 0.24,
          -0.35 + Math.sin(a) * 0.58,
          z + Math.sin(a * 2) * 0.18
        )
      },
      0.2,
      head % 2 ? copper : ivory,
      -0.3,
      head - 1.5
    )
    // Attention intersections form a fine internal lattice, not a solid slab.
    for (let i = 0; i < 3; i++) {
      ribbon(
        (t) =>
          point(
            x + (t - 0.5) * 0.36,
            -0.65 + i * 0.27 + Math.sin(t * Math.PI) * 0.11,
            z + 0.02
          ),
        0.045,
        jade,
        -0.3,
        head - 1.5,
        12
      )
    }
  }
  // Feed-forward fan: narrow folded strips expand, project and converge.
  for (let i = 0; i < 11; i++) {
    const x = (i - 5) * 0.15
    ribbon(
      (t) =>
        point(
          x * (0.62 + Math.sin(t * Math.PI) * 0.6),
          0.42 + t * 0.65,
          (t - 0.5) * 0.6 + Math.sin(t * Math.PI) * 0.16
        ),
      0.062,
      i % 3 === 0 ? copper : ivory,
      0.8,
      0,
      20
    )
  }
  // Two add/norm seams connect all heads. Thin curled bands leave the flow visible.
  for (const y of [-1.1, 0.27, 1.22]) {
    ribbon(
      (t) =>
        point(
          (t - 0.5) * 2.3,
          y + Math.sin(t * Math.PI) * 0.055,
          Math.sin(t * Math.PI * 2) * 0.1
        ),
      0.075,
      gold,
      y,
      0,
      36
    )
  }
  for (const path of createSignalPaths().slice(-2)) {
    ribbon(
      (t) => path.points[Math.min(64, Math.round(t * 64))],
      0.045,
      gold,
      0,
      0,
      64
    )
  }
  for (const y of [-1.72, 1.72]) {
    for (let i = 0; i < 12; i++) {
      token(
        (i - 5.5) * 0.19,
        y,
        ((i % 3) - 1) * 0.34,
        0.065,
        i % 3 === 0 ? copper : ivory,
        y
      )
    }
  }
  return faces
}

/** Reversible expansion opens the heads, twists the stream, then returns to its assembled shape. */
export function transformCorePoint(
  point: CorePoint,
  _face: Pick<CoreFace, 'layer' | 'hinge'>,
  progress: number
): CorePoint {
  const spread = smooth(0.04, 0.38, progress) * (1 - smooth(0.74, 1, progress))
  const twist = Math.sin(point.y * 1.1) * spread * 0.48
  const x = point.x * (1 + spread * 0.35)
  const z = point.z * (1 + spread * 0.65)
  return {
    x: x * Math.cos(twist) - z * Math.sin(twist),
    y: point.y * (1 + spread * 0.23),
    z: x * Math.sin(twist) + z * Math.cos(twist),
  }
}
