/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import {
  createCoreMesh,
  createSignalPaths,
  transformCorePoint,
  type CorePoint,
} from './home-core'
import { createWebGLCore, type CoreFilm } from './home-core-webgl'
import { createTokenCloud } from './home-token-cloud'

export function unit(value: number) {
  return Number.isFinite(value) ? Math.min(1, Math.max(0, value)) : 0
}

export function pointerPosition(
  x: number,
  y: number,
  rect: Pick<DOMRect, 'left' | 'top' | 'width' | 'height'>
) {
  return {
    x: unit((x - rect.left) / Math.max(rect.width, 1)) - 0.5,
    y: unit((y - rect.top) / Math.max(rect.height, 1)) - 0.5,
  }
}

/** All measurements are relative to the actual section, not window.scrollY. */
export function storyPosition(top: number, height: number, viewport: number) {
  return unit((viewport * 0.5 - top) / Math.max(height, 1))
}

type Point = CorePoint

export function cinemaPosition(
  top: number,
  height: number,
  frameHeight: number,
  stickyTop = 0
) {
  return unit((stickyTop - top) / Math.max(height - frameHeight, 1))
}

/** A conceptual Transformer architecture: matrices, attention branches and residual paths. */
function createFilm(canvas: HTMLCanvasElement): CoreFilm | null {
  const accelerated = createWebGLCore(canvas)
  if (accelerated) return accelerated
  const ctx = canvas.getContext('2d', { alpha: false })
  if (!ctx) return null
  const mesh = createCoreMesh()
  const signalPaths = createSignalPaths()
  let width = 0
  let height = 0
  let pixelRatio = 0
  return (
    time: number,
    pointer: { x: number; y: number },
    progress: number
  ) => {
    const w = canvas.clientWidth
    const h = canvas.clientHeight
    if (!w || !h) return
    const ratio = Math.min(window.devicePixelRatio || 1, 1.25, 1440 / w)
    if (w !== width || h !== height || ratio !== pixelRatio) {
      pixelRatio = ratio
      width = w
      height = h
      canvas.width = Math.round(w * ratio)
      canvas.height = Math.round(h * ratio)
      ctx.setTransform(ratio, 0, 0, ratio, 0, 0)
    }
    const backdrop = ctx.createLinearGradient(0, h, w, 0)
    backdrop.addColorStop(0, '#f3f0e9')
    backdrop.addColorStop(0.55, '#f3f0e9')
    backdrop.addColorStop(1, '#e8e4dc')
    ctx.fillStyle = backdrop
    ctx.fillRect(0, 0, w, h)
    const glow = ctx.createRadialGradient(
      w * 0.43,
      h * 0.4,
      0,
      w * 0.43,
      h * 0.4,
      h * 0.8
    )
    glow.addColorStop(0, '#ffffff33')
    glow.addColorStop(1, '#ffffff00')
    ctx.fillStyle = glow
    ctx.fillRect(0, 0, w, h)
    const narrow = w < 650
    const cx = w * (narrow ? 0.5 : 0.255)
    const cy = h * (narrow ? 0.255 : 0.49)
    const scale = Math.min(
      w * (narrow ? 0.105 : 0.1),
      h * (narrow ? 0.15 : 0.095)
    )
    const ry =
      -0.48 +
      Math.sin(time * 0.2) * 0.16 +
      pointer.x * 0.27 +
      Math.sin(progress * Math.PI * 2) * 0.42
    const rx =
      -0.28 +
      Math.cos(time * 0.16) * 0.035 +
      pointer.y * 0.12 -
      Math.sin(progress * Math.PI) * 0.28
    const rz = -0.16 + Math.sin(time * 0.12) * 0.025 + progress * 0.22
    const [sx, cxr, sy, cyr, sz, czr] = [
      Math.sin(rx),
      Math.cos(rx),
      Math.sin(ry),
      Math.cos(ry),
      Math.sin(rz),
      Math.cos(rz),
    ]
    const rotate = (p: Point): Point => {
      const x = p.x * cyr + p.z * sy
      const z = -p.x * sy + p.z * cyr
      const y = p.y * cxr - z * sx
      return { x: x * czr - y * sz, y: x * sz + y * czr, z: p.y * sx + z * cxr }
    }
    const shadow = ctx.createRadialGradient(
      cx,
      h * 0.88,
      0,
      cx,
      h * 0.88,
      scale * 2.5
    )
    shadow.addColorStop(0, '#020b0999')
    shadow.addColorStop(1, '#020b0900')
    ctx.save()
    ctx.translate(0, h * 0.72)
    ctx.scale(1, 0.18)
    ctx.fillStyle = shadow
    ctx.fillRect(0, -h * 3, w, h * 6)
    ctx.restore()
    const project = (p: Point) => {
      const perspective = 7 / (7 - p.z)
      return {
        x: cx + p.x * scale * perspective,
        y: cy - p.y * scale * perspective,
        perspective,
      }
    }
    // Fine attention routes stay behind the solid ribbons; no opaque full-screen effects.
    const paths = signalPaths.map((path) => ({
      color: path.color,
      points: path.points.map((p) =>
        rotate(transformCorePoint(p, { layer: 0, hinge: 0 }, progress))
      ),
    }))
    ctx.lineWidth = narrow ? 0.7 : 0.85
    for (const path of paths) {
      ctx.strokeStyle = `rgba(${path.color.join(',')},0.28)`
      ctx.beginPath()
      path.points.forEach((p, i) => {
        const q = project(p)
        if (i === 0) ctx.moveTo(q.x, q.y)
        else ctx.lineTo(q.x, q.y)
      })
      ctx.stroke()
    }
    const transformed = mesh
      .map((face) => {
        const points = face.points.map((p) =>
          rotate(transformCorePoint(p, face, progress))
        )
        return {
          ...face,
          points,
          normal: (() => {
            const a = points[0],
              b = points[1],
              c = points[2]
            const u = { x: b.x - a.x, y: b.y - a.y, z: b.z - a.z },
              v = { x: c.x - a.x, y: c.y - a.y, z: c.z - a.z }
            const n = {
              x: u.y * v.z - u.z * v.y,
              y: u.z * v.x - u.x * v.z,
              z: u.x * v.y - u.y * v.x,
            }
            const length = Math.hypot(n.x, n.y, n.z) || 1
            return { x: n.x / length, y: n.y / length, z: n.z / length }
          })(),
          z: points.reduce((sum, p) => sum + p.z, 0) / points.length,
        }
      })
      .sort((a, b) => a.z - b.z)
    for (const face of transformed) {
      const n = face.normal
      const diffuse = Math.max(0, -n.x * 0.4 + n.y * 0.5 + n.z * 0.7)
      const specular = Math.pow(
        Math.max(0, -n.x * 0.22 + n.y * 0.28 + n.z * 0.93),
        22
      )
      const luminance = 0.38 + diffuse * 0.74
      const c = (base: number, shine: number) =>
        Math.round(Math.min(255, base * luminance + specular * shine))
      ctx.fillStyle = `rgb(${c(face.color[0], 64 * face.shine)} ${c(face.color[1], 88 * face.shine)} ${c(face.color[2], 64 * face.shine)})`
      ctx.beginPath()
      face.points.forEach((p, i) => {
        const perspective = 7 / (7 - p.z)
        const x = cx + p.x * scale * perspective
        const y = cy - p.y * scale * perspective
        if (i === 0) ctx.moveTo(x, y)
        else ctx.lineTo(x, y)
      })
      ctx.closePath()
      ctx.fill()
      ctx.strokeStyle = ctx.fillStyle
      ctx.lineWidth = 0.6
      ctx.stroke()
    }
    // A bounded set of pulses makes input/attention/output continuity legible.
    paths.forEach((path, index) => {
      for (let pulse = 0; pulse < 2; pulse++) {
        const t =
          (time * 0.14 + index * 0.071 + pulse * 0.5 + progress * 0.8) % 1
        const cursor = t * (path.points.length - 1)
        const a = path.points[Math.floor(cursor)]
        const b =
          path.points[Math.min(path.points.length - 1, Math.floor(cursor) + 1)]
        const fraction = cursor % 1
        const q = project({
          x: a.x + (b.x - a.x) * fraction,
          y: a.y + (b.y - a.y) * fraction,
          z: a.z + (b.z - a.z) * fraction,
        })
        ctx.fillStyle = `rgba(${path.color.join(',')},0.12)`
        ctx.beginPath()
        ctx.arc(q.x, q.y, 5 * q.perspective, 0, Math.PI * 2)
        ctx.fill()
        ctx.fillStyle = '#fff0d3'
        ctx.beginPath()
        ctx.arc(q.x, q.y, 1.45 * q.perspective, 0, Math.PI * 2)
        ctx.fill()
      }
    })
    canvas.parentElement?.setAttribute('data-rendered', '')
  }
}

/** A single owned animation lifecycle, shared by the page and its browser tests. */
export function mountHomeMotion(root: HTMLElement) {
  const cinema = root.querySelector<HTMLElement>('[data-cinema]')
  const inner = root.querySelector<HTMLElement>('[data-cinema-inner]')
  const canvas = root.querySelector<HTMLCanvasElement>('[data-film]')
  if (!cinema || !inner || !canvas) return () => {}
  let draw = createFilm(canvas)
  const tokens = createTokenCloud(
    root.querySelector<HTMLElement>('[data-token-cloud]')
  )
  const scenePanels = [
    ...root.querySelectorAll<HTMLElement>('[data-cinema-panel]'),
  ]
  const sceneSteps = [
    ...root.querySelectorAll<HTMLElement>('[data-cinema-step]'),
  ]
  const story = root.querySelector<HTMLElement>('[data-story]')
  const steps = [...root.querySelectorAll<HTMLElement>('[data-story-step]')]
  const toggle = root.querySelector<HTMLButtonElement>('[data-motion-toggle]')
  const panels = [...root.querySelectorAll<HTMLElement>('[data-story-panel]')]
  const links = [...root.querySelectorAll<HTMLElement>('[data-step-link]')]
  if (
    typeof window.IntersectionObserver !== 'function' ||
    typeof window.ResizeObserver !== 'function'
  ) {
    draw?.(0, { x: 0, y: 0 }, 0)
    tokens?.measure(inner.getBoundingClientRect(), scenePanels)
    tokens?.draw(0, null, false)
    root.dataset.motion = 'static'
    if (toggle) toggle.hidden = true
    return () => {
      draw?.dispose?.()
      tokens?.dispose()
      delete root.dataset.motion
    }
  }
  const reduced = window.matchMedia('(prefers-reduced-motion: reduce)')
  const fine = window.matchMedia('(hover: hover) and (pointer: fine)')
  const connection = (
    navigator as Navigator & { connection?: { saveData?: boolean } }
  ).connection
  let paused = !!connection?.saveData
  let visible = false
  let disposed = false
  let frame: number | null = null
  let lastTime = 0
  let clock = 0
  let dirty = true
  let target = { x: 0, y: 0 }
  let pointer = { x: 0, y: 0 }
  let tokenPointer: { x: number; y: number } | null = null
  let measured = true
  let sceneProgress = 0

  const updateControls = () => {
    root.dataset.motion = !draw
      ? 'static'
      : reduced.matches
        ? 'reduced'
        : paused
          ? 'paused'
          : 'playing'
    if (!toggle) return
    toggle.hidden = reduced.matches || !draw
    toggle.setAttribute('aria-pressed', String(paused))
    const play = toggle.querySelector<HTMLElement>('[data-play-label]')
    const pause = toggle.querySelector<HTMLElement>('[data-pause-label]')
    if (play) play.hidden = !paused
    if (pause) pause.hidden = paused
    toggle.querySelector('[data-play-icon]')?.toggleAttribute('hidden', !paused)
    toggle.querySelector('[data-pause-icon]')?.toggleAttribute('hidden', paused)
    toggle.setAttribute(
      'aria-label',
      (paused ? play : pause)?.textContent || ''
    )
  }
  const schedule = () => {
    if (!disposed && frame === null && !document.hidden) {
      frame = requestAnimationFrame(render)
    }
  }
  const readLayout = () => {
    const cinemaRect = cinema.getBoundingClientRect()
    const frameRect = inner.getBoundingClientRect()
    const stickyTop = Number.parseFloat(window.getComputedStyle(inner).top) || 0
    const animated = !!draw && !reduced.matches && window.innerHeight > 600
    sceneProgress = animated
      ? cinemaPosition(
          cinemaRect.top,
          cinemaRect.height,
          frameRect.height,
          stickyTop
        )
      : 0
    inner.style.setProperty('--scene-progress', String(sceneProgress))
    const focused = scenePanels.findIndex((panel) =>
      panel.contains(document.activeElement)
    )
    const chapter =
      focused >= 0
        ? focused
        : Math.min(
            scenePanels.length - 1,
            Math.floor(sceneProgress * scenePanels.length)
          )
    inner.dataset.chapter = String(chapter)
    scenePanels.forEach((panel, index) => {
      const active = !animated || index === chapter
      panel.toggleAttribute('data-active', active)
      panel.setAttribute('aria-hidden', String(!active))
      panel.inert = !active
    })
    sceneSteps.forEach((step, index) =>
      step.toggleAttribute('data-active', index === chapter)
    )
    tokens?.measure(frameRect, [
      ...scenePanels,
      ...sceneSteps,
      ...(toggle ? [toggle] : []),
    ])
    if (story) {
      const rect = story.getBoundingClientRect()
      const progress = storyPosition(rect.top, rect.height, window.innerHeight)
      story.style.setProperty('--story-progress', String(progress))
      const distances = steps.map((step) =>
        Math.abs(
          step.getBoundingClientRect().top +
            step.offsetHeight / 2 -
            window.innerHeight / 2
        )
      )
      const active = distances.indexOf(Math.min(...distances))
      steps.forEach((step, i) =>
        step.toggleAttribute('data-active', i === active)
      )
      const keepFocus = panels.findIndex((panel) =>
        panel.contains(document.activeElement)
      )
      const chapter =
        keepFocus >= 0
          ? keepFocus
          : reduced.matches ||
              paused ||
              window.innerWidth <= 680 ||
              window.innerHeight <= 650
            ? 2
            : active
      story.dataset.chapter = String(chapter)
      panels.forEach((panel, index) => {
        panel.setAttribute('aria-hidden', String(index !== chapter))
        panel.inert = index !== chapter
      })
      links.forEach((link, index) => {
        if (index === chapter) link.setAttribute('aria-current', 'step')
        else link.removeAttribute('aria-current')
      })
    }
    measured = false
  }
  const render = (now: number): void => {
    frame = null
    if (disposed || document.hidden) return
    if (measured) readLayout()
    const animate = !reduced.matches && !paused
    if (animate) {
      pointer.x += (target.x - pointer.x) * 0.14
      pointer.y += (target.y - pointer.y) * 0.14
    } else pointer = { x: 0, y: 0 }
    inner.style.setProperty('--pointer-x', `${pointer.x * 12}px`)
    inner.style.setProperty('--pointer-y', `${pointer.y * 8}px`)
    inner.style.setProperty('--rotate-x', `${-pointer.y * 3}deg`)
    inner.style.setProperty('--rotate-y', `${pointer.x * 4}deg`)
    // Film is capped at 30fps; a hidden tab/offscreen scene owns no running loop.
    if (
      draw &&
      (dirty || (animate && visible && now - lastTime >= 1000 / 30))
    ) {
      if (lastTime && animate) clock += Math.min((now - lastTime) / 1000, 0.1)
      draw(reduced.matches ? 0 : clock, pointer, sceneProgress)
      tokens?.draw(clock, tokenPointer, animate)
      lastTime = now
      dirty = false
    } else if (!draw && dirty) {
      tokens?.draw(0, null, false)
      dirty = false
    }
    if (draw && animate && visible) schedule()
  }
  const update = () => {
    dirty = true
    measured = true
    schedule()
  }
  const filmUnavailable = () => {
    draw?.dispose?.()
    draw = null
    tokens?.draw(0, null, false)
    updateControls()
    update()
  }
  const resize = () => {
    measured = true
    dirty = true
    schedule()
  }
  const move = (event: PointerEvent) => {
    if (
      event.pointerType === 'touch' ||
      !fine.matches ||
      reduced.matches ||
      paused
    ) {
      return
    }
    target = pointerPosition(
      event.clientX,
      event.clientY,
      cinema.getBoundingClientRect()
    )
    const frame = inner.getBoundingClientRect()
    tokenPointer = {
      x: event.clientX - frame.left,
      y: event.clientY - frame.top,
    }
    schedule()
  }
  const leave = () => {
    target = { x: 0, y: 0 }
    tokenPointer = null
    schedule()
  }
  const preferences = () => {
    target = { x: 0, y: 0 }
    tokenPointer = null

    dirty = true

    measured = true

    lastTime = 0

    updateControls()

    schedule()
  }
  const toggleMotion = () => {
    paused = !paused
    preferences()
  }
  const visibility = () => {
    if (document.hidden && frame !== null) {
      cancelAnimationFrame(frame)
      frame = null
    }
    lastTime = 0
    if (!document.hidden) {
      dirty = true
      measured = true
      schedule()
    }
  }
  const observer = new window.IntersectionObserver(([entry]) => {
    visible = entry.isIntersecting
    lastTime = 0
    if (!visible && frame !== null) {
      cancelAnimationFrame(frame)
      frame = null
    }
    if (visible) {
      dirty = true
      schedule()
    }
  })
  observer.observe(cinema)
  const resizeObserver = new window.ResizeObserver(resize)
  resizeObserver.observe(root)
  resizeObserver.observe(cinema)
  canvas.addEventListener('webglcontextlost', filmUnavailable)
  cinema.addEventListener('pointermove', move, { passive: true })
  cinema.addEventListener('pointerleave', leave)
  toggle?.addEventListener('click', toggleMotion)
  // Capture also observes a scrollable parent without taking over native scrolling.
  document.addEventListener('scroll', update, { passive: true, capture: true })
  window.addEventListener('resize', resize, { passive: true })
  document.addEventListener('visibilitychange', visibility)
  root.addEventListener('focusin', update)
  root.addEventListener('focusout', update)
  reduced.addEventListener('change', preferences)
  fine.addEventListener('change', preferences)
  updateControls()
  schedule()
  return () => {
    disposed = true
    draw?.dispose?.()
    tokens?.dispose()
    if (frame !== null) cancelAnimationFrame(frame)
    observer.disconnect()
    resizeObserver.disconnect()
    canvas.removeEventListener('webglcontextlost', filmUnavailable)
    cinema.removeEventListener('pointermove', move)
    cinema.removeEventListener('pointerleave', leave)
    toggle?.removeEventListener('click', toggleMotion)
    document.removeEventListener('scroll', update, true)
    window.removeEventListener('resize', resize)
    document.removeEventListener('visibilitychange', visibility)
    root.removeEventListener('focusin', update)
    root.removeEventListener('focusout', update)
    reduced.removeEventListener('change', preferences)
    fine.removeEventListener('change', preferences)
    delete root.dataset.motion
    delete inner.dataset.chapter
    scenePanels.forEach((panel) => {
      panel.inert = false
      panel.removeAttribute('aria-hidden')
      panel.removeAttribute('data-active')
    })
    sceneSteps.forEach((step) => step.removeAttribute('data-active'))
    for (const key of [
      '--pointer-x',
      '--pointer-y',
      '--rotate-x',
      '--rotate-y',
      '--scene-progress',
    ]) {
      inner.style.removeProperty(key)
    }
    story?.style.removeProperty('--story-progress')
    if (story) delete story.dataset.chapter
    steps.forEach((step) => step.removeAttribute('data-active'))
    panels.forEach((panel) => {
      panel.inert = false
      panel.removeAttribute('aria-hidden')
    })
    links.forEach((link) => link.removeAttribute('aria-current'))
  }
}
