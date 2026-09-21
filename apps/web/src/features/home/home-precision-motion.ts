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
/** Decorative media only: never treat its state as API-health telemetry. */
export const PRECISION_VIDEO =
  'https://strvid.nyc3.cdn.digitaloceanspaces.com/motionsite/abstract-video.mp4'

export type MotionConditions = {
  reduced: boolean
  saveData: boolean
  hidden: boolean
  visible: boolean
  paused: boolean
  dialogOpen: boolean
}

export function canPlayBackground(state: MotionConditions) {
  return !state.reduced && !state.saveData && !state.hidden && state.visible &&
    !state.paused && !state.dialogOpen
}

export function counterValue(target: number, elapsed: number) {
  const progress = Math.min(1, Math.max(0, elapsed / 900))
  return Math.round(target * (1 - (1 - progress) ** 3))
}

/** One disposable lifecycle for media, the native dialog and finite counters. */
export function mountPrecisionMotion(root: HTMLElement) {
  const background = root.querySelector<HTMLVideoElement>('[data-precision-video]')
  const dialog = root.querySelector<HTMLDialogElement>('[data-precision-dialog]')
  const demo = root.querySelector<HTMLVideoElement>('[data-precision-demo]')
  const open = root.querySelector<HTMLButtonElement>('[data-precision-open]')
  const close = root.querySelector<HTMLButtonElement>('[data-precision-close]')
  const toggle = root.querySelector<HTMLButtonElement>('[data-precision-pause]')
  if (!background || !dialog || !demo || !open || !close || !toggle) return

  const reduced = window.matchMedia('(prefers-reduced-motion: reduce)')
  const connection = (navigator as Navigator & {
    connection?: EventTarget & { saveData?: boolean }
  }).connection
  let visible = false
  let disposed = false
  let counted = false
  let frame = 0
  let paused = false
  const cleanup: Array<() => void> = []
  const listen = (target: EventTarget, event: string, handler: EventListener) => {
    target.addEventListener(event, handler)
    cleanup.push(() => target.removeEventListener(event, handler))
  }
  const counters = [...root.querySelectorAll<HTMLElement>('[data-precision-count]')]
  const finishCounters = () => {
    cancelAnimationFrame(frame)
    for (const node of counters) node.textContent = node.dataset.precisionCount || ''
  }
  const startCounters = () => {
    if (counted || !visible || document.hidden) return
    counted = true
    if (reduced.matches || connection?.saveData || paused) return finishCounters()
    const start = performance.now()
    const tick = (now: number) => {
      if (disposed) return
      for (const node of counters) {
        node.textContent = String(counterValue(Number(node.dataset.precisionCount), now - start))
      }
      if (now - start < 900) frame = requestAnimationFrame(tick)
    }
    frame = requestAnimationFrame(tick)
  }
  const sync = () => {
    const playing = canPlayBackground({
      reduced: reduced.matches, saveData: Boolean(connection?.saveData),
      hidden: document.hidden, visible, paused, dialogOpen: dialog.open,
    })
    root.dataset.animated = String(playing)
    if (playing) {
      // No source is assigned at all in reduced-motion/data-saving mode.
      if (!background.getAttribute('src')) background.src = PRECISION_VIDEO
      void background.play().catch(() => {
        // Autoplay denial or CDN failure leaves the local chrome artwork intact.
        if (!disposed) root.dataset.videoReady = 'false'
      })
    } else {
      background.pause()
      if (reduced.matches || connection?.saveData) {
        background.removeAttribute('src')
        background.load()
        root.dataset.videoReady = 'false'
      }
      if (counted) finishCounters()
    }
    if (document.hidden) demo.pause()
    startCounters()
  }
  const hideDialog = () => dialog.close()
  const resetDemo = () => {
    demo.pause()
    demo.removeAttribute('src')
    demo.load()
    root.dataset.demoError = 'false'
    if (!disposed) {
      sync()
      open.focus({ preventScroll: true })
    }
  }
  listen(open, 'click', () => {
    dialog.showModal()
    sync()
    demo.src = PRECISION_VIDEO
    // Explicit user gesture: controls remain usable if playback is rejected.
    void demo.play().catch(() => {})
  })
  listen(close, 'click', hideDialog)
  listen(dialog, 'close', resetDemo)
  listen(dialog, 'click', (event) => {
    const e = event as MouseEvent
    const rect = dialog.getBoundingClientRect()
    if (e.target === dialog && (e.clientX < rect.left || e.clientX > rect.right ||
      e.clientY < rect.top || e.clientY > rect.bottom)) hideDialog()
  })
  listen(toggle, 'click', () => {
    paused = !paused
    toggle.setAttribute('aria-pressed', String(paused))
    root.dataset.userPaused = String(paused)
    sync()
  })
  listen(background, 'playing', () => { root.dataset.videoReady = 'true' })
  listen(background, 'error', () => { root.dataset.videoReady = 'false' })
  listen(demo, 'error', () => { root.dataset.demoError = 'true' })
  listen(document, 'visibilitychange', sync)
  listen(reduced, 'change', sync)
  if (connection) listen(connection, 'change', sync)
  const observer = new IntersectionObserver(([entry]) => {
    visible = entry.isIntersecting
    sync()
  }, { threshold: 0 })
  observer.observe(root)
  sync()
  return () => {
    disposed = true
    observer.disconnect()
    cleanup.forEach((fn) => fn())
    cancelAnimationFrame(frame)
    if (dialog.open) dialog.close()
    background.pause()
    background.removeAttribute('src')
    background.load()
    demo.pause()
    demo.removeAttribute('src')
    demo.load()
  }
}
