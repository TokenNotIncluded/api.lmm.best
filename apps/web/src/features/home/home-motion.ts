/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { createHomePoster } from './home-poster'

const clamp = (value: number, maximum = 1) =>
  Math.max(0, Math.min(maximum, value))
export const easePosterProgress = (
  current: number,
  target: number,
  elapsed: number
) =>
  current +
  (target - current) * (1 - Math.exp(-Math.max(0, Math.min(80, elapsed)) / 190))

/** One owned animation loop. Scroll stays native; geometry eases towards it. */
export function mountHomeMotion(root: HTMLElement) {
  const cinema = root.querySelector<HTMLElement>('[data-cinema]')
  const innerNode = root.querySelector<HTMLElement>('[data-cinema-inner]')
  const canvas = root.querySelector<HTMLCanvasElement>('[data-film]')
  if (!cinema || !innerNode || !canvas) return () => {}
  const inner = innerNode
  const film = createHomePoster(canvas, () => refresh())
  const reduced = window.matchMedia('(prefers-reduced-motion: reduce)')
  const fine = window.matchMedia('(hover: hover) and (pointer: fine)')
  const connection = (
    navigator as Navigator & { connection?: { saveData?: boolean } }
  ).connection
  const panels = [...root.querySelectorAll<HTMLElement>('[data-cinema-panel]')]
  const buttons = [
    ...root.querySelectorAll<HTMLButtonElement>('[data-cinema-jump]'),
  ]
  const toggle = root.querySelector<HTMLButtonElement>('[data-motion-toggle]')
  const story = root.querySelector<HTMLElement>('[data-story]')
  const storySteps = [
    ...root.querySelectorAll<HTMLElement>('[data-story-step]'),
  ]
  const storyPanels = [
    ...root.querySelectorAll<HTMLElement>('[data-story-panel]'),
  ]
  const storyLinks = [...root.querySelectorAll<HTMLElement>('[data-step-link]')]
  const copies = new Map<
    HTMLCanvasElement,
    ReturnType<typeof createHomePoster>
  >()
  let frame: number | null = null,
    disposed = false,
    visible = true,
    paused = connection?.saveData === true
  let dirty = true,
    measure = true,
    last = 0,
    progress = 0,
    target = 0
  let manual: number | null = null
  let layout = false, filmVisible = true
  const visibleCopies = new Set<HTMLCanvasElement>()
  const pointer = {
    x: 0,
    y: 0,
    vx: 0,
    vy: 0,
    previousX: 0,
    previousY: 0,
    active: false,
  }
  const restingPointer = { ...pointer }
  let pointerCanvas = canvas
  let pointerTarget = { x: 0, y: 0, active: false }
  const supported =
    typeof window.IntersectionObserver === 'function' &&
    typeof window.ResizeObserver === 'function'
  const controls = () => {
    root.dataset.motion =
      !film || !supported
        ? 'static'
        : reduced.matches
          ? 'reduced'
          : paused
            ? 'paused'
            : 'playing'
    if (!toggle) return
    toggle.hidden = !film || !supported || reduced.matches
    toggle.setAttribute('aria-pressed', String(paused))
    for (const [selector, hidden] of [
      ['[data-play-label]', !paused],
      ['[data-play-icon]', !paused],
      ['[data-pause-label]', paused],
      ['[data-pause-icon]', paused],
    ] as const) {
      toggle
        .querySelector<HTMLElement>(selector)
        ?.toggleAttribute('hidden', hidden)
    }
    toggle.setAttribute(
      'aria-label',
      toggle.querySelector<HTMLElement>(
        paused ? '[data-play-label]' : '[data-pause-label]'
      )?.textContent ?? ''
    )
  }
  const schedule = () => {
    if (frame === null && !disposed && !document.hidden && visible) {
      frame = requestAnimationFrame(render)
    }
  }
  const refresh = () => {
    dirty = true
    measure = true
    schedule()
  }
  const readLayout = () => {
    const rect = cinema.getBoundingClientRect(),
      box = inner.getBoundingClientRect()
    layout =
      window.innerWidth > 900 &&
      window.innerHeight > 600 &&
      !reduced.matches &&
      !!film &&
      supported
    const focused = panels.findIndex((panel) =>
      panel.contains(document.activeElement)
    )
    target =
      manual !== null
        ? manual
        : focused >= 0 && layout
          ? focused
          : layout
            ? clamp(
                ((Number.parseFloat(getComputedStyle(inner).top) || 0) -
                  rect.top) /
                  Math.max(1, rect.height - box.height)
              ) * 4
            : 0
    if (!layout) progress = 0
    if (story) {
      const box = story.getBoundingClientRect()
      const positions = storySteps.map((step) =>
        Math.abs(
          step.getBoundingClientRect().top +
            step.offsetHeight / 2 -
            window.innerHeight / 2
        )
      )
      const current =
        layout && !paused ? positions.indexOf(Math.min(...positions)) : 2
      story.dataset.chapter = String(current)
      story.style.setProperty(
        '--story-progress',
        String(
          clamp((window.innerHeight * 0.5 - box.top) / Math.max(1, box.height))
        )
      )
      storySteps.forEach((step, index) =>
        step.toggleAttribute('data-active', index === current)
      )
      storyPanels.forEach((panel, index) => {
        panel.inert = index !== current
        panel.setAttribute('aria-hidden', String(index !== current))
      })
      storyLinks.forEach((link, index) => {
        if (index === current) link.setAttribute('aria-current', 'step')
        else link.removeAttribute('aria-current')
      })
    }
    const art = canvas.getBoundingClientRect()
    filmVisible = layout || (art.bottom > 0 && art.top < window.innerHeight)
    visibleCopies.clear()
    if (!layout) {
      for (const copy of root.querySelectorAll<HTMLCanvasElement>('[data-chapter-film]')) {
        const rect = copy.getBoundingClientRect()
        if (rect.bottom > 0 && rect.top < window.innerHeight) {
          if (!copies.has(copy)) copies.set(copy, createHomePoster(copy, refresh))
          visibleCopies.add(copy)
        }
      }
    }
    measure = false
  }
  function render(now: number) {
    frame = null
    if (disposed || document.hidden || !visible) return
    const budget =
      window.innerWidth > 900 && fine.matches ? 1000 / 60 : 1000 / 30
    const elapsed = last ? Math.min(80, now - last) : budget
    if (!dirty && elapsed + 0.5 < budget) {
      schedule()
      return
    }
    last = now
    if (measure) readLayout()
    const animate = !reduced.matches && !paused && supported
    progress =
      layout && animate ? easePosterProgress(progress, target, elapsed) : target
    pointer.previousX = pointer.x
    pointer.previousY = pointer.y
    pointer.x += (pointerTarget.x - pointer.x) * (1 - Math.exp(-elapsed / 75))
    pointer.y += (pointerTarget.y - pointer.y) * (1 - Math.exp(-elapsed / 75))
    const seconds = Math.max(elapsed / 1000, 0.001)
    const vx = Math.max(
      -1600,
      Math.min(1600, (pointer.x - pointer.previousX) / seconds)
    )
    const vy = Math.max(
      -1600,
      Math.min(1600, (pointer.y - pointer.previousY) / seconds)
    )
    pointer.vx += (vx - pointer.vx) * (1 - Math.exp(-elapsed / 110))
    pointer.vy += (vy - pointer.vy) * (1 - Math.exp(-elapsed / 110))
    pointer.active = pointerTarget.active && animate
    const active = Math.round(progress)
    inner.dataset.chapter = String(active)
    inner.style.setProperty('--scene-progress', String(progress))
    panels.forEach((panel, index) => {
      const shown = !layout || index === active
      panel.toggleAttribute('data-active', shown)
      panel.inert = !shown
      panel.setAttribute('aria-hidden', String(!shown))
    })
    buttons.forEach((button, index) => {
      button.setAttribute('aria-pressed', String(index === active))
      button.closest('li')?.toggleAttribute('data-active', index === active)
    })
    if (filmVisible) film?.draw(layout ? progress : 0, pointerCanvas === canvas ? pointer : restingPointer, elapsed / 1000, !animate)
    for (const copy of visibleCopies) {
      copies.get(copy)?.draw(Number(copy.dataset.chapterFilm), pointerCanvas === copy ? pointer : restingPointer, elapsed / 1000, !animate)
    }
    dirty = false
    if (film && animate && (filmVisible || visibleCopies.size)) schedule()
  }
  const move = (event: PointerEvent) => {
    if (
      (!fine.matches && event.pointerType !== 'touch') ||
      paused ||
      reduced.matches
    ) {
      return
    }
    const eventTarget = event.target as HTMLElement | null
    const touched = !layout ? eventTarget?.closest('[data-cinema-panel]')?.querySelector<HTMLCanvasElement>('[data-chapter-film]') : null
    const targetCanvas = touched ?? canvas
    if (pointerCanvas !== targetCanvas) pointerTarget.active = false
    pointerCanvas = targetCanvas
    const rect = pointerCanvas.getBoundingClientRect()
    if (!pointerTarget.active) {
      pointer.x = pointer.previousX = event.clientX - rect.left
      pointer.y = pointer.previousY = event.clientY - rect.top
      pointer.vx = pointer.vy = 0
    }
    pointerTarget = {
      x: event.clientX - rect.left,
      y: event.clientY - rect.top,
      active: true,
    }
    schedule()
  }
  const leave = () => {
    pointerTarget.active = false
    schedule()
  }
  const scrollScene = () => {
    manual = null
    refresh()
  }
  const jump = (event: Event) => {
    const value = Number(
      (event.currentTarget as HTMLElement).dataset.cinemaJump
    )
    if (layout) {
      manual = clamp(value, 4)
      refresh()
    } else {
      panels[value]?.scrollIntoView({
        block: 'start',
        behavior: reduced.matches ? 'instant' : 'smooth',
      })
    }
  }
  const preference = () => {
    pointerTarget.active = false
    last = 0
    controls()
    refresh()
  }
  const pause = () => {
    paused = !paused
    preference()
  }
  const visibility = () => {
    if (frame !== null) cancelAnimationFrame(frame)
    frame = null
    last = 0
    if (!document.hidden) refresh()
  }
  const observer = supported
    ? new window.IntersectionObserver(([entry]) => {
        visible = entry.isIntersecting
        if (!visible && frame !== null) {
          cancelAnimationFrame(frame)
          frame = null
        }
        if (visible) refresh()
      })
    : null
  const resizeObserver = supported ? new window.ResizeObserver(refresh) : null
  const themeObserver = typeof window.MutationObserver === 'function'
    ? new window.MutationObserver(refresh)
    : null
  themeObserver?.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
  observer?.observe(cinema)
  resizeObserver?.observe(inner)
  cinema.addEventListener('pointermove', move, { passive: true })
  cinema.addEventListener('pointerdown', move, { passive: true })
  cinema.addEventListener('pointerleave', leave)
  cinema.addEventListener('pointerup', leave)
  cinema.addEventListener('pointercancel', leave)
  document.addEventListener('scroll', scrollScene, {
    passive: true,
    capture: true,
  })
  window.addEventListener('resize', refresh, { passive: true })
  document.addEventListener('visibilitychange', visibility)
  root.addEventListener('focusin', refresh)
  root.addEventListener('focusout', refresh)
  reduced.addEventListener('change', preference)
  toggle?.addEventListener('click', pause)
  buttons.forEach((button) => button.addEventListener('click', jump))
  controls()
  if (supported) schedule()
  else {
    readLayout()
    render(0)
  }
  return () => {
    disposed = true
    if (frame !== null) cancelAnimationFrame(frame)
    observer?.disconnect()
    resizeObserver?.disconnect()
    themeObserver?.disconnect()
    visibleCopies.clear()
    film?.dispose()
    copies.forEach((copy) => copy?.dispose())
    copies.clear()
    cinema.removeEventListener('pointermove', move)
    cinema.removeEventListener('pointerdown', move)
    cinema.removeEventListener('pointerleave', leave)
    cinema.removeEventListener('pointerup', leave)
    cinema.removeEventListener('pointercancel', leave)
    document.removeEventListener('scroll', scrollScene, true)
    window.removeEventListener('resize', refresh)
    document.removeEventListener('visibilitychange', visibility)
    root.removeEventListener('focusin', refresh)
    root.removeEventListener('focusout', refresh)
    reduced.removeEventListener('change', preference)
    toggle?.removeEventListener('click', pause)
    buttons.forEach((button) => button.removeEventListener('click', jump))
    panels.forEach((panel) => {
      panel.inert = false
      panel.removeAttribute('aria-hidden')
    })
    storyPanels.forEach((panel) => {
      panel.inert = false
      panel.removeAttribute('aria-hidden')
    })
    delete root.dataset.motion
    delete inner.dataset.chapter
    if (story) delete story.dataset.chapter
  }
}
