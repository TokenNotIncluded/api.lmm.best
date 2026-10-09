/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { Shape, TAU, mix, palette as C, rotate, type Deform, type Vec3 } from './geometry'

export const CRANK: Vec3 = [-0.03, -0.64, 0]

export const PEDAL_RADIUS = 0.145

export const pedalAt = (seconds: number, side: number): Vec3 => {
  const angle = -seconds * 3 + (side < 0 ? Math.PI : 0)
  return [
    CRANK[0] + Math.cos(angle) * PEDAL_RADIUS,
    CRANK[1] + Math.sin(angle) * PEDAL_RADIUS,
    side * 0.18
  ]
}
/** Two fixed-length bones. The knee bends forward, and the foot stays on the pedal. */

export function kneeAt(hip: Vec3, ankle: Vec3): Vec3 {
  const dx = ankle[0] - hip[0], dy = ankle[1] - hip[1]
  const distance = Math.hypot(dx, dy)
  const upper = 0.39, lower = 0.4
  const along = (upper * upper - lower * lower + distance * distance) / (2 * distance)
  const height = Math.sqrt(Math.max(0, upper * upper - along * along))
  return [
    hip[0] + (dx * along - dy * height) / distance,
    hip[1] + (dy * along + dx * height) / distance,
    hip[2]
  ]
}

function bonePose(a: Vec3, b: Vec3, nextA: Vec3, nextB: Vec3): Deform {
  const angle = Math.atan2(nextB[1] - nextA[1], nextB[0] - nextA[0]) - Math.atan2(b[1] - a[1], b[0] - a[0])
  const c = Math.cos(angle), s = Math.sin(angle)
  return (_p, v) => {
    const x = v[0] - a[0], y = v[1] - a[1]
    v[0] = nextA[0] + x * c - y * s
    v[1] = nextA[1] + x * s + y * c
    v[2] += nextA[2] - a[2]
  }
}
/** Shared wheels, frame, crank, pedals and rider leg motion for both cyclists. */

export function bicycle(s: Shape, legColor: Vec3, footColor: Vec3, legRadius: number) {
  const rear: Vec3 = [-0.73, -0.78, 0], front: Vec3 = [0.74, -0.78, 0]
  for (const [index, center] of [rear, front].entries()) {
    s.ring(center, 0.365, 0.028, C.ink, index + 1)
    s.ring(center, 0.334, 0.009, C.cream, index + 1)
    s.ellipsoid(center, [0.035, 0.035, 0.045], C.gold, index + 1, 150)
    for (let spoke = 0; spoke < 12; spoke++) {
      const a = spoke / 12 * TAU
      s.line(
        center,
        [center[0] + Math.cos(a) * 0.332, center[1] + Math.sin(a) * 0.332, 0],
        0.006,
        C.cream,
        index + 1,
        15
      )
    }
  }
  const seat: Vec3 = [-0.32, -0.22, 0], stem: Vec3 = [0.51, -0.2, 0]
  for (const [a, b] of [
    [rear, seat],
    [seat, CRANK],
    [CRANK, rear],
    [seat, stem],
    [stem, CRANK],
    [stem, front]
  ] as [Vec3, Vec3][]) {
    s.line(a, b, 0.024, C.teal)
  }
  s.line(seat, [-0.31, -0.16, 0], 0.016, C.gold)
  s.ellipsoid([-0.31, -0.15, 0], [0.18, 0.035, 0.095], C.ink, 0, 550)
  s.line(stem, [0.48, -0.015, 0], 0.017, C.gold)
  s.line([0.48, -0.015, -0.17], [0.48, -0.015, 0.17], 0.021, C.ink)
  s.ring(CRANK, 0.095, 0.017, C.gold, 3)
  s.line(
    [CRANK[0] - PEDAL_RADIUS, CRANK[1], -0.08],
    [CRANK[0] + PEDAL_RADIUS, CRANK[1], 0.08],
    0.015,
    C.gold,
    3
  )
  s.tube(
    (t) => {
      const a = t * TAU
      return [
        mix(rear[0], CRANK[0], (1 + Math.cos(a)) / 2),
        mix(rear[1], CRANK[1], (1 + Math.cos(a)) / 2) + Math.sin(a) * 0.077,
        0.06
      ]
    },
    0.006,
    C.gold,
    0,
    95,
    4
  )
  const legs = [-1, 1].map((side, index) => {
    const hip: Vec3 = [-0.3, -0.1, side * 0.18],
      foot = pedalAt(0, side),
      knee = kneeAt(hip, foot)
    s.box(foot, [0.16, 0.025, 0.07], C.gold, 4 + index)
    s.line(hip, knee, legRadius, legColor, 10 + index * 2, 30)
    s.line(knee, foot, legRadius * 0.8, legColor, 11 + index * 2, 30)
    s.ellipsoid(
      [foot[0] + 0.045, foot[1] + 0.04, foot[2]],
      [0.12, 0.035, 0.065],
      footColor,
      14 + index,
      360
    )
    return { hip, knee, foot, side }
  })
  return (t: number): Deform => {
    const wheels = [rotate(2, -t * 1.85, rear), rotate(2, -t * 1.85, front)]
    const crank = rotate(2, -t * 3, CRANK), bob = Math.sin(t * 6) * 0.012
    const posed = legs.map(({ hip, knee, foot, side }) => {
      const nextHip: Vec3 = [hip[0], hip[1] + bob, hip[2]],
        nextFoot = pedalAt(t, side),
        nextKnee = kneeAt(nextHip, nextFoot)
      return { upper: bonePose(hip, knee, nextHip, nextKnee), lower: bonePose(knee, foot, nextKnee, nextFoot), dx: nextFoot[0] - foot[0], dy: nextFoot[1] - foot[1] }
    })
    return (p, v) => {
      if (p.part === 1 || p.part === 2) wheels[p.part - 1](p, v)
      else if (p.part === 3) crank(p, v)
      else if (p.part === 4 || p.part === 5 || p.part === 14 || p.part === 15) {
        const leg = posed[p.part % 2]
        v[0] += leg.dx
        v[1] += leg.dy
      } else if (p.part >= 10 && p.part <= 13) {
        const leg = posed[Math.floor((p.part - 10) / 2)]
        if (p.part % 2) leg.lower(p, v)
        else leg.upper(p, v)
      } else if (p.part >= 20) v[1] += bob
    }
  }
}
