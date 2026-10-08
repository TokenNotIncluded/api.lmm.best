/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
export type Vec3 = [number, number, number]
export type Point = {
  x: number
  y: number
  z: number
  color: Vec3
  part: number
  closed?: Vec3
}
export type Deform = (point: Point, position: Vec3) => void
export type Sculpture = {
  points: Point[]
  view?: [number, number]
  animate: (seconds: number) => Deform
}
export const TAU = Math.PI * 2
export const mix = (a: number, b: number, t: number) => a + (b - a) * t
export const smooth = (t: number) => t * t * (3 - 2 * t)
export const hash = (n: number) => {
  const v = Math.sin(n * 127.1 + 311.7) * 43758.5453
  return v - Math.floor(v)
}
export const palette = {
  cream: [234, 224, 203] as Vec3,
  rose: [233, 150, 181] as Vec3,
  teal: [101, 186, 171] as Vec3,
  gold: [235, 182, 105] as Vec3,
  violet: [168, 157, 225] as Vec3,
  blue: [96, 156, 218] as Vec3,
  ink: [40, 52, 65] as Vec3,
  rust: [211, 113, 86] as Vec3,
}
export const tint = (color: Vec3, light: number): Vec3 => [
  color[0] * light, color[1] * light, color[2] * light,
]
export function rotate(axis: 0 | 1 | 2, angle: number, center: Vec3 = [0, 0, 0]): Deform {
  const a = (axis + 1) % 3,
    b = (axis + 2) % 3
  const c = Math.cos(angle),
    s = Math.sin(angle)
  return (_p, v) => {
    const x = v[a] - center[a],
      y = v[b] - center[b]
    v[a] = center[a] + x * c - y * s
    v[b] = center[b] + x * s + y * c
  }
}

/** Geometry is sampled once. Animation writes into the renderer's scratch vector. */
export class Shape {
  points: Point[] = []
  add(v: Vec3, color: Vec3, part = 0) {
    this.points.push({ x: v[0], y: v[1], z: v[2], color, part })
  }
  surface(
    rows: number,
    columns: number,
    sample: (u: number, v: number) => Vec3,
    color: Vec3 | ((u: number, v: number) => Vec3),
    part = 0
  ) {
    for (let row = 0; row < rows; row++) {
      for (let col = 0; col < columns; col++) {
        const u = row / (rows - 1),
          v = col / (columns - 1)
        this.add(sample(u, v), typeof color === 'function' ? color(u, v) : color, part)
      }
    }
  }
  ellipsoid(center: Vec3, radius: Vec3, color: Vec3, part = 0, count = 1400) {
    for (let i = 0; i < count; i++) {
      const y = 1 - 2 * (i + 0.5) / count,
        r = Math.sqrt(1 - y * y),
        a = i * 2.3999632297
      const x = Math.cos(a) * r,
        z = Math.sin(a) * r
      const light = 0.61 + 0.39 * Math.max(0, -x * 0.35 + y * 0.5 + z * 0.65)
      this.add(
        [center[0] + radius[0] * x, center[1] + radius[1] * y, center[2] + radius[2] * z],
        tint(color, light),
        part
      )
    }
  }
  tube(
    path: (t: number) => Vec3,
    radius: number,
    color: Vec3,
    part = 0,
    steps = 60,
    sides = 10
  ) {
    for (let i = 0; i < steps; i++) {
      const t = i / (steps - 1),
        p = path(t),
        q = path(Math.min(1, t + 0.001)),
        prev = path(Math.max(0, t - 0.001))
      const dx = q[0] - prev[0],
        dy = q[1] - prev[1],
        dz = q[2] - prev[2]
      const length = Math.hypot(dx, dy, dz) || 1
      const tx = dx / length,
        ty = dy / length,
        tz = dz / length
      // Choose a reference axis that cannot be parallel to the tangent.
      const nx = Math.abs(ty) < 0.9 ? -tz : 0
      const ny = Math.abs(ty) < 0.9 ? 0 : tz
      const nz = Math.abs(ty) < 0.9 ? tx : -ty
      const norm = Math.hypot(nx, ny, nz) || 1
      const ax = nx / norm,
        ay = ny / norm,
        az = nz / norm
      const bx = ty * az - tz * ay,
        by = tz * ax - tx * az,
        bz = tx * ay - ty * ax
      for (let j = 0; j < sides; j++) {
        const a = j / sides * TAU,
          c = Math.cos(a) * radius,
          s = Math.sin(a) * radius
        this.add(
          [p[0] + ax * c + bx * s, p[1] + ay * c + by * s, p[2] + az * c + bz * s],
          tint(color, 0.72 + 0.28 * (j / sides)),
          part
        )
      }
    }
  }
  line(a: Vec3, b: Vec3, radius: number, color: Vec3, part = 0, steps = 28) {
    this.tube(
      t => [mix(a[0], b[0], t), mix(a[1], b[1], t), mix(a[2], b[2], t)],
      radius,
      color,
      part,
      steps,
      8
    )
  }
  ring(
    center: Vec3,
    radius: number,
    thickness: number,
    color: Vec3,
    part = 0,
    axis: 0 | 1 | 2 = 2
  ) {
    const a = (axis + 1) % 3,
      b = (axis + 2) % 3
    this.tube(
      t => {
        const v: Vec3 = [...center]
        v[a] += Math.cos(t * TAU) * radius
        v[b] += Math.sin(t * TAU) * radius
        return v
      },
      thickness,
      color,
      part,
      140,
      12
    )
  }
  box(center: Vec3, size: Vec3, color: Vec3, part = 0) {
    for (let axis = 0; axis < 3; axis++) {
      const a = (axis + 1) % 3,
        b = (axis + 2) % 3
      for (const side of [-1, 1]) {
        this.surface(
          Math.max(3, Math.ceil(size[a] * 43)),
          Math.max(3, Math.ceil(size[b] * 43)),
          (u, v) => {
            const point: Vec3 = [...center]
            point[axis] += side * size[axis] / 2
            point[a] += (u - 0.5) * size[a]
            point[b] += (v - 0.5) * size[b]
            return point
          },
          tint(color, axis === 2 && side === 1 ? 1 : axis === 1 ? 0.88 : 0.66),
          part
        )
      }
    }
  }
  model(animate: Sculpture['animate'], view: [number, number] = [0, 0]): Sculpture {
    return { points: this.points, animate, view }
  }
}
