/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */

export function insideInput(x: number, y: number, rect: DOMRect) {
  return x >= rect.left && x <= rect.right && y >= rect.top && y <= rect.bottom
}

/** A chapter title can be dropped on the input to rebuild its own description. */
export function mountHomeGravity(root: HTMLElement, input: HTMLElement | null) {
  if (!input) return () => {}
  const titles = [...root.querySelectorAll<HTMLElement>('[data-gravity-title]')]
  const animations = new Set<Animation>()
  let generation = 0
  let drag:
    | {
        title: HTMLElement
        pointerId: number
        startX: number
        startY: number
        moved: boolean
      }
    | undefined

  const restore = () => {
    generation += 1
    for (const animation of animations) animation.cancel()
    animations.clear()
    input.removeAttribute('data-drag-over')
  }
  const track = (animation: Animation) => {
    animations.add(animation)
    return animation
  }
  const replay = (title: HTMLElement) => {
    restore()
    const description = title
      .closest('[data-cinema-panel]')
      ?.querySelector<HTMLElement>('[data-gravity-description]')
    if (
      !description ||
      window.matchMedia('(prefers-reduced-motion: reduce)').matches
    ) {
      return
    }
    const glyphs = [
      ...description.querySelectorAll<HTMLElement>('[data-gravity-glyph]'),
    ]
    if (!glyphs.length || typeof glyphs[0].animate !== 'function') return
    const run = generation
    const source = input.getBoundingClientRect()
    const stage = root
      .querySelector<HTMLElement>('[data-cinema-inner]')
      ?.getBoundingClientRect()
    const positions = glyphs.map((glyph) => glyph.getBoundingClientRect())
    const fall = glyphs.map((glyph, index) =>
      track(
        glyph.animate(
          [
            { transform: 'translate3d(0, 0, 0)', opacity: 1 },
            {
              transform: `translate3d(${((index % 7) - 3) * 9}px, ${Math.max(180, (stage?.bottom ?? window.innerHeight) - positions[index].top + 60)}px, 0) rotate(${((index % 5) - 2) * 9}deg)`,
              opacity: 0,
            },
          ],
          {
            duration: 460,
            delay: Math.min(index * 7, 240),
            easing: 'cubic-bezier(0.3, 0, 0.8, 0.35)',
            fill: 'forwards',
          }
        )
      )
    )
    void Promise.all(
      fall.map((animation) => animation.finished.catch(() => {}))
    ).then(() => {
      if (run !== generation) return
      for (const animation of fall) {
        animation.cancel()
        animations.delete(animation)
      }
      const fromX = source.left + source.width / 2
      const fromY = source.top + source.height / 2
      const returning = glyphs.map((glyph, index) => {
        const rect = positions[index]
        return track(
          glyph.animate(
            [
              {
                transform: `translate3d(${fromX - rect.left - rect.width / 2}px, ${fromY - rect.top - rect.height / 2}px, 0) scale(0.35)`,
                opacity: 0,
              },
              { transform: 'translate3d(0, 0, 0) scale(1)', opacity: 1 },
            ],
            {
              duration: 520,
              delay: index * 27,
              easing: 'cubic-bezier(0.18, 0.8, 0.2, 1)',
              fill: 'both',
            }
          )
        )
      })
      void Promise.all(
        returning.map((animation) => animation.finished.catch(() => {}))
      ).then(() => {
        if (run === generation) restore()
      })
    })
  }
  const pointerDown = (event: PointerEvent) => {
    if (event.pointerType === 'mouse' && event.button !== 0) return
    const title = event.currentTarget as HTMLElement
    if (title.closest<HTMLElement>('[data-cinema-panel]')?.inert) return
    drag = {
      title,
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      moved: false,
    }
    title.setPointerCapture?.(event.pointerId)
    title.setAttribute('data-dragging', '')
    title.style.transition = 'none'
  }
  const pointerMove = (event: PointerEvent) => {
    if (!drag || drag.pointerId !== event.pointerId) return
    const dx = event.clientX - drag.startX
    const dy = event.clientY - drag.startY
    if (Math.hypot(dx, dy) < 4 && !drag.moved) return
    drag.moved = true
    drag.title.style.transform = `translate3d(${dx}px, ${dy}px, 0)`
    input.toggleAttribute(
      'data-drag-over',
      insideInput(event.clientX, event.clientY, input.getBoundingClientRect())
    )
  }
  const pointerEnd = (event: PointerEvent) => {
    if (!drag || drag.pointerId !== event.pointerId) return
    const { title, moved } = drag
    const landed =
      event.type !== 'pointercancel' &&
      moved &&
      insideInput(event.clientX, event.clientY, input.getBoundingClientRect())
    title.releasePointerCapture?.(event.pointerId)
    title.style.removeProperty('transition')
    title.style.removeProperty('transform')
    title.removeAttribute('data-dragging')
    title.blur()
    input.removeAttribute('data-drag-over')
    drag = undefined
    if (landed) replay(title)
    else restore()
  }
  const keyDown = (event: KeyboardEvent) => {
    if (event.key !== 'Enter' && event.key !== ' ') return
    event.preventDefault()
    replay(event.currentTarget as HTMLElement)
  }
  for (const title of titles) {
    title.addEventListener('pointerdown', pointerDown)
    title.addEventListener('pointermove', pointerMove)
    title.addEventListener('pointerup', pointerEnd)
    title.addEventListener('pointercancel', pointerEnd)
    title.addEventListener('keydown', keyDown)
  }
  return () => {
    restore()
    if (drag) {
      drag.title.style.removeProperty('transition')
      drag.title.style.removeProperty('transform')
      drag.title.removeAttribute('data-dragging')
    }
    for (const title of titles) {
      title.removeEventListener('pointerdown', pointerDown)
      title.removeEventListener('pointermove', pointerMove)
      title.removeEventListener('pointerup', pointerEnd)
      title.removeEventListener('pointercancel', pointerEnd)
      title.removeEventListener('keydown', keyDown)
    }
  }
}
