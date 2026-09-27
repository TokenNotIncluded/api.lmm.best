/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import {
  createCamera,
  createNetwork,
  createTrainingScene,
  PALETTE,
  projectPoint,
  rasterizeToken,
  Shape,
  type Camera,
  type CorePalette,
  type Rgb,
  type SceneSink,
} from './home-core'
import { filmLayout, type CoreFilm } from './home-core-webgl'

const css = (color: Rgb, alpha = 1) =>
  `rgba(${color.map(Math.round).join(',')},${Math.min(1, Math.max(0, alpha)).toFixed(3)})`
const FONT = 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace'

/** Canvas 2D fallback for the same training scene, drawn in emission order. */
export function createCanvasCore(
  canvas: HTMLCanvasElement,
  palette: CorePalette = PALETTE
): CoreFilm | null {
  const ctx = canvas.getContext('2d')
  if (!ctx) return null
  const scene = createTrainingScene(
    createNetwork(palette),
    rasterizeToken,
    palette
  )
  let camera: Camera | null = null
  let width = 0
  let height = 0
  let pixelRatio = 0
  let layout: ReturnType<typeof filmLayout> = 'side'
  const ground = css(palette.ground)
  const additive = (glow: boolean) => {
    ctx.globalCompositeOperation = glow ? 'lighter' : 'source-over'
  }
  const sink: SceneSink = {
    token(p, text, color, alpha, size) {
      if (!camera) return
      const q = projectPoint(camera, p)
      const [x0, y0, x1, y1] = camera.quiet
      const quiet = q.x > x0 && q.x < x1 && q.y > y0 && q.y < y1 ? 0.12 : 1
      const fade =
        (0.28 + 0.72 * Math.min(1, Math.max(0, (q.scale - 0.62) / 0.43))) *
        (1 - Math.min(1, Math.max(0, (q.scale - 1.12) / 0.33))) *
        quiet
      if (alpha * fade <= 0.01) return
      additive(false)
      ctx.font = `700 ${Math.max(6, size * camera.unit * q.scale)}px ${FONT}`
      ctx.textAlign = 'center'
      ctx.fillStyle = css(color, alpha * fade)
      ctx.fillText(text, q.x, q.y)
    },
    label(p, text, color, alpha, size, align = 0) {
      if (!camera || alpha <= 0.01) return
      const q = projectPoint(camera, p)
      additive(false)
      ctx.font = `700 ${Math.max(6, size * camera.unit * q.scale)}px ${FONT}`
      ctx.textAlign = align >= 0.5 ? 'center' : 'left'
      ctx.fillStyle = css(color, alpha)
      ctx.fillText(text, q.x, q.y)
    },
    segment(a, b, color, alpha, lineWidth, glow = false, dash = 0) {
      if (!camera || alpha <= 0.01) return
      const p = projectPoint(camera, a),
        q = projectPoint(camera, b)
      additive(glow)
      ctx.setLineDash(dash ? [dash, dash] : [])
      const stroke = (w: number, value: number) => {
        ctx.strokeStyle = css(color, value)
        ctx.lineWidth = w
        ctx.beginPath()
        ctx.moveTo(p.x, p.y)
        ctx.lineTo(q.x, q.y)
        ctx.stroke()
      }
      if (glow) stroke(lineWidth * 3.4, alpha * 0.22)
      stroke(lineWidth, alpha)
    },
    sprite(p, color, alpha, size, shape, fill = 0, glow = false) {
      if (!camera || alpha <= 0.01) return
      const q = projectPoint(camera, p)
      const r = (size * camera.unit * q.scale) / 2
      additive(glow)
      ctx.setLineDash([])
      if (shape === Shape.glow) {
        const g = ctx.createRadialGradient(q.x, q.y, 0, q.x, q.y, r)
        g.addColorStop(0, css(color, alpha))
        g.addColorStop(1, css(color, 0))
        ctx.fillStyle = g
        ctx.fillRect(q.x - r, q.y - r, r * 2, r * 2)
      } else if (shape === Shape.pixel) {
        ctx.fillStyle = css(color, alpha)
        ctx.fillRect(q.x - r * 0.84, q.y - r * 0.84, r * 1.68, r * 1.68)
      } else if (shape === Shape.frame) {
        ctx.strokeStyle = css(color, alpha)
        ctx.lineWidth = 1.5
        ctx.strokeRect(q.x - r + 0.75, q.y - r + 0.75, r * 2 - 1.5, r * 2 - 1.5)
      } else {
        const outline = () => {
          ctx.beginPath()
          if (shape === Shape.diamond) {
            ctx.moveTo(q.x, q.y - r)
            ctx.lineTo(q.x + r, q.y)
            ctx.lineTo(q.x, q.y + r)
            ctx.lineTo(q.x - r, q.y)
            ctx.closePath()
          } else ctx.arc(q.x, q.y, r * 0.84, 0, Math.PI * 2)
        }
        outline()
        ctx.fillStyle = ground
        ctx.fill()
        if (fill > 0.01) {
          ctx.fillStyle = css(color, alpha * fill * 0.82)
          ctx.fill()
        }
        ctx.strokeStyle = css(color, alpha)
        ctx.lineWidth = Math.max(1.5, r * 0.32)
        ctx.stroke()
        if (fill >= 0.3 && shape === Shape.neuron) {
          ctx.fillStyle = css(palette.ink, alpha * 0.8)
          ctx.fillRect(q.x - r * 0.24, q.y - r * 0.24, r * 0.48, r * 0.48)
        }
      }
    },
  }
  const draw: CoreFilm = (time, pointer, progress) => {
    const w = canvas.clientWidth
    const h = canvas.clientHeight
    if (!w || !h) return
    const ratio = Math.min(window.devicePixelRatio || 1, 1.5, 1920 / w)
    if (w !== width || h !== height || ratio !== pixelRatio) {
      pixelRatio = ratio
      width = w
      height = h
      canvas.width = Math.round(w * ratio)
      canvas.height = Math.round(h * ratio)
      layout = filmLayout(canvas)
    }
    ctx.setTransform(ratio, 0, 0, ratio, 0, 0)
    ctx.clearRect(0, 0, w, h)
    ctx.textBaseline = 'middle'
    ctx.lineCap = 'round'
    camera = createCamera(time, pointer, progress, w, h, layout)
    scene(sink, time)
    additive(false)
    canvas.parentElement?.setAttribute('data-rendered', '')
  }
  draw.setToken = scene.setToken
  return draw
}
