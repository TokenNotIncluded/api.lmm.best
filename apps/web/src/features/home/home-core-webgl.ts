/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import {
  createCamera,
  createNetwork,
  createTrainingScene,
  PALETTE,
  PIVOT,
  projectPoint,
  rasterizeToken,
  SCENE_TEXT,
  type Camera,
  type CorePalette,
  type CorePoint,
  type Rgb,
  type SceneSink,
} from './home-core'

export type CoreFilm = ((
  time: number,
  pointer: { x: number; y: number },
  progress: number
) => void) & { dispose?: () => void; setToken?: (text: string) => string }

const PRECISION = `
  #ifdef GL_FRAGMENT_PRECISION_HIGH
  precision highp float;
  #else
  precision mediump float;
  #endif`

/** Shared camera: identical to projectPoint() in home-core.ts. */
const PLACE = `
  uniform mat3 uRotation; uniform vec3 uPivot; uniform float uExpand;
  uniform float uDistance; uniform vec2 uCenter; uniform vec2 uView; uniform float uUnit;
  vec3 place(vec3 p) {
    vec3 q = uRotation * (vec3(p.xy, p.z * uExpand) - uPivot);
    float s = uDistance / max(uDistance - q.z, 0.5);
    return vec3(uCenter + vec2(q.x, -q.y) * uUnit * s, s);
  }
  vec4 clip(vec2 px) {
    return vec4(px.x / uView.x * 2.0 - 1.0, 1.0 - px.y / uView.y * 2.0, 0.0, 1.0);
  }`

/** Screen-space quads, so wires keep a constant pixel width with anti-aliased edges. */
const SEGMENT_VERTEX = `${PLACE}
  attribute vec3 aA; attribute vec3 aB; attribute vec2 aEnd; attribute vec4 aColor; attribute vec2 aStyle;
  varying vec4 vColor; varying float vSide; varying float vWidth; varying float vAlong; varying float vDash;
  void main() {
    vec3 a = place(aA); vec3 b = place(aB);
    vec2 d = b.xy - a.xy; float len = length(d);
    vec2 dir = len > 0.001 ? d / len : vec2(1.0, 0.0);
    float extent = aStyle.x * 0.5 + 1.0;
    vec2 px = mix(a.xy, b.xy, aEnd.x) + vec2(-dir.y, dir.x) * aEnd.y * extent;
    vSide = aEnd.y * extent; vWidth = aStyle.x * 0.5; vAlong = aEnd.x * len;
    vDash = aStyle.y; vColor = aColor;
    gl_Position = clip(px);
  }`
const SEGMENT_FRAGMENT = `${PRECISION}
  varying vec4 vColor; varying float vSide; varying float vWidth; varying float vAlong; varying float vDash;
  void main() {
    float cover = clamp(vWidth + 0.5 - abs(vSide), 0.0, 1.0);
    if (vDash > 0.0 && fract(vAlong / (vDash * 2.0)) > 0.5) cover = 0.0;
    float a = vColor.a * cover;
    gl_FragColor = vec4(vColor.rgb * a, a);
  }`

/** Point sprites quantized to a coarse grid, which gives neurons their pixel-art rings. */
const SPRITE_VERTEX = `${PLACE}
  attribute vec3 aPosition; attribute vec4 aColor; attribute vec3 aStyle;
  uniform float uPixelRatio; uniform float uMaxPoint;
  varying vec4 vColor; varying float vShape; varying float vFill; varying float vCells;
  void main() {
    vec3 p = place(aPosition);
    float css = aStyle.x * uUnit * p.z;
    gl_Position = clip(p.xy);
    gl_PointSize = clamp(css * uPixelRatio, 1.0, uMaxPoint);
    vColor = aColor; vShape = aStyle.y; vFill = aStyle.z;
    vCells = max(5.0, floor(css / 2.0));
  }`
const SPRITE_FRAGMENT = `${PRECISION}
  uniform vec3 uGround;
  varying vec4 vColor; varying float vShape; varying float vFill; varying float vCells;
  void main() {
    vec2 uv = gl_PointCoord - 0.5;
    vec2 q = (floor(gl_PointCoord * vCells) + 0.5) / vCells - 0.5;
    vec3 rgb = vColor.rgb;
    float a = 1.0;
    if (vShape < 0.5) {
      float d = length(q) * 2.0;
      if (d > 1.0) discard;
      vec3 inner = mix(uGround, rgb * 0.82, vFill);
      float core = step(d, 0.28) * step(0.3, vFill);
      inner = mix(inner, mix(rgb, vec3(1.0), 0.7), core);
      rgb = mix(inner, rgb, step(0.68, d));
    } else if (vShape < 1.5) {
      if (max(abs(uv.x), abs(uv.y)) > 0.42) discard;
    } else if (vShape < 2.5) {
      if (max(abs(q.x), abs(q.y)) < 0.5 - 1.5 / vCells) discard;
    } else if (vShape < 3.5) {
      float d = (abs(q.x) + abs(q.y)) * 2.0;
      if (d > 1.0) discard;
      rgb = mix(mix(uGround, rgb, vFill), rgb, step(0.7, d));
    } else {
      float d = length(uv) * 2.0;
      a = exp(-d * d * 5.0) * (1.0 - smoothstep(0.75, 1.0, d));
    }
    a *= vColor.a;
    gl_FragColor = vec4(rgb * a, a);
  }`

/** Text is sampled from a glyph atlas and thresholded into hard, pixel-like edges. */
const TEXT_VERTEX = `${PLACE}
  attribute vec3 aPosition; attribute vec2 aCorner; attribute vec2 aUv; attribute vec4 aColor; attribute vec2 aStyle;
  uniform vec4 uQuiet;
  varying vec2 vUv; varying vec4 vColor;
  void main() {
    vec3 p = place(aPosition);
    float fade = 1.0;
    if (aStyle.y > 0.5) {
      fade = mix(0.28, 1.0, smoothstep(0.62, 1.05, p.z)) * (1.0 - smoothstep(1.12, 1.45, p.z));
      vec2 inside = smoothstep(uQuiet.xy - 60.0, uQuiet.xy + 20.0, p.xy)
        * (1.0 - smoothstep(uQuiet.zw - 20.0, uQuiet.zw + 60.0, p.xy));
      fade *= 1.0 - 0.88 * inside.x * inside.y;
    }
    vColor = vec4(aColor.rgb, aColor.a * fade); vUv = aUv;
    gl_Position = clip(p.xy + aCorner * aStyle.x * uUnit * p.z);
  }`
const TEXT_FRAGMENT = `${PRECISION}
  uniform sampler2D uAtlas;
  varying vec2 vUv; varying vec4 vColor;
  void main() {
    float a = smoothstep(0.38, 0.56, texture2D(uAtlas, vUv).a) * vColor.a;
    if (a <= 0.0) discard;
    gl_FragColor = vec4(vColor.rgb * a, a);
  }`

const FONT_PX = 22
const ATLAS = { width: 1024, height: 256, line: 30 }
type Glyph = {
  u0: number
  v0: number
  u1: number
  v1: number
  w: number
  h: number
}

function createAtlas() {
  const canvas = document.createElement('canvas')
  canvas.width = ATLAS.width
  canvas.height = ATLAS.height
  const ctx = canvas.getContext('2d')
  if (!ctx || typeof ctx.fillText !== 'function') return null
  ctx.font = `700 ${FONT_PX}px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace`
  ctx.textBaseline = 'middle'
  ctx.fillStyle = '#fff'
  const glyphs = new Map<string, Glyph>()
  let x = 2,
    y = 0
  for (const text of SCENE_TEXT) {
    const width = Math.ceil(ctx.measureText(text).width) + 2
    if (x + width > ATLAS.width) {
      x = 2
      y += ATLAS.line
    }
    ctx.fillText(text, x + 1, y + ATLAS.line / 2)
    glyphs.set(text, {
      u0: x / ATLAS.width,
      v0: y / ATLAS.height,
      u1: (x + width) / ATLAS.width,
      v1: (y + ATLAS.line) / ATLAS.height,
      w: width / FONT_PX,
      h: ATLAS.line / FONT_PX,
    })
    x += width + 2
  }
  return { canvas, glyphs }
}

/** Stacked (static) layouts centre the network; see --film-layout in forge-home.css. */
export function filmLayout(canvas: HTMLCanvasElement): 'side' | 'center' {
  try {
    const value = window
      .getComputedStyle(canvas)
      .getPropertyValue('--film-layout')
    return value.trim() === 'center' ? 'center' : 'side'
  } catch {
    return 'side'
  }
}

type Batch = { data: Float32Array; length: number }
const batch = (): Batch => ({ data: new Float32Array(8192), length: 0 })
function write(target: Batch, values: number[]) {
  if (target.length + values.length > target.data.length) {
    const grown = new Float32Array(
      Math.max(target.data.length * 2, target.length + values.length)
    )
    grown.set(target.data.subarray(0, target.length))
    target.data = grown
  }
  target.data.set(values, target.length)
  target.length += values.length
}

const QUAD = [
  [0, -1],
  [1, -1],
  [1, 1],
  [0, -1],
  [1, 1],
  [0, 1],
] as const
const CORNERS = [
  [0, 0],
  [1, 0],
  [1, 1],
  [0, 0],
  [1, 1],
  [0, 1],
] as const

/** Local network renderer: wiring, activations, gradients and a token cloud. */
export function createWebGLCore(
  canvas: HTMLCanvasElement,
  palette: CorePalette = PALETTE
): CoreFilm | null {
  let gl: WebGLRenderingContext | null
  try {
    gl = canvas.getContext('webgl', {
      alpha: true,
      antialias: false,
      premultipliedAlpha: true,
      powerPreference: 'low-power',
    })
  } catch {
    return null
  }
  if (!gl || typeof gl.createShader !== 'function') return null
  const shaders: WebGLShader[] = []
  const programs: WebGLProgram[] = []
  const buffers: WebGLBuffer[] = []
  const textures: WebGLTexture[] = []
  const release = () => {
    for (const item of shaders) gl.deleteShader(item)
    for (const item of programs) gl.deleteProgram(item)
    for (const item of buffers) gl.deleteBuffer(item)
    for (const item of textures) gl.deleteTexture(item)
  }
  const shader = (type: number, source: string) => {
    const value = gl.createShader(type)
    if (!value) throw new Error('Shader allocation failed')
    shaders.push(value)
    gl.shaderSource(value, source)
    gl.compileShader(value)
    if (!gl.getShaderParameter(value, gl.COMPILE_STATUS)) {
      throw new Error('Shader compilation failed')
    }
    return value
  }
  const link = (
    vertex: string,
    fragment: string,
    attributes: [name: string, size: number][]
  ) => {
    const program = gl.createProgram()
    if (!program) throw new Error('Program allocation failed')
    programs.push(program)
    gl.attachShader(program, shader(gl.VERTEX_SHADER, vertex))
    gl.attachShader(program, shader(gl.FRAGMENT_SHADER, fragment))
    gl.linkProgram(program)
    if (!gl.getProgramParameter(program, gl.LINK_STATUS)) {
      throw new Error('Program linking failed')
    }
    let offset = 0
    const layout = attributes.map(([name, size]) => {
      const entry = {
        location: gl.getAttribLocation(program, name),
        size,
        offset,
      }
      offset += size
      return entry
    })
    const uniforms = new Map<string, WebGLUniformLocation | null>()
    const uniform = (name: string) => {
      if (!uniforms.has(name)) {
        uniforms.set(name, gl.getUniformLocation(program, name))
      }
      return uniforms.get(name) ?? null
    }
    return { program, layout, stride: offset, uniform }
  }
  type Program = ReturnType<typeof link>

  try {
    const segments = link(SEGMENT_VERTEX, SEGMENT_FRAGMENT, [
      ['aA', 3],
      ['aB', 3],
      ['aEnd', 2],
      ['aColor', 4],
      ['aStyle', 2],
    ])
    const sprites = link(SPRITE_VERTEX, SPRITE_FRAGMENT, [
      ['aPosition', 3],
      ['aColor', 4],
      ['aStyle', 3],
    ])
    const text = link(TEXT_VERTEX, TEXT_FRAGMENT, [
      ['aPosition', 3],
      ['aCorner', 2],
      ['aUv', 2],
      ['aColor', 4],
      ['aStyle', 2],
    ])
    const buffer = gl.createBuffer()
    if (!buffer) throw new Error('Buffer allocation failed')
    buffers.push(buffer)
    const atlas = createAtlas()
    const texture = atlas ? gl.createTexture() : null
    if (atlas && texture) {
      textures.push(texture)
      gl.bindTexture(gl.TEXTURE_2D, texture)
      gl.texImage2D(
        gl.TEXTURE_2D,
        0,
        gl.RGBA,
        gl.RGBA,
        gl.UNSIGNED_BYTE,
        atlas.canvas
      )
      for (const [key, value] of [
        [gl.TEXTURE_MIN_FILTER, gl.LINEAR],
        [gl.TEXTURE_MAG_FILTER, gl.LINEAR],
        [gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE],
        [gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE],
      ]) {
        gl.texParameteri(gl.TEXTURE_2D, key, value)
      }
    }
    const pointRange = gl.getParameter(
      gl.ALIASED_POINT_SIZE_RANGE
    ) as Float32Array | null
    const maxPoint = pointRange?.[1] ?? 64
    const scene = createTrainingScene(
      createNetwork(palette),
      rasterizeToken,
      palette
    )

    const tokens = batch(),
      lines = batch(),
      glowLines = batch(),
      dots = batch(),
      glowDots = batch(),
      labels = batch()
    type Sprite = [
      p: CorePoint,
      color: Rgb,
      alpha: number,
      size: number,
      shape: number,
      fill: number,
      depth: number,
    ]
    let pending: Sprite[] = []
    let camera: Camera | null = null
    const rgba = (color: Rgb, alpha: number) => [
      color[0] / 255,
      color[1] / 255,
      color[2] / 255,
      Math.min(1, Math.max(0, alpha)),
    ]
    const quad = (
      target: Batch,
      a: CorePoint,
      b: CorePoint,
      color: number[],
      width: number,
      dash: number
    ) => {
      for (const [t, side] of QUAD) {
        write(target, [
          a.x,
          a.y,
          a.z,
          b.x,
          b.y,
          b.z,
          t,
          side,
          ...color,
          width,
          dash,
        ])
      }
    }
    const glyph = (
      target: Batch,
      p: CorePoint,
      value: string,
      color: Rgb,
      alpha: number,
      size: number,
      align: number,
      fade: number
    ) => {
      const g = atlas?.glyphs.get(value)
      if (!g || alpha <= 0.01) return
      const c = rgba(color, alpha)
      for (const [cx, cy] of CORNERS) {
        write(target, [
          p.x,
          p.y,
          p.z,
          (cx - align) * g.w,
          (cy - 0.5) * g.h,
          g.u0 + (g.u1 - g.u0) * cx,
          g.v0 + (g.v1 - g.v0) * cy,
          ...c,
          size,
          fade,
        ])
      }
    }
    const sink: SceneSink = {
      token: (p, value, color, alpha, size) =>
        glyph(tokens, p, value, color, alpha, size, 0.5, 1),
      label: (p, value, color, alpha, size, align = 0) =>
        glyph(labels, p, value, color, alpha, size, align, 0),
      segment(a, b, color, alpha, width, glow = false, dash = 0) {
        if (alpha <= 0.01) return
        if (!glow) return quad(lines, a, b, rgba(color, alpha), width, dash)
        quad(glowLines, a, b, rgba(color, alpha * 0.22), width * 3.4, dash)
        quad(glowLines, a, b, rgba(color, alpha), width, dash)
      },
      sprite(p, color, alpha, size, shape, fill = 0, glow = false) {
        if (alpha <= 0.01) return
        if (glow) {
          write(glowDots, [
            p.x,
            p.y,
            p.z,
            ...rgba(color, alpha),
            size,
            shape,
            fill,
          ])
          return
        }
        pending.push([
          p,
          color,
          alpha,
          size,
          shape,
          fill,
          camera ? projectPoint(camera, p).depth : 0,
        ])
      },
    }

    let layout = filmLayout(canvas)
    let contextLost = false
    const lost = (event: Event) => {
      event.preventDefault()
      contextLost = true
      canvas.parentElement?.removeAttribute('data-rendered')
    }
    canvas.addEventListener('webglcontextlost', lost)
    const flush = (
      program: Program,
      data: Batch,
      mode: number,
      additive: boolean,
      w: number,
      h: number,
      dpr: number
    ) => {
      if (!data.length || !camera) return
      gl.useProgram(program.program)
      gl.uniformMatrix3fv(program.uniform('uRotation'), false, camera.rotation)
      gl.uniform3f(program.uniform('uPivot'), PIVOT.x, PIVOT.y, PIVOT.z)
      gl.uniform1f(program.uniform('uExpand'), camera.expand)
      gl.uniform1f(program.uniform('uDistance'), camera.distance)
      gl.uniform2f(program.uniform('uCenter'), camera.cx, camera.cy)
      gl.uniform2f(program.uniform('uView'), w, h)
      gl.uniform1f(program.uniform('uUnit'), camera.unit)
      gl.uniform1f(program.uniform('uPixelRatio'), dpr)
      gl.uniform1f(program.uniform('uMaxPoint'), maxPoint)
      gl.uniform3f(
        program.uniform('uGround'),
        palette.ground[0] / 255,
        palette.ground[1] / 255,
        palette.ground[2] / 255
      )
      gl.uniform1i(program.uniform('uAtlas'), 0)
      gl.uniform4fv(program.uniform('uQuiet'), camera.quiet)
      gl.blendFunc(gl.ONE, additive ? gl.ONE : gl.ONE_MINUS_SRC_ALPHA)
      gl.bindBuffer(gl.ARRAY_BUFFER, buffer)
      gl.bufferData(
        gl.ARRAY_BUFFER,
        data.data.subarray(0, data.length),
        gl.STREAM_DRAW
      )
      for (const entry of program.layout) {
        if (entry.location < 0) continue
        gl.enableVertexAttribArray(entry.location)
        gl.vertexAttribPointer(
          entry.location,
          entry.size,
          gl.FLOAT,
          false,
          program.stride * 4,
          entry.offset * 4
        )
      }
      gl.drawArrays(mode, 0, data.length / program.stride)
      for (const entry of program.layout) {
        if (entry.location >= 0) gl.disableVertexAttribArray(entry.location)
      }
    }

    const draw: CoreFilm = (time, pointer, progress) => {
      if (contextLost) return
      const w = canvas.clientWidth,
        h = canvas.clientHeight
      if (!w || !h) return
      const dpr = Math.min(window.devicePixelRatio || 1, 2, 2560 / w)
      if (
        canvas.width !== Math.round(w * dpr) ||
        canvas.height !== Math.round(h * dpr)
      ) {
        canvas.width = Math.round(w * dpr)
        canvas.height = Math.round(h * dpr)
        layout = filmLayout(canvas)
      }
      camera = createCamera(time, pointer, progress, w, h, layout)
      for (const target of [tokens, lines, glowLines, dots, glowDots, labels]) {
        target.length = 0
      }
      pending = []
      scene(sink, time)
      pending.sort((a, b) => a[6] - b[6])
      for (const [p, color, alpha, size, shape, fill] of pending) {
        write(dots, [p.x, p.y, p.z, ...rgba(color, alpha), size, shape, fill])
      }
      gl.viewport(0, 0, canvas.width, canvas.height)
      gl.clearColor(0, 0, 0, 0)
      gl.clear(gl.COLOR_BUFFER_BIT)
      gl.disable(gl.DEPTH_TEST)
      gl.enable(gl.BLEND)
      if (texture) {
        gl.activeTexture(gl.TEXTURE0)
        gl.bindTexture(gl.TEXTURE_2D, texture)
      }
      flush(text, tokens, gl.TRIANGLES, false, w, h, dpr)
      flush(segments, lines, gl.TRIANGLES, false, w, h, dpr)
      flush(segments, glowLines, gl.TRIANGLES, true, w, h, dpr)
      flush(sprites, dots, gl.POINTS, false, w, h, dpr)
      flush(sprites, glowDots, gl.POINTS, true, w, h, dpr)
      flush(text, labels, gl.TRIANGLES, false, w, h, dpr)
      canvas.parentElement?.setAttribute('data-rendered', '')
    }
    draw.setToken = scene.setToken
    draw.dispose = () => {
      canvas.removeEventListener('webglcontextlost', lost)
      release()
    }
    return draw
  } catch {
    release()
    return null
  }
}
