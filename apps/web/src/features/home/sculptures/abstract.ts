/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { Shape, TAU, palette as C, rotate, tint, type Sculpture, type Vec3 } from './geometry'

function cage(s: Shape, radius: number, color: Vec3, part = 0) {
  const phi = (1 + Math.sqrt(5)) / 2, norm = Math.hypot(1, phi)
  const vertices: Vec3[] = []
  for (const a of [-1, 1]) for (const b of [-phi, phi]) {
    vertices.push([0, a, b], [a, b, 0], [b, 0, a])
  }
  const points = vertices.map((p) => p.map((v) => v / norm * radius) as Vec3)
  points.forEach((p, index) => {
    s.ellipsoid(p, [0.028, 0.028, 0.028], C.cream, part, 80)
    for (let j = index + 1; j < points.length; j++) {
      const q = points[j]
      if (Math.hypot(p[0] - q[0], p[1] - q[1], p[2] - q[2]) < radius * 1.1) {
        s.line(p, q, 0.01, color, part, 20)
      }
    }
  })
}

export function socket(): Sculpture {
  const s = new Shape()
  for (const [radius, z] of [[0.84, -0.12], [0.69, 0.15], [0.37, 0.24]]) {
    s.ring(
      [0, 0, z],
      radius,
      radius > 0.5 ? 0.045 : 0.025,
      radius > 0.7 ? C.violet : C.gold
    )
  }
  for (let rib = 0; rib < 18; rib++) {
    const angle = rib / 18 * TAU
    s.tube(
      (t) => {
        const a = angle + Math.sin(t * Math.PI) * 0.23, r = 0.38 + t * 0.76
        return [
          Math.cos(a) * r,
          Math.sin(a) * r,
          0.19 - t * 0.3 + Math.sin(t * Math.PI) * 0.13
        ]
      },
      0.023,
      rib % 3 ? C.violet : C.teal,
      0,
      60,
      8
    )
    s.tube((t) => {
      const a = angle + (t - 0.5) * 0.14
      return [Math.cos(a) * 1.14, Math.sin(a) * 1.14, -0.11]
    }, 0.031, C.gold, 0, 20, 8)
    s.ellipsoid(
      [Math.cos(angle) * 0.7, Math.sin(angle) * 0.7, 0.19],
      [0.025, 0.025, 0.025],
      C.cream,
      0,
      100
    )
  }
  cage(s, 0.29, C.teal, 1)
  s.ellipsoid([0, 0, 0], [0.095, 0.095, 0.095], C.rose, 1, 700)
  return s.model((t) => {
    const wheel = rotate(2, t * 0.16), core = rotate(1, -t * 0.43)
    return (p, v) => {
      if (p.part) core(p, v)
      wheel(p, v)
    }
  }, [0.13, 0.23])
}

export function gyroscope(): Sculpture {
  const s = new Shape()
  for (const axis of [0, 1, 2] as const) {
    const radius = 1.08 - axis * 0.19, color = [C.teal, C.gold, C.violet][axis]
    for (const rim of [-0.025, 0.025]) s.ring([0, 0, 0], radius + rim, 0.017, color, axis + 1, axis)
    const a = (axis + 1) % 3, b = (axis + 2) % 3
    for (let tick = 0; tick < 32; tick++) {
      const angle = tick / 32 * TAU
      const inner: Vec3 = [0, 0, 0], outer: Vec3 = [0, 0, 0]
      inner[a] = Math.cos(angle) * (radius - 0.025)
      inner[b] = Math.sin(angle) * (radius - 0.025)
      outer[a] = Math.cos(angle) * (radius + 0.025)
      outer[b] = Math.sin(angle) * (radius + 0.025)
      s.line(inner, outer, 0.006, C.cream, axis + 1, 6)
      if (tick % 8 === 0) s.ellipsoid(outer, [0.044, 0.044, 0.044], color, axis + 1, 160)
    }
  }
  cage(s, 0.45, C.rose, 4)
  s.ellipsoid([0, 0, 0], [0.15, 0.15, 0.15], C.gold, 0, 1100)
  return s.model(
    (t) => {
      const turns = [
        rotate(1, t * 0.53),
        rotate(2, -t * 0.41),
        rotate(0, t * 0.31),
        rotate(1, -t * 0.19)
      ]
      return (p, v) => { if (p.part) turns[p.part - 1](p, v) }
    },
    [0.28, 0.29]
  )
}

export function mobius(): Sculpture {
  const s = new Shape()
  const strip = (u: number, v: number): Vec3 => {
    const a = u * TAU, w = (v - 0.5) * 0.66, r = 0.8 + Math.cos(a / 2) * w
    return [Math.cos(a) * r, Math.sin(a) * r, Math.sin(a / 2) * w]
  }
  for (let band = 0; band < 3; band++) {
    s.surface(
      210,
      17,
      (u, v) => strip(u, (band + 0.05 + v * 0.9) / 3),
      (u, v) => tint(
        band === 1 ? C.teal : C.violet,
        0.66 + Math.sin(u * Math.PI) * 0.27 + Math.sin(v * Math.PI) * 0.07
      )
    )
  }
  for (const v of [0, 1 / 3, 2 / 3, 1]) s.tube((u) => strip(u, v), 0.008, C.gold, 0, 230, 5)
  for (let rib = 0; rib < 48; rib++) s.tube((v) => strip(rib / 48, v), 0.005, tint(C.cream, 0.82), 0, 22, 4)
  return s.model((t) => rotate(1, t * 0.23), [0.1, 0.42])
}

export function trefoil(): Sculpture {
  const s = new Shape()
  const strand = (t: number, phase: number): Vec3 => {
    const a = t * TAU,
      r = (2 + Math.cos(3 * a)) * 0.32,
      dr = -0.96 * Math.sin(3 * a)
    const x = Math.cos(2 * a), y = Math.sin(2 * a)
    let tx = dr * x - 2 * r * y,
      ty = dr * y + 2 * r * x,
      tz = 0.96 * Math.cos(3 * a)
    const length = Math.hypot(tx, ty, tz)
    tx /= length
    ty /= length
    tz /= length
    let bx = -tz * y, by = tz * x, bz = tx * y - ty * x
    const norm = Math.hypot(bx, by, bz)
    bx /= norm
    by /= norm
    bz /= norm
    const nx = by * tz - bz * ty,
      ny = bz * tx - bx * tz,
      nz = bx * ty - by * tx
    const c = Math.cos(a * 12 + phase) * 0.065,
      d = Math.sin(a * 12 + phase) * 0.065
    return [
      x * r + nx * c + bx * d,
      y * r + ny * c + by * d,
      Math.sin(3 * a) * 0.32 + nz * c + bz * d
    ]
  }
  for (let band = 0; band < 3; band++) s.tube(
    (t) => strand(t, band / 3 * TAU),
    0.043,
    [C.teal, C.gold, C.violet][band],
    0,
    520,
    12
  )
  return s.model((t) => {
    const turn = rotate(1, t * 0.24), roll = rotate(2, t * 0.08)
    return (p, v) => {
      turn(p, v)
      roll(p, v)
    }
  }, [0, 0.14])
}

export function helix(): Sculpture {
  const s = new Shape()
  const rail = (u: number, phase: number): Vec3 => [
    Math.cos(u * TAU * 1.8 + phase) * 0.49,
    (u - 0.5) * 2.18,
    Math.sin(u * TAU * 1.8 + phase) * 0.49
  ]
  for (const phase of [0, Math.PI]) {
    s.tube((u) => rail(u, phase), 0.05, phase ? C.rose : C.teal, 0, 240, 12)
    for (let node = 0; node < 23; node++) s.ellipsoid(
      rail(node / 22, phase),
      [0.068, 0.068, 0.068],
      phase ? C.rose : C.teal,
      0,
      135
    )
  }
  for (let i = 0; i < 23; i++) {
    const a = rail(i / 22, 0),
      b = rail(i / 22, Math.PI),
      center: Vec3 = [0, a[1], 0]
    s.line(a, center, 0.022, i % 2 ? C.gold : C.violet, 0, 24)
    s.line(center, b, 0.022, i % 2 ? C.violet : C.gold, 0, 24)
    s.ellipsoid(center, [0.029, 0.029, 0.029], C.cream, 0, 70)
  }
  for (let side = 0; side < 2; side++) s.ellipsoid([0, 0, 0], [0.075, 0.075, 0.075], C.cream, side + 1, 260)
  return s.model(
    (t) => {
      const turn = rotate(1, t * 0.36),
        lights = [
          rail((1 + Math.sin(t * 0.8)) / 2, 0),
          rail((1 + Math.sin(t * 0.8 + Math.PI)) / 2, Math.PI)
        ]
      return (p, v) => {
        if (p.part) for (let i = 0; i < 3; i++) v[i] += lights[p.part - 1][i]
        turn(p, v)
        for (let i = 0; i < 3; i++) v[i] *= 0.9
      }
    },
    [0.05, 0.12]
  )
}

export function ribbon(): Sculpture {
  const s = new Shape()
  for (let band = 0; band < 3; band++) {
    const strip = (u: number, v: number): Vec3 => {
      const a = u * TAU,
        phase = band / 3 * TAU,
        r = 0.8 + Math.cos(a * 3 + phase) * 0.13 + (v - 0.5) * 0.2
      return [
        Math.cos(a) * r,
        Math.sin(a * 3 + phase) * 0.31 + (band - 1) * 0.13,
        Math.sin(a) * r
      ]
    }
    s.surface(
      170,
      27,
      strip,
      (u, v) => tint(
        [C.violet, C.rose, C.teal][band],
        0.65 + Math.sin(v * Math.PI) * 0.22 + Math.sin(u * Math.PI) * 0.1
      )
    )
    for (const v of [0, 1]) s.tube((u) => strip(u, v), 0.008, C.gold, 0, 170, 5)
    for (let rib = 0; rib < 27; rib++) s.tube((v) => strip(rib / 27, v), 0.004, tint(C.cream, 0.84), 0, 14, 3)
  }
  cage(s, 0.26, C.gold, 1)
  return s.model((t) => {
    const turn = rotate(1, t * 0.15), core = rotate(2, -t * 0.35)
    return (p, v) => {
      if (p.part) core(p, v)
      else v[1] += Math.sin(t * 1.3 + p.x * 3 + p.z * 2) * 0.065
      turn(p, v)
    }
  }, [0.05, 0.7])
}
