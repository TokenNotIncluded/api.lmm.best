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
const copper = [189, 128, 78] as const
const alloy = [206, 211, 190] as const
const board = [32, 64, 57] as const
const ceramic = [36, 43, 41] as const
const gold = [211, 175, 108] as const
const smooth = (a: number, b: number, p: number) => {
  const t = Math.min(1, Math.max(0, (p - a) / (b - a)))
  return t * t * (3 - 2 * t)
}

/** Authored beveled plates, chip packages, contacts and bus traces, not a stock torus. */
export function createCoreMesh(): CoreFace[] {
  const faces: CoreFace[] = []
  const plate = (
    x: number,
    y: number,
    z: number,
    w: number,
    h: number,
    depth: number,
    color: CoreFace['color'],
    layer: number,
    cut = 0.035,
    hinge = 0,
    shine = 1
  ) => {
    const a = w / 2,
      b = h / 2,
      c = Math.min(cut, a / 2, b / 2)
    const outline = [
      [-a + c, -b],
      [a - c, -b],
      [a, -b + c],
      [a, b - c],
      [a - c, b],
      [-a + c, b],
      [-a, b - c],
      [-a, -b + c],
    ]
    const front = outline.map(([px, py]) => ({
      x: x + px,
      y: y + py,
      z: z + depth / 2,
    }))
    const back = front.map((p) => ({ ...p, z: z - depth / 2 }))
    faces.push({
      points: front,
      normal: { x: 0, y: 0, z: 1 },
      color,
      layer,
      hinge,
      shine,
    })
    faces.push({
      points: [...back].reverse(),
      normal: { x: 0, y: 0, z: -1 },
      color,
      layer,
      hinge,
      shine,
    })
    for (let i = 0; i < front.length; i++) {
      const j = (i + 1) % front.length,
        dx = front[j].x - front[i].x,
        dy = front[j].y - front[i].y,
        len = Math.hypot(dx, dy)
      faces.push({
        points: [back[i], back[j], front[j], front[i]],
        normal: { x: dy / len, y: -dx / len, z: 0 },
        color,
        layer,
        hinge,
        shine,
      })
    }
  }
  // Chassis, routed board and the socket are separate objects with real thickness.
  plate(0, 0, -0.42, 3.06, 2.58, 0.16, alloy, -1, 0.2)
  plate(0, 0, -0.27, 2.9, 2.42, 0.09, copper, -0.8, 0.16)
  plate(0, 0, -0.13, 2.8, 2.3, 0.11, board, -0.65, 0.13, 0, 0.25)
  plate(0, 0, 0.1, 1.72, 1.62, 0.25, ceramic, 0, 0.12, 0, 0.45)
  plate(0, 0, 0.265, 1.55, 1.45, 0.06, gold, 0, 0.1)
  plate(0, 0, 0.34, 1.36, 1.27, 0.09, board, 0, 0.08, 0, 0.6)
  // A fine tiled die is revealed when the two machined heat-spreader leaves open.
  for (let row = 0; row < 5; row++) {
    for (let col = 0; col < 6; col++) {
      plate(
        (col - 2.5) * 0.194,
        (row - 2) * 0.213,
        0.405,
        0.172,
        0.19,
        0.028,
        row % 2 ? alloy : copper,
        0,
        0.012
      )
    }
  }
  plate(-0.385, 0, 0.58, 0.75, 1.52, 0.13, alloy, 0.6, 0.075, -1)
  plate(0.385, 0, 0.58, 0.75, 1.52, 0.13, copper, 0.6, 0.075, 1)
  // Heat-sink machining: individual ribs catch the key light as the core turns.
  for (let i = 0; i < 10; i++) {
    const y = (i - 4.5) * 0.126
    plate(-0.385, y, 0.67, 0.59, 0.034, 0.045, alloy, 0.6, 0.01, -1)
    plate(0.385, y, 0.67, 0.59, 0.034, 0.045, copper, 0.6, 0.01, 1)
  }
  // Socket contacts, visible bus traces and small memory packages.
  for (let i = 0; i < 12; i++) {
    const offset = (i - 5.5) * 0.12
    for (const side of [-1, 1]) {
      plate(side * 0.97, offset, 0.05, 0.19, 0.043, 0.045, gold, -0.25, 0.009)
      plate(offset, side * 0.92, 0.05, 0.043, 0.19, 0.045, gold, -0.25, 0.009)
    }
  }
  for (const side of [-1, 1]) {
    for (let i = 0; i < 4; i++) {
      const y = (i - 1.5) * 0.47
      plate(
        side * 1.245,
        y,
        -0.01,
        0.27,
        0.34,
        0.1,
        ceramic,
        -0.65,
        0.025,
        0,
        0.3
      )
      plate(
        side * 1.245,
        y,
        0.047,
        0.17,
        0.22,
        0.014,
        alloy,
        -0.65,
        0.018,
        0,
        0.4
      )
      plate(side * 0.99, y, -0.061, 0.35, 0.018, 0.012, copper, -0.65, 0.003)
    }
  }
  for (const x of [-1.3, 1.3]) {
    for (const y of [-1.07, 1.07]) {
      plate(x, y, -0.015, 0.14, 0.14, 0.08, alloy, -0.65, 0.055)
      plate(x, y, 0.034, 0.084, 0.016, 0.01, ceramic, -0.65, 0.002)
    }
  }
  return faces
}

/** Explode, unfold, then recombine. Scroll is reversible and never changes topology. */
export function transformCorePoint(
  point: CorePoint,
  face: Pick<CoreFace, 'layer' | 'hinge'>,
  progress: number
): CorePoint {
  const spread = smooth(0.06, 0.38, progress) * (1 - smooth(0.72, 1, progress))
  const fold = smooth(0.24, 0.55, progress) * (1 - smooth(0.76, 1, progress))
  const twist = face.layer * spread * 0.18
  let x = point.x * Math.cos(twist) - point.y * Math.sin(twist)
  const y = point.x * Math.sin(twist) + point.y * Math.cos(twist)
  let z = point.z + face.layer * spread * 1.7
  if (face.hinge) {
    const pivot = face.hinge * 0.79,
      angle = face.hinge * fold * 1.24
    const dx = x - pivot,
      dz = z - (0.58 + face.layer * spread * 1.7)
    x = pivot + dx * Math.cos(angle) + dz * Math.sin(angle)
    z =
      0.58 +
      face.layer * spread * 1.7 -
      dx * Math.sin(angle) +
      dz * Math.cos(angle)
  }
  return { x, y: y * (1 - smooth(0.72, 1, progress) * 0.07), z }
}
