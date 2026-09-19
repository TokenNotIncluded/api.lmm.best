/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import {
  coreExpansion,
  createCoreMesh,
  createSignalPaths,
  transformCorePoint,
  type CorePoint,
} from './home-core'

export type CoreFilm = ((
  time: number,
  pointer: { x: number; y: number },
  progress: number
) => void) & { dispose?: () => void }

/** Local tensor renderer with depth-tested matrix cells and animated data paths. */
export function createWebGLCore(canvas: HTMLCanvasElement): CoreFilm | null {
  let gl: WebGLRenderingContext | null
  try {
    gl = canvas.getContext('webgl', {
      alpha: true,
      antialias: true,
      premultipliedAlpha: false,
      powerPreference: 'low-power',
    })
  } catch {
    return null
  }
  if (!gl || typeof gl.createShader !== 'function') return null
  const shaders: WebGLShader[] = []
  const programs: WebGLProgram[] = []
  const buffers: WebGLBuffer[] = []
  const release = () => {
    for (const item of shaders) gl.deleteShader(item)
    for (const item of programs) gl.deleteProgram(item)
    for (const item of buffers) gl.deleteBuffer(item)
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
  const vertex = `
    attribute vec3 aPosition; attribute vec3 aNormal; attribute vec3 aColor;
    uniform mat3 uRotation; uniform vec2 uScale; uniform vec2 uCenter;
    uniform float uPixelRatio; uniform vec3 uExpand;
    varying vec3 vNormal; varying vec3 vColor; varying vec3 vPosition; varying float vLayer;
    void main() {
      vec3 p = uRotation * (aPosition * uExpand);
      float perspective = 7.0 / (7.0 - p.z);
      gl_Position = vec4(uCenter + p.xy * uScale * perspective, -p.z / 8.0, 1.0);
      gl_PointSize = 4.2 * uPixelRatio * perspective;
      vNormal = uRotation * (aNormal / uExpand); vColor = aColor; vPosition = p; vLayer = aPosition.y;
    }`
  const fragment = `
    precision mediump float;
    varying vec3 vNormal; varying vec3 vColor; varying vec3 vPosition; varying float vLayer;
    uniform float uPass; uniform float uFocus;
    void main() {
      if (uPass > 1.5) {
        float d = length(gl_PointCoord - 0.5) * 2.0;
        if (d > 1.0) discard;
        gl_FragColor = vec4(vec3(0.77,0.32,0.13), 1.0-smoothstep(0.25,1.0,d)); return;
      }
      if (uPass > 0.5) { gl_FragColor = vec4(vColor,0.48); return; }
      vec3 n = normalize(vNormal);
      if (!gl_FrontFacing) n = -n;
      float key = max(dot(n,normalize(vec3(-0.6,0.8,1.0))),0.0);
      float fill = max(dot(n,normalize(vec3(0.8,-0.2,0.5))),0.0);
      vec3 matte = vColor * (0.54 + key * 0.48 + fill * 0.16);
      float active = 1.0-smoothstep(0.02,0.12,abs(vLayer-uFocus));
      matte = mix(matte,vec3(0.71,0.37,0.20),active*0.65);
      gl_FragColor = vec4(matte,1.0);
    }`
  try {
    const program = gl.createProgram()
    if (!program) throw new Error('Program allocation failed')
    programs.push(program)
    gl.attachShader(program, shader(gl.VERTEX_SHADER, vertex))
    gl.attachShader(program, shader(gl.FRAGMENT_SHADER, fragment))
    gl.linkProgram(program)
    if (!gl.getProgramParameter(program, gl.LINK_STATUS)) {
      throw new Error('Program linking failed')
    }
    const buffer = gl.createBuffer()
    if (!buffer) throw new Error('Buffer allocation failed')
    buffers.push(buffer)
    const attributeNames = ['aPosition', 'aNormal', 'aColor']
    const locations = attributeNames.map((name) =>
      gl.getAttribLocation(program, name)
    )
    const rotation = gl.getUniformLocation(program, 'uRotation')
    const scale = gl.getUniformLocation(program, 'uScale')
    const center = gl.getUniformLocation(program, 'uCenter')
    const pass = gl.getUniformLocation(program, 'uPass')
    const focus = gl.getUniformLocation(program, 'uFocus')
    const pixelRatio = gl.getUniformLocation(program, 'uPixelRatio')
    const expansion = gl.getUniformLocation(program, 'uExpand')
    const mesh = createCoreMesh()
    const paths = createSignalPaths()
    let previousProgress = -1
    let triangles = new Float32Array()
    let routes = new Float32Array()
    const moved = (p: CorePoint, progress: number) =>
      transformCorePoint(p, { layer: 0, hinge: 0 }, progress)
    const pack = (
      target: number[],
      p: CorePoint,
      n: CorePoint,
      color: readonly number[]
    ) =>
      target.push(
        p.x,
        p.y,
        p.z,
        n.x,
        n.y,
        n.z,
        color[0] / 255,
        color[1] / 255,
        color[2] / 255
      )
    const rebuild = (progress: number) => {
      const data: number[] = []
      for (const face of mesh) {
        for (let i = 1; i < face.points.length - 1; i++) {
          for (const p of [
            face.points[0],
            face.points[i],
            face.points[i + 1],
          ]) {
            pack(data, p, face.normal, face.color)
          }
        }
      }
      triangles = new Float32Array(data)
      const lines: number[] = []
      for (const path of paths) {
        for (let i = 0; i < path.points.length - 1; i++) {
          for (const p of [path.points[i], path.points[i + 1]]) {
            pack(lines, moved(p, progress), { x: 0, y: 0, z: 1 }, path.color)
          }
        }
      }
      routes = new Float32Array(lines)
      previousProgress = progress
    }
    let contextLost = false
    const lost = (event: Event) => {
      event.preventDefault()
      contextLost = true
      canvas.parentElement?.removeAttribute('data-rendered')
    }
    canvas.addEventListener('webglcontextlost', lost)
    const draw: CoreFilm = (time, pointer, progress) => {
      if (contextLost) return
      const w = canvas.clientWidth,
        h = canvas.clientHeight
      if (!w || !h) return
      const dpr = Math.min(window.devicePixelRatio || 1, 1.75, 1920 / w)
      if (
        canvas.width !== Math.round(w * dpr) ||
        canvas.height !== Math.round(h * dpr)
      ) {
        canvas.width = Math.round(w * dpr)
        canvas.height = Math.round(h * dpr)
      }
      if (previousProgress < 0) rebuild(0)
      gl.viewport(0, 0, canvas.width, canvas.height)
      gl.clearColor(0, 0, 0, 0)
      gl.clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
      gl.enable(gl.DEPTH_TEST)
      gl.enable(gl.BLEND)
      gl.blendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
      gl.useProgram(program)
      gl.bindBuffer(gl.ARRAY_BUFFER, buffer)
      locations.forEach((location, index) => {
        gl.enableVertexAttribArray(location)
        gl.vertexAttribPointer(location, 3, gl.FLOAT, false, 36, index * 12)
      })
      const ry =
        -0.42 +
        Math.sin(time * 0.12) * 0.06 +
        pointer.x * 0.18 +
        Math.sin(progress * Math.PI * 2) * 0.42
      const rx = -0.12 + pointer.y * 0.08,
        rz = -0.035 + Math.sin(progress * Math.PI) * 0.05
      const rotate = (p: CorePoint) => {
        const x = p.x * Math.cos(ry) + p.z * Math.sin(ry),
          z = -p.x * Math.sin(ry) + p.z * Math.cos(ry)
        const y = p.y * Math.cos(rx) - z * Math.sin(rx)
        return [
          x * Math.cos(rz) - y * Math.sin(rz),
          x * Math.sin(rz) + y * Math.cos(rz),
          p.y * Math.sin(rx) + z * Math.cos(rx),
        ]
      }
      gl.uniformMatrix3fv(
        rotation,
        false,
        new Float32Array([
          ...rotate({ x: 1, y: 0, z: 0 }),
          ...rotate({ x: 0, y: 1, z: 0 }),
          ...rotate({ x: 0, y: 0, z: 1 }),
        ])
      )
      const narrow = w < 650,
        unit = Math.min(w * (narrow ? 0.105 : 0.1), h * (narrow ? 0.15 : 0.095))
      const spread = coreExpansion(progress)
      gl.uniform3f(
        expansion,
        1 + spread * 0.12,
        1 + spread * 0.12,
        1 + spread * 2.8
      )
      const zoom = narrow ? 1 : 1 + Math.sin(progress * Math.PI) ** 2 * 0.16
      gl.uniform2f(scale, (unit * 2 * zoom) / w, (unit * 2 * zoom) / h)
      gl.uniform2f(
        center,
        (narrow ? 0.5 : 0.255) * 2 - 1,
        1 - (narrow ? 0.255 : 0.49) * 2
      )
      gl.uniform1f(pixelRatio, dpr)
      gl.uniform1f(focus, -3.4 + ((time * 0.065 + progress * 0.7) % 1) * 6.9)
      gl.uniform1f(pass, 0)
      gl.bufferData(gl.ARRAY_BUFFER, triangles, gl.STATIC_DRAW)
      gl.drawArrays(gl.TRIANGLES, 0, triangles.length / 9)
      gl.uniform1f(pass, 1)
      gl.depthMask(false)
      gl.bufferData(gl.ARRAY_BUFFER, routes, gl.STREAM_DRAW)
      gl.drawArrays(gl.LINES, 0, routes.length / 9)
      const pulses: number[] = []
      paths.forEach((path, index) => {
        if (index % 4 !== 0) return
        const t = (time * 0.11 + index * 0.071 + progress * 0.8) % 1,
          cursor = t * (path.points.length - 1),
          i = Math.floor(cursor),
          f = cursor - i
        const a = path.points[i],
          b = path.points[Math.min(i + 1, path.points.length - 1)]
        pack(
          pulses,
          moved(
            {
              x: a.x + (b.x - a.x) * f,
              y: a.y + (b.y - a.y) * f,
              z: a.z + (b.z - a.z) * f,
            },
            0
          ),
          { x: 0, y: 0, z: 1 },
          path.color
        )
      })
      gl.uniform1f(pass, 2)
      gl.bufferData(gl.ARRAY_BUFFER, new Float32Array(pulses), gl.STREAM_DRAW)
      gl.drawArrays(gl.POINTS, 0, pulses.length / 9)
      gl.depthMask(true)
      canvas.parentElement?.setAttribute('data-rendered', '')
    }
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
