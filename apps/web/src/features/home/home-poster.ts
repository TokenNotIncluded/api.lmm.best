/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
/** Original point-sampled sculpture. No reference-video images are shipped. */
type Point = {
  x: number
  y: number
  z: number
  closed?: [number, number, number]
  color: [number, number, number]
  pixel?: number
  fringe?: number
  corner?: number
  coverage?: number
}
type Pointer = {
  x: number
  y: number
  vx: number
  vy: number
  previousX: number
  previousY: number
  active: boolean
}
const TAU = Math.PI * 2
const lerp = (a: number, b: number, t: number) => a + (b - a) * t
const bloom = (p: Point, key: 'x' | 'y' | 'z', slot: number, open: number) =>
  p.closed ? lerp(p.closed[slot], p[key], open) : p[key]
const pack = (r: number, g: number, b: number) =>
  (255 << 24) |
  (Math.round(b * 0.86 + 0.98) << 16) |
  (Math.round(g * 0.86 + 0.98) << 8) |
  Math.round(r * 0.86 + 0.98)
const coveragePixel = (pixel: number, alpha: number) =>
  (255 << 24) |
  (Math.round(((pixel >>> 16) & 255) * alpha + 7 * (1 - alpha)) << 16) |
  (Math.round(((pixel >>> 8) & 255) * alpha + 7 * (1 - alpha)) << 8) |
  Math.round((pixel & 255) * alpha + 7 * (1 - alpha))
const hash = (n: number) => {
  const v = Math.sin(n * 127.1 + 311.7) * 43758.5453
  return v - Math.floor(v)
}

function shop(): Point[] {
  const points: Point[] = []
  const plane = (
    x: number,
    y: number,
    z: number,
    width: number,
    height: number,
    depth: number,
    color: Point['color']
  ) => {
    const rows = Math.max(2, Math.round(height * 65)),
      columns = Math.max(2, Math.round(width * 65))
    for (let row = 0; row < rows; row++) {
      for (let col = 0; col < columns; col++) {
        const u = col / (columns - 1),
          v = row / (rows - 1)
        points.push({
          x: x + u * width,
          y: y + v * height,
          z: z + u * depth,
          color,
        })
      }
    }
  }
  plane(-0.94, -0.87, 0.38, 1.88, 1.3, 0, [83, 169, 162])
  plane(0.94, -0.87, -0.5, 0.01, 1.3, 0.88, [39, 99, 96])
  for (let bay = 0; bay < 3; bay++) {
    plane(-0.82 + bay * 0.56, -0.7, 0.43, 0.42, 0.91, 0, [19, 44, 43])
    plane(-0.82 + bay * 0.56, -0.7, 0.46, 0.42, 0.06, 0, [164, 204, 181])
    for (let row = 0; row < 3; row++) {
      plane(-0.76 + bay * 0.56, -0.6 + row * 0.28, 0.47, 0.29, 0.14, 0, [
        82 + row * 24,
        118 + row * 20,
        85 + bay * 20,
      ])
    }
  }
  for (let stripe = 0; stripe < 14; stripe++) {
    plane(
      -1.04 + stripe * 0.15,
      0.43,
      -0.48,
      0.15,
      0.045,
      1.03,
      stripe % 2 ? [230, 181, 145] : [96, 162, 150]
    )
    plane(
      -1.04 + stripe * 0.15,
      0.27,
      0.55,
      0.15,
      0.2,
      0,
      stripe % 2 ? [230, 181, 145] : [96, 162, 150]
    )
  }
  plane(-1.15, -0.94, -0.58, 2.3, 0.05, 1.25, [66, 83, 77])
  return points
}

function tool(): Point[] {
  const points: Point[] = []
  // Machined torus, a central socket and twelve connector fins.
  for (let ring = 0; ring < 180; ring++) {
    for (let tube = 0; tube < 42; tube++) {
      const a = (ring / 180) * TAU,
        b = (tube / 42) * TAU,
        radius = 0.69 + Math.cos(b) * 0.24
      const lit = 0.56 + Math.max(0, Math.sin(b) * 0.3 + Math.cos(a) * 0.25)
      points.push({
        x: Math.cos(a) * radius,
        y: Math.sin(a) * radius,
        z: Math.sin(b) * 0.24,
        color: [170 * lit, 165 * lit, 222 * lit],
      })
    }
  }
  for (let fin = 0; fin < 12; fin++) {
    for (let row = 0; row < 42; row++) {
      for (let side = 0; side < 12; side++) {
        const a = (fin / 12) * TAU,
          r = 0.92 + (row / 41) * 0.38,
          w = (side / 11 - 0.5) * 0.16
        points.push({
          x: Math.cos(a) * r - Math.sin(a) * w,
          y: Math.sin(a) * r + Math.cos(a) * w,
          z: (side % 2 ? -1 : 1) * 0.09,
          color: [170 + side * 5, 145 + side * 5, 218 + side * 3],
        })
      }
    }
  }
  for (let i = 0; i < 1600; i++) {
    const a = i * 2.399963,
      r = Math.sqrt(i / 1600) * 0.32
    points.push({
      x: Math.cos(a) * r,
      y: Math.sin(a) * r,
      z: 0.13,
      color: [63, 70, 111],
    })
  }
  return points
}

function planet(): Point[] {
  const points: Point[] = []
  for (let lat = 1; lat < 100; lat++) {
    const v = (lat / 100) * Math.PI,
      count = Math.round(Math.sin(v) * 290)
    for (let lon = 0; lon < count; lon++) {
      const u = (lon / count) * TAU,
        field =
          Math.sin(u * 3 + Math.sin(v * 4)) + Math.cos(u * 5 - v * 3) * 0.45
      const land = field > 0.45,
        brightness = 0.34 + Math.max(0, Math.sin(u) * 0.6 + Math.cos(v) * 0.3)
      points.push({
        x: Math.sin(v) * Math.cos(u),
        y: Math.cos(v),
        z: Math.sin(v) * Math.sin(u),
        color: land
          ? [106 * brightness, 197 * brightness, 139 * brightness]
          : [52 * brightness, 109 * brightness, 165 * brightness],
      })
    }
  }
  for (let i = 0; i < 1700; i++) {
    const a = (i / 1700) * TAU
    points.push({
      x: Math.cos(a) * 1.44,
      y: Math.sin(a) * 0.38,
      z: Math.sin(a) * 1.28,
      color: [121, 160, 166],
    })
  }
  return points
}

export const homeLotusUrl = new URL('./assets/lotus.webp', import.meta.url).href
let sculptures: Point[][] | undefined
let imageStarted = false
const readyListeners = new Set<() => void>()
function loadLotus(models: Point[][]) {
  if (imageStarted) return
  imageStarted = true
  const image = document.createElement('img')
  image.onload = () => {
    const surface = document.createElement('canvas')
    surface.width = image.naturalWidth
    surface.height = image.naturalHeight
    const context = surface.getContext('2d', { willReadFrequently: true })
    if (!context) return
    try {
      context.drawImage(image, 0, 0)
      const { data } = context.getImageData(0, 0, surface.width, surface.height)
      const points: Point[] = []
      for (let row = 0; row < surface.height; row += 4) {
        for (let column = 0; column < surface.width; column += 4) {
          const index = (row * surface.width + column) * 4
          if (data[index + 3] < 155) continue
          const x = (column / surface.width - 0.5) * 2.7
          const y = (0.36 - row / surface.height) * 3.6
          const red = data[index],
            green = data[index + 1],
            blue = data[index + 2]
          const petal = red > green * 1.15 && row < surface.height * 0.6
          points.push({
            x,
            y,
            z: 0,
            color: [red, green, blue],
            pixel: pack(red, green, blue),
            closed: petal
              ? [x * 0.34, y * 0.72 + Math.abs(x) * 0.35 + 0.16, 0]
              : undefined,
          })
        }
      }
      if (points.length) {
        models[0] = points
        for (const listener of readyListeners) listener()
      }
    } catch {
      // A failed image decode keeps the locally packaged static cutout visible.
    }
  }
  image.src = homeLotusUrl
}
export function createHomePoster(
  canvas: HTMLCanvasElement,
  onReady = () => {}
) {
  const context = canvas.getContext('2d', { alpha: false })
  if (!context) return null
  const models = (sculptures ??= [[], shop(), tool(), planet()])
  let alive = true
  const loaded = () => {
    if (!alive) return
    canvas.dataset.ready = 'true'
    onReady()
  }
  readyListeners.add(loaded)
  if (models[0].length) queueMicrotask(loaded)
  else loadLotus(models)
  // More fine points, not oversized dots, carry the desktop petal detail.
  const count = canvas.clientWidth < 680 ? 22000 : 60000
  const offsets = new Float32Array(count * 4)
  const scatter = Float32Array.from(
    { length: count * 2 },
    (_, i) => hash(i) - 0.5
  )
  let bitmap: ImageData | undefined
  let pixels: Uint32Array | undefined
  let width = 0,
    height = 0,
    ratio = 1
  const resize = (w: number, h: number) => {
    ratio = Math.min(window.devicePixelRatio || 1, 1.5)
    width = w
    height = h
    canvas.width = Math.round(w * ratio)
    canvas.height = Math.round(h * ratio)
    bitmap = undefined
    pixels = undefined
  }
  return {
    draw(
      progress: number,
      clock: number,
      pointer: Pointer,
      delta: number,
      still = false
    ) {
      const w = canvas.clientWidth,
        h = canvas.clientHeight
      if (!w || !h) return
      if (width !== w || height !== h) resize(w, h)
      context.setTransform(ratio, 0, 0, ratio, 0, 0)
      context.fillStyle = '#070707'
      context.fillRect(0, 0, w, h)
      const chapter = Math.min(4, Math.max(0, progress))
      const from = Math.floor(chapter),
        to = Math.min(4, from + 1)
      const fraction = chapter - from,
        mix = fraction * fraction * (3 - 2 * fraction)
      const open = still ? 0.91 : 0.67 + Math.sin((clock * TAU) / 5.6) * 0.31
      const scale = Math.min(w * 0.34, h * 0.35)
      const pose = (index: number) =>
        index === 0 || index === 4 ? 0 : index === 2 ? 0.13 : 0.68
      const pitch = lerp(pose(from), pose(to), mix)
      const yaw = lerp(
        from === 0 || from === 4 ? 0 : 0.16,
        to === 0 || to === 4 ? 0 : 0.16,
        mix
      )
      const cy = Math.cos(yaw),
        sy = Math.sin(yaw),
        cp = Math.cos(pitch),
        sp = Math.sin(pitch)
      const centerX = w / 2,
        centerY = h * 0.43
      const grid = w < 680 ? 1.7 : 2.7
      const seconds = Math.min(0.06, Math.max(0.001, delta))
      const first = models[from % 4],
        second = models[to % 4]
      if (!first.length) return
      const next = second.length ? second : first
      bitmap ??= context.createImageData(canvas.width, canvas.height)
      pixels ??= new Uint32Array(bitmap.data.buffer)
      pixels.fill(0xff070707)
      const stride = canvas.width,
        rows = canvas.height
      const diameter = grid * 0.76 * ratio
      const fullSize = Math.floor(diameter)
      // Preserve subpixel coverage: 1.29px mobile dots must not collapse to 1px.
      const fringe = diameter - fullSize > 0.1 ? diameter - fullSize : 0
      const size = Math.max(1, fullSize + (fringe ? 1 : 0))
      const n = mix < 0.001 ? Math.min(count, first.length) : count
      const brushX = pointer.x - pointer.previousX,
        brushY = pointer.y - pointer.previousY
      const segment = brushX * brushX + brushY * brushY
      const speed = Math.min(1600, Math.hypot(pointer.vx, pointer.vy))
      const radius = Math.min(w, h) * (0.22 + speed / 22000)
      const recovery = Math.exp(-20 * seconds)
      for (let i = 0; i < n; i++) {
        // Ordered samples preserve the petals' occlusion; don't randomise the flower's layers.
        const a = first[Math.floor((i / n) * first.length)],
          b = next[Math.floor((i / n) * next.length)]
        const x = lerp(bloom(a, 'x', 0, open), bloom(b, 'x', 0, open), mix)
        const y = lerp(bloom(a, 'y', 1, open), bloom(b, 'y', 1, open), mix)
        const z = lerp(bloom(a, 'z', 2, open), bloom(b, 'z', 2, open), mix)
        const rx = x * cy + z * sy,
          rz = z * cy - x * sy
        const ry = y * cp + rz * sp,
          depth = rz * cp - y * sp
        const perspective = 3.9 / (3.9 - depth * 0.42)
        // Stable screen-space halftone lattice; only the brush breaks its grid.
        const px =
            Math.round((centerX + rx * scale * perspective) / grid) * grid,
          py = Math.round((centerY - ry * scale * perspective) / grid) * grid
        const index = i * 4
        let fx = 0,
          fy = 0
        if (pointer.active && !still) {
          // Sweep the previous/current brush segment; fast motion cannot skip points.
          const along = segment
            ? Math.max(
                0,
                Math.min(
                  1,
                  ((px - pointer.previousX) * brushX +
                    (py - pointer.previousY) * brushY) /
                    segment
                )
              )
            : 1
          const dx = px - (pointer.previousX + brushX * along),
            dy = py - (pointer.previousY + brushY * along)
          const distance = Math.hypot(dx, dy)
          if (distance < radius) {
            const force = Math.pow(1 - distance / radius, 2)
            // Advection carries petals with the sweep, rather than only repelling.
            fx =
              force *
              (pointer.vx * 0.16 +
                dx * 0.25 +
                scatter[i * 2] * (35 + speed * 0.06))
            fy =
              force *
              (pointer.vy * 0.07 +
                dy * 0.35 +
                scatter[i * 2 + 1] * (30 + speed * 0.09))
          }
        }
        // Closed-form critical damping makes return duration independent of fps.
        const gapX = offsets[index] - fx,
          gapY = offsets[index + 1] - fy
        const springX = offsets[index + 2] + 20 * gapX
        const springY = offsets[index + 3] + 20 * gapY
        offsets[index] = fx + (gapX + springX * seconds) * recovery
        offsets[index + 1] = fy + (gapY + springY * seconds) * recovery
        offsets[index + 2] =
          (offsets[index + 2] - 20 * springX * seconds) * recovery
        offsets[index + 3] =
          (offsets[index + 3] - 20 * springY * seconds) * recovery
        const pixel =
          mix < 0.001
            ? (a.pixel ??= pack(...a.color))
            : pack(
                lerp(a.color[0], b.color[0], mix),
                lerp(a.color[1], b.color[1], mix),
                lerp(a.color[2], b.color[2], mix)
              )
        let edgePixel = pixel,
          cornerPixel = pixel
        if (fringe) {
          if (mix < 0.001 && a.coverage === fringe) {
            edgePixel = a.fringe as number
            cornerPixel = a.corner as number
          } else {
            edgePixel = coveragePixel(pixel, fringe)
            cornerPixel = coveragePixel(pixel, fringe * fringe)
            if (mix < 0.001) {
              a.fringe = edgePixel
              a.corner = cornerPixel
              a.coverage = fringe
            }
          }
        }
        const ox = offsets[index],
          oy = offsets[index + 1]
        const dx = Math.round((px + ox) * ratio),
          dy = Math.round((py + oy) * ratio)
        for (
          let row = Math.max(0, dy);
          row < Math.min(rows, dy + size);
          row++
        ) {
          for (
            let col = Math.max(0, dx);
            col < Math.min(stride, dx + size);
            col++
          ) {
            pixels[row * stride + col] = fringe
              ? row - dy === fullSize && col - dx === fullSize
                ? cornerPixel
                : row - dy === fullSize || col - dx === fullSize
                  ? edgePixel
                  : pixel
              : pixel
          }
        }
        if (Math.abs(ox) > 2) {
          const trail = pack(
            a.color[0] * 0.12,
            a.color[1] * 0.12,
            a.color[2] * 0.12
          )
          const ty = Math.round((py + oy * 0.75) * ratio)
          const tx = Math.round((px + ox * 0.62) * ratio)
          const length = Math.round(
            (Math.min(55, Math.abs(ox) * 0.36) + grid) * ratio
          )
          if (ty >= 0 && ty < rows) {
            for (
              let col = Math.max(0, tx);
              col < Math.min(stride, tx + length);
              col++
            ) {
              const slot = ty * stride + col
              if (pixels[slot] === 0xff070707) pixels[slot] = trail
            }
          }
        }
      }
      // One bitmap upload replaces tens of thousands of Canvas material/draw calls.
      context.putImageData(bitmap, 0, 0)
      context.globalAlpha = 1
    },
    dispose() {
      alive = false
      readyListeners.delete(loaded)
      delete canvas.dataset.ready
      canvas.width = 0
      canvas.height = 0
    },
  }
}
