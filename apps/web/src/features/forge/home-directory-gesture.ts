/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
const WHEEL_DISTANCE = 240
const TOUCH_DISTANCE = 72
const IDLE_MS = 700

/** Normalize mouse wheels without letting one large wheel tick open a page. */
export function directoryWheelDistance(
  deltaY: number,
  deltaMode: number,
  viewportHeight: number
): number {
  if (!Number.isFinite(deltaY)) return 0
  const unit = deltaMode === 1 ? 16 : deltaMode === 2 ? viewportHeight : 1
  return Math.sign(-deltaY) * Math.min(80, Math.abs(deltaY * unit))
}

export function directoryProgress(
  distance: number,
  threshold: number
): number {
  if (
    !Number.isFinite(distance) ||
    !Number.isFinite(threshold) ||
    threshold <= 0
  ) {
    return 0
  }
  return Math.max(0, Math.min(1, distance / threshold))
}

type DirectoryGestureOptions = {
  onProgress: (progress: number) => void
  onNavigate: () => void | Promise<unknown>
}

/** At the page top, scroll up; on the entry itself, swipe up. Else scroll normally. */
export function attachDirectoryGesture(
  entry: HTMLElement,
  { onProgress, onNavigate }: DirectoryGestureOptions
): () => void {
  const doc = entry.ownerDocument
  const win = doc.defaultView
  if (!win) return () => {}

  let distance = 0
  let timer = 0
  let frame = 0
  let committed = false
  let disposed = false
  let touch: { id: number; x: number; y: number; moved: boolean } | null = null
  let suppressClickUntil = 0

  const reset = () => {
    if (committed || disposed) return
    win.clearTimeout(timer)
    win.cancelAnimationFrame(frame)
    timer = 0
    frame = 0
    distance = 0
    touch = null
    onProgress(0)
  }

  const available = () => {
    const rect = entry.getBoundingClientRect()
    return (
      !disposed &&
      !committed &&
      !doc.hidden &&
      Math.max(win.scrollY, doc.scrollingElement?.scrollTop ?? 0) <= 2 &&
      rect.bottom > 0 &&
      rect.top < win.innerHeight &&
      win.getComputedStyle(doc.body).overflowY !== 'hidden' &&
      !doc.querySelector(
        '[role="dialog"][aria-modal="true"]:not([aria-hidden="true"])'
      )
    )
  }

  const finish = () => {
    if (committed || disposed) return
    committed = true
    touch = null
    win.clearTimeout(timer)
    onProgress(1)
    // Paint the completed bar before changing the route. Cleanup cancels both frames.
    frame = win.requestAnimationFrame(() => {
      frame = win.requestAnimationFrame(() => {
        if (disposed) return
        void Promise.resolve()
          .then(() => {
            if (!disposed) return onNavigate()
          })
          .catch(() => {
            if (disposed) return
            committed = false
            reset()
          })
      })
    })
  }

  const ignoresTarget = (target: EventTarget | null) => {
    if (!(target instanceof win.HTMLElement)) return true
    if (entry.contains(target)) return false
    if (
      target.closest(
        'a, button, input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="menu"], [role="listbox"]'
      )
    ) {
      return true
    }
    for (
      let node: HTMLElement | null = target;
      node && node !== doc.body;
      node = node.parentElement
    ) {
      if (
        node.scrollHeight > node.clientHeight &&
        /auto|scroll|overlay/.test(win.getComputedStyle(node).overflowY)
      ) {
        return true
      }
    }
    return false
  }

  const onWheel = (event: WheelEvent) => {
    if (
      event.defaultPrevented ||
      !event.cancelable ||
      event.ctrlKey ||
      event.metaKey ||
      event.altKey ||
      event.shiftKey ||
      Math.abs(event.deltaX) > Math.abs(event.deltaY) ||
      ignoresTarget(event.target)
    ) {
      return
    }
    if (!available() || event.deltaY >= 0) {
      reset()
      return
    }
    const delta = directoryWheelDistance(
      event.deltaY,
      event.deltaMode,
      win.innerHeight
    )
    if (delta <= 0) return
    event.preventDefault()
    touch = null
    distance += delta
    onProgress(directoryProgress(distance, WHEEL_DISTANCE))
    win.clearTimeout(timer)
    if (distance >= WHEEL_DISTANCE) finish()
    else timer = win.setTimeout(reset, IDLE_MS)
  }

  const onTouchStart = (event: TouchEvent) => {
    reset()
    if (event.touches.length !== 1 || !available()) return
    const point = event.touches[0]
    touch = {
      id: point.identifier,
      x: point.clientX,
      y: point.clientY,
      moved: false,
    }
  }

  const onTouchMove = (event: TouchEvent) => {
    if (!touch) return
    if (event.touches.length !== 1 || !available()) {
      reset()
      return
    }
    const point = Array.from(event.touches).find(
      (item) => item.identifier === touch?.id
    )
    if (!point) {
      reset()
      return
    }
    const dy = touch.y - point.clientY
    const dx = point.clientX - touch.x
    if (Math.abs(dx) > Math.max(10, Math.abs(dy)) || dy < 0) {
      reset()
      return
    }
    if (dy < 10 && !touch.moved) return
    if (!event.cancelable) {
      reset()
      return
    }
    event.preventDefault()
    touch.moved = true
    suppressClickUntil = Date.now() + 500
    distance = dy
    onProgress(directoryProgress(distance, TOUCH_DISTANCE))
  }

  const onTouchEnd = (event: TouchEvent) => {
    if (!touch) return
    const ended = Array.from(event.changedTouches).some(
      (point) => point.identifier === touch?.id
    )
    if (!ended) return
    if (distance >= TOUCH_DISTANCE && available()) finish()
    else reset()
  }

  const onClick = (event: MouseEvent) => {
    if (event.detail > 0 && Date.now() < suppressClickUntil) {
      event.preventDefault()
      event.stopPropagation()
    }
  }
  const onScroll = () => {
    if (win.scrollY > 2) reset()
  }

  win.addEventListener('wheel', onWheel, { passive: false })
  win.addEventListener('scroll', onScroll, { passive: true })
  win.addEventListener('blur', reset)
  doc.addEventListener('visibilitychange', reset)
  entry.addEventListener('touchstart', onTouchStart, { passive: true })
  entry.addEventListener('touchmove', onTouchMove, { passive: false })
  entry.addEventListener('touchend', onTouchEnd, { passive: true })
  entry.addEventListener('touchcancel', reset, { passive: true })
  entry.addEventListener('click', onClick, true)

  return () => {
    disposed = true
    win.clearTimeout(timer)
    win.cancelAnimationFrame(frame)
    win.removeEventListener('wheel', onWheel)
    win.removeEventListener('scroll', onScroll)
    win.removeEventListener('blur', reset)
    doc.removeEventListener('visibilitychange', reset)
    entry.removeEventListener('touchstart', onTouchStart)
    entry.removeEventListener('touchmove', onTouchMove)
    entry.removeEventListener('touchend', onTouchEnd)
    entry.removeEventListener('touchcancel', reset)
    entry.removeEventListener('click', onClick, true)
  }
}
