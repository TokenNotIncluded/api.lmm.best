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
  CYCLE_START,
  RESPONSE_END,
  createCamera,
  INPUT,
  projectPoint,
} from './home-core'
import { createCanvasCore } from './home-core-canvas'
import { createWebGLCore, filmLayout, type CoreFilm } from './home-core-webgl'
import { mountHomeGravity } from './home-gravity'
import { homePalette } from './home-palette'
import { createTokenCloud } from './home-token-cloud'
import { homeWorldFrame, smoothWorldProgress } from './home-worlds'

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

export function cinemaPosition(
  top: number,
  height: number,
  frameHeight: number,
  stickyTop = 0
) {
  return unit((stickyTop - top) / Math.max(height - frameHeight, 1))
}

/** A small network reading the chosen token through fixed visual layers. */
function createFilm(canvas: HTMLCanvasElement): CoreFilm | null {
  const palette = homePalette(canvas.parentElement ?? canvas)
  return createWebGLCore(canvas, palette) ?? createCanvasCore(canvas, palette)
}

/** One lazily loaded GPU renderer also makes static chapter copies on phones. */
function mountChapterFilms(root: HTMLElement, onReady: () => void) {
  const canvas = root.querySelector<HTMLCanvasElement>('[data-world-film]')
  const canvases = [
    ...root.querySelectorAll<HTMLCanvasElement>('[data-chapter-film]'),
  ]
  let film: import('./home-world-three').HomeWorldFilm | null = null
  let loading: Promise<void> | null = null
  let disposed = false
  let unavailable = false
  const failed = () => {
    if (disposed) return
    unavailable = true
    film?.dispose()
    film = null
    if (canvas) canvas.dataset.unavailable = 'true'
    root.dataset.worldUnavailable = 'true'
    onReady()
  }
  const load = () => {
    if (!canvas || loading || disposed) return
    loading = import('./home-world-three')
      .then(({ createHomeWorldFilm }) => {
        if (disposed) return
        film = createHomeWorldFilm(canvas, homePalette(root), failed)
        if (!film) {
          failed()
          return
        }
        canvas.dataset.ready = 'true'
        if (
          window.innerWidth > 680 &&
          window.innerHeight > 600 &&
          !document.hidden
        ) {
          film.draw(
            1,
            { x: 0, y: 0 },
            0.1,
            canvas.clientWidth,
            canvas.clientHeight
          )
        }
        resize()
        onReady()
      })
      .catch((error: unknown) => {
        if (canvas) {
          canvas.dataset.unavailableReason =
            error instanceof Error ? error.message : String(error)
        }
        failed()
      })
  }
  const drawStatic = (target: HTMLCanvasElement) => {
    if (!target.clientWidth || !target.clientHeight || document.hidden) return
    if (!film) {
      load()
      return
    }
    film.draw(
      Number(target.dataset.chapterFilm),
      { x: 0, y: 0 },
      0.5,
      target.clientWidth,
      target.clientHeight,
      target
    )
  }
  const resize = () =>
    canvases.forEach((target) => {
      const rect = target.getBoundingClientRect()
      if (
        !observer ||
        (rect.bottom >= -160 && rect.top <= window.innerHeight + 160)
      ) {
        drawStatic(target)
      }
    })
  const observer =
    canvases.length && typeof window.IntersectionObserver === 'function'
      ? new window.IntersectionObserver(
          (entries) => {
            for (const entry of entries) {
              if (entry.isIntersecting) {
                drawStatic(entry.target as HTMLCanvasElement)
              }
            }
          },
          { rootMargin: '160px' }
        )
      : null
  for (const target of canvases) observer?.observe(target)
  if (canvases.length) {
    window.addEventListener('resize', resize, { passive: true })
    document.addEventListener('visibilitychange', resize)
  }
  const preload = () => load()
  const preloadObserver =
    canvas && typeof window.IntersectionObserver === 'function'
      ? new window.IntersectionObserver(
          (entries) => {
            if (entries.some((entry) => entry.isIntersecting)) {
              load()
              preloadObserver?.disconnect()
            }
          },
          { rootMargin: '240px' }
        )
      : null
  if (canvas) preloadObserver?.observe(root)
  root.addEventListener('pointerenter', preload, { passive: true, once: true })
  root.addEventListener('focusin', preload, { once: true })
  resize()
  return {
    get unavailable() {
      return unavailable
    },
    get ready() {
      return !canvas || (!!film && !unavailable)
    },
    draw(
      chapter: number,
      pointer: { x: number; y: number },
      progress: number,
      next = chapter,
      mix = 0
    ) {
      if (!chapter || !canvas || !canvas.clientWidth || !canvas.clientHeight) {
        return
      }
      if (!film) {
        load()
        return
      }
      film.draw(
        chapter,
        pointer,
        progress,
        canvas.clientWidth,
        canvas.clientHeight,
        undefined,
        next,
        mix
      )
    },
    dispose() {
      disposed = true
      observer?.disconnect()
      preloadObserver?.disconnect()
      root.removeEventListener('pointerenter', preload)
      root.removeEventListener('focusin', preload)
      window.removeEventListener('resize', resize)
      document.removeEventListener('visibilitychange', resize)
      film?.dispose()
      delete root.dataset.worldUnavailable
    },
  }
}

/** A single owned animation lifecycle, shared by the page and its browser tests. */
export function mountHomeMotion(root: HTMLElement) {
  const cinema = root.querySelector<HTMLElement>('[data-cinema]')
  const inner = root.querySelector<HTMLElement>('[data-cinema-inner]')
  const canvas = root.querySelector<HTMLCanvasElement>('[data-film]')
  if (!cinema || !inner || !canvas) return () => {}
  let chapterReady = () => {}
  const chapterFilms = mountChapterFilms(root, () => chapterReady())
  const visual = root.querySelector<HTMLElement>('[data-home-visual]') ?? inner
  const tokenField = root.querySelector<HTMLInputElement>(
    '[data-home-token-field]'
  )
  let draw = createFilm(canvas)
  const inputZone = root.querySelector<HTMLElement>('[data-token-input]')
  const result = root.querySelector<HTMLOutputElement>('[data-token-result]')
  const chosen = root.querySelector<HTMLElement>('[data-selected-token]')
  const predicted = root.querySelector<HTMLElement>('[data-predicted-token]')
  const releaseGravity = mountHomeGravity(root, inputZone)
  let layout = filmLayout(canvas)
  let refreshSelection = () => {}
  const placeInput = (
    time: number,
    pointer: { x: number; y: number },
    progress: number
  ) => {
    if (!inputZone || !canvas.clientWidth || !canvas.clientHeight) return
    const camera = createCamera(
      time,
      pointer,
      progress,
      canvas.clientWidth,
      canvas.clientHeight,
      layout
    )
    const halfWidth = (INPUT.cols * INPUT.cell) / 2 + 0.06
    const halfHeight = (INPUT.rows * INPUT.cell) / 2 + 0.06
    const corners = [
      { x: INPUT.x - halfWidth, y: -halfHeight, z: 0 },
      { x: INPUT.x + halfWidth, y: -halfHeight, z: 0 },
      { x: INPUT.x + halfWidth, y: halfHeight, z: 0 },
      { x: INPUT.x - halfWidth, y: halfHeight, z: 0 },
    ].map((point) => projectPoint(camera, point))
    const left = Math.min(...corners.map((point) => point.x))
    const right = Math.max(...corners.map((point) => point.x))
    const top = Math.min(...corners.map((point) => point.y))
    const bottom = Math.max(...corners.map((point) => point.y))
    const width = Math.min(
      canvas.clientWidth - 24,
      Math.max(96, right - left + 20)
    )
    const height = Math.max(68, bottom - top + 16)
    inputZone.style.left = `${Math.max(12, Math.min(canvas.clientWidth - width - 12, (left + right - width) / 2))}px`
    inputZone.style.top = `${Math.max(42, (top + bottom - height) / 2)}px`
    inputZone.style.width = `${width}px`
    inputZone.style.height = `${height}px`
  }
  const selectToken = (token: string) => {
    const match = draw?.setToken?.(token)
    if (!match) return
    if (chosen) chosen.textContent = token
    if (predicted) predicted.textContent = match
    if (result) result.hidden = false
    if (tokenField && tokenField.value !== token) tokenField.value = token
    root
      .querySelectorAll<HTMLElement>('[data-token-option]')
      .forEach((option) =>
        option.toggleAttribute(
          'data-selected',
          option.dataset.tokenOption === token
        )
      )
    refreshSelection()
  }
  const resetSelection = () => {
    if (chosen) chosen.textContent = ''
    if (predicted) predicted.textContent = ''
    if (result) result.hidden = true
    root
      .querySelectorAll<HTMLElement>('[data-token-option]')
      .forEach((option) => option.removeAttribute('data-selected'))
  }
  const tokens = createTokenCloud(
    root.querySelector<HTMLElement>('[data-token-cloud]'),
    selectToken
  )
  const allowedTokens = new Set(
    [...root.querySelectorAll<HTMLElement>('[data-token-option]')].map(
      (option) => option.dataset.tokenOption
    )
  )
  const dragOver = (event: DragEvent) => {
    event.preventDefault()
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy'
    inputZone?.setAttribute('data-drag-over', '')
  }
  const dragLeave = (event: DragEvent) => {
    if (!inputZone?.contains(event.relatedTarget as Node | null)) {
      inputZone?.removeAttribute('data-drag-over')
    }
  }
  const drop = (event: DragEvent) => {
    event.preventDefault()
    inputZone?.removeAttribute('data-drag-over')
    const token = event.dataTransfer?.getData('text/plain')
    if (token && allowedTokens.has(token)) selectToken(token)
  }
  const typeToken = () => {
    const token = tokenField?.value.trim().slice(0, 20)
    if (token) selectToken(token)
  }
  const replayToken = (event: KeyboardEvent) => {
    if (event.key === 'Enter') {
      event.preventDefault()
      typeToken()
    }
  }
  tokenField?.addEventListener('input', typeToken)
  tokenField?.addEventListener('keydown', replayToken)
  inputZone?.addEventListener('dragover', dragOver)
  inputZone?.addEventListener('dragleave', dragLeave)
  inputZone?.addEventListener('drop', drop)
  const releaseInput = () => {
    inputZone?.removeEventListener('dragover', dragOver)
    inputZone?.removeEventListener('dragleave', dragLeave)
    inputZone?.removeEventListener('drop', drop)
    inputZone?.removeAttribute('data-drag-over')
    tokenField?.removeEventListener('input', typeToken)
    tokenField?.removeEventListener('keydown', replayToken)
  }
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
    refreshSelection = () => draw?.(RESPONSE_END, { x: 0, y: 0 }, 0)
    draw?.(RESPONSE_END, { x: 0, y: 0 }, 0)
    placeInput(RESPONSE_END, { x: 0, y: 0 }, 0)
    tokens?.measure(inner.getBoundingClientRect(), [
      ...scenePanels,
      ...(inputZone ? [inputZone] : []),
    ])
    tokens?.draw(0, null, false)
    root.dataset.motion = 'static'
    if (toggle) toggle.hidden = true
    return () => {
      draw?.dispose?.()
      tokens?.dispose()
      releaseInput()
      releaseGravity()
      chapterFilms.dispose()
      resetSelection()
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
  let clock = RESPONSE_END
  let responding = false
  let dirty = true
  let target = { x: 0, y: 0 }
  let pointer = { x: 0, y: 0 }
  let tokenPointer: { x: number; y: number } | null = null
  let measured = true
  let sceneProgress = 0
  let displayProgress = 0
  let lastMotionTime = 0
  let animatedLayout = false
  let manualChapter: number | null = null

  const updateControls = () => {
    root.dataset.motion =
      !draw || chapterFilms.unavailable
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
  chapterReady = () => {
    dirty = true
    measured = true
    updateControls()
    schedule()
  }
  refreshSelection = () => {
    clock = reduced.matches || paused ? RESPONSE_END : CYCLE_START
    responding = !reduced.matches && !paused
    lastTime = 0
    dirty = true
    schedule()
  }
  const readLayout = () => {
    const cinemaRect = cinema.getBoundingClientRect()
    const frameRect = inner.getBoundingClientRect()
    const stickyTop = Number.parseFloat(window.getComputedStyle(inner).top) || 0
    const animated =
      !!draw &&
      !chapterFilms.unavailable &&
      !reduced.matches &&
      window.innerHeight > 600 &&
      window.innerWidth > 680
    animatedLayout = animated
    sceneProgress =
      manualChapter !== null && animated
        ? manualChapter / Math.max(1, scenePanels.length - 1)
        : animated
          ? cinemaPosition(
              cinemaRect.top,
              cinemaRect.height,
              frameRect.height,
              stickyTop
            )
          : 0
    layout = filmLayout(canvas)
    placeInput(animated ? clock : 0, pointer, sceneProgress)
    const focused = scenePanels.findIndex((panel) =>
      panel.contains(document.activeElement)
    )
    const chapter = !animated
      ? 0
      : focused >= 0 && manualChapter === null && animated
        ? focused
        : (manualChapter ??
          homeWorldFrame(sceneProgress, scenePanels.length).chapter)
    // A focused link can retain its panel while scrolling; keep its model aligned.
    if (animated && (manualChapter !== null || focused >= 0)) {
      sceneProgress = Math.min(1, (chapter + 0.5) / scenePanels.length)
    }
    if (!animated) displayProgress = 0
    tokens?.measure(frameRect, [
      ...scenePanels,
      ...sceneSteps,
      ...(inputZone ? [inputZone] : []),
      ...(toggle ? [toggle] : []),
      ...root.querySelectorAll<HTMLElement>('.lmm-simulation-info'),
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
    const animate = !reduced.matches && !paused && !chapterFilms.unavailable
    const elapsed = lastMotionTime ? now - lastMotionTime : 1000 / 24
    lastMotionTime = now
    // Keep the first film intact on a slow chunk load, then join the same damping.
    const renderTarget =
      chapterFilms.ready || !animatedLayout
        ? sceneProgress
        : Math.min(sceneProgress, 0.54 / scenePanels.length)
    displayProgress =
      animatedLayout && animate && visible
        ? smoothWorldProgress(displayProgress, renderTarget, elapsed)
        : renderTarget
    const state = homeWorldFrame(displayProgress, scenePanels.length)
    inner.dataset.chapter = String(state.chapter)
    inner.style.setProperty('--scene-progress', String(displayProgress))
    const heroWeight = !animatedLayout
      ? 1
      : state.from === 0
        ? 1 - state.mix
        : 0
    inner.style.setProperty('--hero-opacity', String(heroWeight))
    inner.style.setProperty('--world-opacity', String(1 - heroWeight))
    inner.style.setProperty('--hero-offset', `${(1 - heroWeight) * -1.5}rem`)
    const networkVisible = !animatedLayout || heroWeight > 0.01
    for (const element of [
      inputZone,
      root.querySelector<HTMLElement>('[data-token-cloud]'),
      root.querySelector<HTMLElement>('.lmm-simulation-info'),
    ]) {
      if (!element) continue
      element.hidden = !networkVisible
      element.inert = animatedLayout && state.chapter !== 0
    }
    scenePanels.forEach((panel, index) => {
      const active = !animatedLayout || index === state.chapter
      const weight = !animatedLayout
        ? 1
        : state.from === state.to
          ? Number(index === state.from)
          : index === state.from
            ? 1 - state.mix
            : index === state.to
              ? state.mix
              : 0
      panel.style.setProperty(
        '--panel-offset',
        `${(index === state.to ? 1 : -1) * (1 - weight) * 1.8}rem`
      )
      // A settled overlap shows one readable heading; the chapter change itself fades.
      panel.style.setProperty(
        '--panel-opacity',
        String(active ? 0.78 + weight * 0.22 : 0)
      )
      panel.toggleAttribute('data-visible', active)
      panel.toggleAttribute('data-active', active)
      panel.setAttribute('aria-hidden', String(!active))
      panel.inert = !active
    })
    sceneSteps.forEach((step, index) => {
      step.toggleAttribute('data-active', index === state.chapter)
      step
        .querySelector('button')
        ?.setAttribute('aria-pressed', String(index === state.chapter))
    })
    if (animate) {
      pointer.x += (target.x - pointer.x) * 0.11
      pointer.y += (target.y - pointer.y) * 0.11
    } else pointer = { x: 0, y: 0 }
    const ambientActive =
      animate &&
      visible &&
      fine.matches &&
      window.innerWidth >= 720 &&
      !responding
    const cameraPointer = ambientActive
      ? {
          x: pointer.x + Math.sin(now / 3600) * 0.045,
          y: pointer.y + Math.cos(now / 4700) * 0.032,
        }
      : pointer
    inner.style.setProperty('--pointer-x', `${cameraPointer.x * 18}px`)
    inner.style.setProperty('--pointer-y', `${cameraPointer.y * 12}px`)
    inner.style.setProperty('--rotate-x', `${-cameraPointer.y * 4}deg`)
    inner.style.setProperty('--rotate-y', `${cameraPointer.x * 6}deg`)
    // Film is capped at 24fps; a hidden tab/offscreen scene owns no running loop.
    if (
      draw &&
      (dirty || (animate && visible && now - lastTime >= 1000 / 24))
    ) {
      if (lastTime && animate && responding) {
        clock = Math.min(
          RESPONSE_END,
          clock + Math.min((now - lastTime) / 1000, 0.1) * 8
        )
        if (clock >= RESPONSE_END) responding = false
      }
      const time = reduced.matches ? RESPONSE_END : clock
      if (heroWeight > 0 || !animatedLayout) {
        draw(time, cameraPointer, 0)
      }
      if (animatedLayout && (state.to > 0 || state.from > 0)) {
        chapterFilms.draw(
          Math.max(1, state.from),
          cameraPointer,
          displayProgress,
          Math.max(1, state.to),
          state.from === 0 ? state.mix - 1 : state.mix
        )
      }
      placeInput(time, cameraPointer, displayProgress)
      if (!inputZone?.hidden) tokens?.draw(now / 1000, tokenPointer, animate)
      lastTime = now
      dirty = false
    } else if (!draw && dirty) {
      tokens?.draw(0, null, false)
      dirty = false
    }
    const pointerMoving =
      Math.abs(pointer.x - target.x) + Math.abs(pointer.y - target.y) > 0.001
    if (
      draw &&
      animate &&
      visible &&
      (responding ||
        pointerMoving ||
        ambientActive ||
        Math.abs(displayProgress - renderTarget) > 0.0001)
    ) {
      schedule()
    }
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
    if (!visual.contains(event.target as Node)) return
    target = pointerPosition(
      event.clientX,
      event.clientY,
      visual.getBoundingClientRect()
    )
    const frame = visual.getBoundingClientRect()
    tokenPointer = {
      x: event.clientX - frame.left,
      y: event.clientY - frame.top,
    }
    dirty = true
    schedule()
  }
  const leave = () => {
    target = { x: 0, y: 0 }
    tokenPointer = null
    dirty = true
    schedule()
  }
  const preferences = () => {
    target = { x: 0, y: 0 }
    tokenPointer = null
    if (reduced.matches) {
      clock = RESPONSE_END
      responding = false
    }

    dirty = true

    measured = true

    lastTime = 0
    lastMotionTime = 0

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
    lastMotionTime = 0
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
  const sceneButtons = [
    ...root.querySelectorAll<HTMLButtonElement>('[data-cinema-jump]'),
  ]
  const jumpScene = (event: Event) => {
    const index = Number(
      (event.currentTarget as HTMLElement).dataset.cinemaJump
    )
    if (Number.isInteger(index) && index >= 0 && index < scenePanels.length) {
      manualChapter = index
      update()
    }
  }
  sceneButtons.forEach((button) => button.addEventListener('click', jumpScene))
  const scrollScene = () => {
    manualChapter = null
    update()
  }
  // Capture also observes a scrollable parent without taking over native scrolling.
  document.addEventListener('scroll', scrollScene, {
    passive: true,
    capture: true,
  })
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
    releaseInput()
    releaseGravity()
    chapterFilms.dispose()
    resetSelection()
    for (const element of [
      inputZone,
      root.querySelector<HTMLElement>('[data-token-cloud]'),
      root.querySelector<HTMLElement>('.lmm-simulation-info'),
    ]) {
      if (!element) continue
      element.hidden = false
      element.inert = false
    }
    if (frame !== null) cancelAnimationFrame(frame)
    observer.disconnect()
    resizeObserver.disconnect()
    canvas.removeEventListener('webglcontextlost', filmUnavailable)
    cinema.removeEventListener('pointermove', move)
    cinema.removeEventListener('pointerleave', leave)
    toggle?.removeEventListener('click', toggleMotion)
    sceneButtons.forEach((button) =>
      button.removeEventListener('click', jumpScene)
    )
    document.removeEventListener('scroll', scrollScene, true)
    window.removeEventListener('resize', resize)
    document.removeEventListener('visibilitychange', visibility)
    root.removeEventListener('focusin', update)
    root.removeEventListener('focusout', update)
    reduced.removeEventListener('change', preferences)
    fine.removeEventListener('change', preferences)
    delete root.dataset.motion
    delete inner.dataset.chapter
    for (const key of ['--hero-opacity', '--world-opacity', '--hero-offset']) {
      inner.style.removeProperty(key)
    }
    scenePanels.forEach((panel) => {
      panel.inert = false
      panel.removeAttribute('aria-hidden')
      panel.removeAttribute('data-active')
      panel.removeAttribute('data-visible')
      panel.style.removeProperty('--panel-opacity')
      panel.style.removeProperty('--panel-offset')
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
    for (const key of ['left', 'top', 'width', 'height']) {
      inputZone?.style.removeProperty(key)
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
