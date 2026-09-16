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

type Point = { x: number; y: number; z: number }
type Face = { points: Point[]; normal: Point }

function sculpture(narrow: boolean): Face[] {
  const faces: Face[] = []
  const ribs = narrow ? 72 : 112
  const sides = narrow ? 20 : 32
  const point = (a: number, b: number): Point => ({
    x: (1.72 + 0.38 * Math.cos(b)) * Math.cos(a),
    y: (1.72 + 0.38 * Math.cos(b)) * Math.sin(a),
    z: 0.38 * Math.sin(b),
  })
  for (let rib = 0; rib < ribs; rib++) {
    const a = (rib / ribs) * Math.PI * 2
    const next = a + (Math.PI * 2) / ribs
    for (let side = 0; side < sides; side++) {
      const b = (side / sides) * Math.PI * 2
      const end = ((side + 1) / sides) * Math.PI * 2
      const mid = (b + end) / 2
      faces.push({
        points: [point(a, b), point(next, b), point(next, end), point(a, end)],
        normal: {
          x: Math.cos(mid) * Math.cos(a),
          y: Math.cos(mid) * Math.sin(a),
          z: Math.sin(mid),
        },
      })
    }
  }
  return faces
}

/** Original copper sculpture. No remote media, WebGL dependency, or fake live metrics. */
function createFilm(canvas: HTMLCanvasElement) {
  const ctx = canvas.getContext('2d', { alpha: false })
  if (!ctx) return null
  const mesh = sculpture(canvas.clientWidth < 650)
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
    backdrop.addColorStop(0, '#10221d')
    backdrop.addColorStop(0.55, '#24372c')
    backdrop.addColorStop(1, '#77806a')
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
    glow.addColorStop(0, '#b8b08366')
    glow.addColorStop(1, '#b8b08300')
    ctx.fillStyle = glow
    ctx.fillRect(0, 0, w, h)
    const narrow = w < 650
    const cx = w * (narrow ? 0.51 : 0.4)
    const cy = h * (narrow ? 0.32 : 0.46)
    const scale = Math.min(w * (narrow ? 0.19 : 0.15), h * 0.19)
    const ry =
      0.6 + Math.sin(time * 0.2) * 0.16 + pointer.x * 0.27 + progress * 0.28
    const rx =
      -0.22 + Math.cos(time * 0.16) * 0.1 + pointer.y * 0.18 - progress * 0.12
    const rz = -0.4 + Math.sin(time * 0.12) * 0.09
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
    const transformed = mesh
      .map((face) => {
        const points = face.points.map(rotate)
        return {
          points,
          normal: rotate(face.normal),
          z: points.reduce((sum, p) => sum + p.z, 0) / 4,
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
      ctx.fillStyle = `rgb(${c(206, 75)} ${c(133, 104)} ${c(83, 129)})`
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
    canvas.parentElement?.setAttribute('data-rendered', '')
  }
}

/** A single owned animation lifecycle, shared by the page and its browser tests. */
export function mountHomeMotion(root: HTMLElement) {
  const cinema = root.querySelector<HTMLElement>('[data-cinema]')
  const inner = root.querySelector<HTMLElement>('[data-cinema-inner]')
  const canvas = root.querySelector<HTMLCanvasElement>('[data-film]')
  if (!cinema || !inner || !canvas) return () => {}
  const draw = createFilm(canvas)
  const story = root.querySelector<HTMLElement>('[data-story]')
  const steps = [...root.querySelectorAll<HTMLElement>('[data-story-step]')]
  const toggle = root.querySelector<HTMLButtonElement>('[data-motion-toggle]')
  const panels = [...root.querySelectorAll<HTMLElement>('[data-story-panel]')]
  const links = [...root.querySelectorAll<HTMLElement>('[data-step-link]')]
  if (!('IntersectionObserver' in window) || !('ResizeObserver' in window)) {
    draw?.(0, { x: 0, y: 0 }, 0)
    if (toggle) toggle.hidden = true
    return () => {}
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
  let measured = true
  let sceneProgress = 0

  const updateControls = () => {
    root.dataset.motion = reduced.matches
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
    sceneProgress =
      reduced.matches || paused
        ? 0
        : unit(-cinemaRect.top / Math.max(cinemaRect.height, 1))
    inner.style.setProperty('--scene-progress', String(sceneProgress))
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
      lastTime = now
      dirty = false
    }
    if (draw && animate && visible) schedule()
  }
  const update = () => {
    measured = true
    schedule()
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
    schedule()
  }
  const leave = () => {
    target = { x: 0, y: 0 }
    schedule()
  }
  const preferences = () => {
    target = { x: 0, y: 0 }

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
  const observer = new IntersectionObserver(([entry]) => {
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
  const resizeObserver = new ResizeObserver(resize)
  resizeObserver.observe(root)
  resizeObserver.observe(cinema)
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
    if (frame !== null) cancelAnimationFrame(frame)
    observer.disconnect()
    resizeObserver.disconnect()
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
