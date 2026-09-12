/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { createLightPuzzle, toggleLight } from '@/lib/light-puzzle'
import { cn } from '@/lib/utils'

interface WaitCompanionProps {
  pending: boolean
  taskKey?: string | number
  delayMs?: number
  finishedLabel?: string
  onReturnToTask?: () => void
  className?: string
}

/** Optional activity, with no network, timers during play, rewards, or task side effects. */
export function WaitCompanion({
  taskKey = 'wait',
  ...props
}: WaitCompanionProps) {
  return <WaitRound key={taskKey} {...props} />
}

function WaitRound({
  pending,
  delayMs = 4000,
  finishedLabel,
  onReturnToTask,
  className,
}: Omit<WaitCompanionProps, 'taskKey'>) {
  const { t } = useTranslation()
  const [offered, setOffered] = useState(false)
  const [playing, setPlaying] = useState(false)
  const [round, setRound] = useState(0)
  const [board, setBoard] = useState(() => createLightPuzzle())
  const [moves, setMoves] = useState(0)
  const rootRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const restartRef = useRef<HTMLButtonElement>(null)
  const previousFocusRef = useRef<HTMLElement | null>(null)
  const gameHadFocusRef = useRef(false)
  const solved = board.every((on) => !on)

  useEffect(() => {
    setOffered(false)
    if (!pending) return
    setPlaying(false)
    setRound(0)
    setBoard(createLightPuzzle())
    setMoves(0)
    const timer = window.setTimeout(
      () => setOffered(true),
      Math.max(0, delayMs)
    )
    return () => window.clearTimeout(timer)
  }, [pending, delayMs])

  useLayoutEffect(() => {
    const root = rootRef.current
    return () => {
      const target = previousFocusRef.current
      if (root?.contains(document.activeElement) && target?.isConnected) {
        target.focus({ preventScroll: true })
      }
    }
  }, [playing])

  useLayoutEffect(() => {
    if (
      !pending &&
      playing &&
      gameHadFocusRef.current &&
      document.activeElement === document.body
    ) {
      triggerRef.current?.focus({ preventScroll: true })
    }
  }, [pending, playing])

  useLayoutEffect(() => {
    if (solved && playing && pending) {
      restartRef.current?.focus({ preventScroll: true })
    }
  }, [solved, playing, pending])

  if (!playing && (!pending || !offered)) return null

  const rememberFocus = (target: EventTarget | null) => {
    if (target instanceof HTMLElement && !rootRef.current?.contains(target)) {
      previousFocusRef.current = target
    }
  }
  const close = () => {
    setPlaying(false)
    if (!pending) {
      onReturnToTask?.()
      if (!onReturnToTask && previousFocusRef.current?.isConnected) {
        previousFocusRef.current.focus({ preventScroll: true })
      }
    } else {
      triggerRef.current?.focus({ preventScroll: true })
    }
  }

  return (
    <div
      ref={rootRef}
      className={cn(
        'flex max-w-full flex-col items-start gap-3 py-2',
        className
      )}
      data-testid='wait-companion'
      onFocusCapture={() => {
        gameHadFocusRef.current = true
      }}
      onBlurCapture={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) {
          gameHadFocusRef.current = false
        }
      }}
    >
      {!pending && playing ? (
        <p role='status' className='text-sm font-medium'>
          {finishedLabel ?? t('The wait is over.')}
        </p>
      ) : null}
      <Button
        ref={triggerRef}
        className='min-h-11'
        type='button'
        variant='ghost'
        size='sm'
        onFocus={(event) => rememberFocus(event.relatedTarget)}
        onPointerDown={() => rememberFocus(document.activeElement)}
        onClick={() => (playing ? close() : setPlaying(true))}
        aria-expanded={pending ? playing : undefined}
      >
        {playing
          ? pending
            ? t('Close game')
            : t('Back to task')
          : t('Play while you wait')}
      </Button>
      {playing && pending ? (
        <div className='flex flex-col gap-3'>
          <p className='text-muted-foreground max-w-64 text-xs leading-5'>
            {t('Turn off every dot. Each tap flips its neighbours too.')}
          </p>
          <div
            role='group'
            aria-label={t('Lights out')}
            className='grid w-fit grid-cols-3 gap-1'
          >
            {board.map((on, index) => (
              <button
                key={index}
                type='button'
                className='hover:bg-muted focus-visible:ring-ring flex size-12 items-center justify-center rounded-lg focus-visible:ring-2 focus-visible:outline-none'
                aria-label={t('Dot {{number}}', { number: index + 1 })}
                aria-pressed={on}
                disabled={solved}
                onClick={() => {
                  setBoard((current) => toggleLight(current, index))
                  setMoves((current) => current + 1)
                }}
              >
                <span
                  aria-hidden='true'
                  className={cn(
                    'size-5 rounded-full',
                    on
                      ? 'bg-foreground'
                      : 'border-muted-foreground border bg-transparent'
                  )}
                />
              </button>
            ))}
          </div>
          <div className='flex items-center gap-4 text-xs'>
            <span className='text-muted-foreground tabular-nums'>
              {t('Moves: {{count}}', { count: moves })}
            </span>
            {solved ? <span role='status'>{t('All clear.')}</span> : null}
          </div>
          <Button
            ref={restartRef}
            type='button'
            size='sm'
            variant='ghost'
            className='min-h-11 self-start'
            onClick={() => {
              const next = round + 1
              setRound(next)
              setBoard(createLightPuzzle(next))
              setMoves(0)
            }}
          >
            {t('Another puzzle')}
          </Button>
        </div>
      ) : null}
    </div>
  )
}
