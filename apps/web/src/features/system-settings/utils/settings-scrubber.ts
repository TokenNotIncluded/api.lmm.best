/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
export function scrubIndex(start: number, distance: number, count: number) {
  return Math.max(0, Math.min(count - 1, start + Math.round(distance / 32)))
}

/** Only this handle owns the gesture. The directory and page scroll normally. */
export function mountSettingsScrubber(
  handle: HTMLElement,
  start: number,
  count: number,
  preview: (index: number | null) => void,
  select: (index: number) => void,
  open: () => void
) {
  let timer: ReturnType<typeof setTimeout> | undefined
  let pointer: number | null = null
  let origin = 0
  let active = false
  let selected = start
  let suppressClick = false
  const stop = () => {
    clearTimeout(timer)
    timer = undefined
    const captured = pointer
    pointer = null
    active = false
    if (captured !== null && handle.hasPointerCapture?.(captured)) {
      handle.releasePointerCapture(captured)
    }
    preview(null)
  }
  const down = (event: PointerEvent) => {
    if (!event.isPrimary || event.button !== 0 || count < 1) return
    stop()
    suppressClick = false
    pointer = event.pointerId
    origin = event.clientY
    selected = start
    handle.setPointerCapture?.(event.pointerId)
    timer = setTimeout(() => {
      active = true
      suppressClick = true
      preview(selected)
    }, 280)
  }
  const move = (event: PointerEvent) => {
    if (event.pointerId !== pointer) return
    if (!active) {
      if (Math.abs(event.clientY - origin) > 10) {
        suppressClick = true
        stop()
      }
      return
    }
    selected = scrubIndex(start, event.clientY - origin, count)
    preview(selected)
  }
  const up = (event: PointerEvent) => {
    if (event.pointerId !== pointer) return
    const commit = active && selected !== start
    const next = selected
    stop()
    if (commit) select(next)
  }
  const cancel = () => {
    suppressClick = true
    stop()
  }
  const click = (event: MouseEvent) => {
    if (suppressClick && event.detail !== 0) {
      event.preventDefault()
      suppressClick = false
      return
    }
    open()
  }
  handle.addEventListener('pointerdown', down)
  handle.addEventListener('pointermove', move)
  handle.addEventListener('pointerup', up)
  handle.addEventListener('pointercancel', cancel)
  handle.addEventListener('lostpointercapture', stop)
  handle.addEventListener('click', click)
  window.addEventListener('blur', cancel)
  return () => {
    handle.removeEventListener('pointerdown', down)
    handle.removeEventListener('pointermove', move)
    handle.removeEventListener('pointerup', up)
    handle.removeEventListener('pointercancel', cancel)
    handle.removeEventListener('lostpointercapture', stop)
    handle.removeEventListener('click', click)
    window.removeEventListener('blur', cancel)
    stop()
  }
}
