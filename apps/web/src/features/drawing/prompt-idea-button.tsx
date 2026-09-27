/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { DiceFaces01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { motion, useReducedMotion } from 'motion/react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Kbd } from '@/components/ui/kbd'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'

import { nextPromptIdea } from './prompt-inspiration'

type PromptIdeaButtonProps = {
  /** Current prompt text; the dice appends rather than overwriting. */
  prompt: string
  onApply: (idea: string) => void
  disabled?: boolean
  className?: string
}

const rollSteps = 5
const rollIntervalMs = 70
const settleDelayMs = 130

function isApplePlatform() {
  return (
    typeof navigator !== 'undefined' &&
    /mac|iphone|ipad/i.test(navigator.userAgent)
  )
}

/**
 * A local-only "lucky prompt" dice. Pressing it flickers through a few
 * fragments, then appends one finished idea to the prompt. Nothing here
 * reaches the network; the fragment pools live in `prompt-inspiration.ts`.
 */
export function PromptIdeaButton({
  prompt,
  onApply,
  disabled,
  className,
}: PromptIdeaButtonProps) {
  const { t } = useTranslation()
  const reduceMotion = useReducedMotion()
  const [rolling, setRolling] = useState(false)
  const [preview, setPreview] = useState<string | null>(null)
  const [rollCount, setRollCount] = useState(0)
  const timersRef = useRef<ReturnType<typeof setTimeout>[]>([])
  const lastIdeaRef = useRef<string | undefined>(undefined)

  const clearTimers = useCallback(() => {
    for (const timer of timersRef.current) clearTimeout(timer)
    timersRef.current = []
  }, [])

  useEffect(() => clearTimers, [clearTimers])

  const commit = useCallback(() => {
    const idea = nextPromptIdea(lastIdeaRef.current)
    lastIdeaRef.current = idea
    setPreview(null)
    setRolling(false)
    setRollCount((count) => count + 1)
    onApply(idea)
  }, [onApply])

  const roll = useCallback(() => {
    if (rolling || disabled) return
    clearTimers()
    if (reduceMotion) {
      // Reduced motion skips the flicker: the text simply changes.
      commit()
      return
    }
    setRolling(true)
    for (let step = 0; step < rollSteps; step += 1) {
      timersRef.current.push(
        setTimeout(() => {
          setPreview(nextPromptIdea(lastIdeaRef.current))
        }, step * rollIntervalMs)
      )
    }
    timersRef.current.push(
      setTimeout(commit, rollSteps * rollIntervalMs + settleDelayMs)
    )
  }, [clearTimers, commit, disabled, reduceMotion, rolling])

  const hasPrompt = prompt.trim().length > 0
  const shortcutLabel = isApplePlatform() ? '⌘I' : 'Ctrl+I'

  // ⌘/Ctrl+I rolls from the keyboard. The listener ships with the button, so
  // the dice and its shortcut can never point at different actions. It stays
  // out of the settings panel and dialogs, where ⌘I means something else.
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.repeat) return
      if (event.key.toLowerCase() !== 'i' || event.defaultPrevented) return
      const target = event.target as HTMLElement | null
      if (target?.closest('[data-slot="drawing-inspector"], [role="dialog"]')) {
        return
      }
      event.preventDefault()
      roll()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [roll])

  const button = (
    <Button
      type='button'
      variant='outline'
      size='sm'
      data-testid='drawing-prompt-idea'
      data-roll-count={rollCount}
      disabled={disabled}
      aria-label={t('Roll a random prompt idea')}
      onClick={roll}
      className={cn(
        'shrink-0 border-white/15 bg-white/5 text-white hover:bg-white/10 hover:text-white',
        className
      )}
    >
      <motion.span
        key={rollCount}
        className='inline-flex'
        aria-hidden='true'
        animate={rolling ? { rotate: [0, -20, 20, -14, 14, 0] } : { rotate: 0 }}
        transition={{ duration: 0.45, ease: 'easeInOut' }}
      >
        <HugeiconsIcon
          icon={DiceFaces01Icon}
          data-icon='inline-start'
          strokeWidth={2}
        />
      </motion.span>
      <span className='hidden sm:inline'>
        {hasPrompt ? t('Roll again') : t('Inspire me')}
      </span>
      <Kbd className='ml-1 hidden bg-white/10 text-white/60 md:inline-flex'>
        {shortcutLabel}
      </Kbd>
    </Button>
  )

  return (
    <div className='flex min-w-0 items-center gap-2'>
      <Tooltip>
        <TooltipTrigger render={button} />
        <TooltipContent>
          {t('Roll a local random prompt idea. Nothing is sent to the server.')}
        </TooltipContent>
      </Tooltip>
      <span
        aria-hidden='true'
        className='hidden min-w-0 flex-1 truncate text-xs text-white/45 lg:block'
      >
        {preview ?? ''}
      </span>
    </div>
  )
}
