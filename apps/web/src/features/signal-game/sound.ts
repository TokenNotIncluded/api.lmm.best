/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
/**
 * Tiny Web Audio cue player for the Signal path board.
 *
 * Kept deliberately separate from the game store: the store is the source of
 * truth that the WebMCP tools and the leaderboard tests depend on, and sound is
 * a presentational concern that must never be able to break a round. There are
 * no audio assets to download — every cue is synthesised from an oscillator,
 * so the first-load budget is untouched.
 *
 * Autoplay policy: an AudioContext created before a user gesture starts
 * suspended. We only ever build it lazily inside a cue triggered by an actual
 * click, so a visitor who never touches the board allocates nothing at all.
 */

export type Cue = 'turn' | 'power' | 'combo' | 'win'

const STORAGE_KEY = 'lmm-signal-sound'

let context: AudioContext | null = null
let enabled = false
let loaded = false

function preference(): boolean {
  if (typeof window === 'undefined') return false
  try {
    // Off unless the player explicitly opted in on a previous visit.
    return window.localStorage?.getItem(STORAGE_KEY) === 'on'
  } catch {
    return false
  }
}

/** Reads the persisted choice once; safe to call during render. */
export function soundEnabled(): boolean {
  if (!loaded) {
    enabled = preference()
    loaded = true
  }
  return enabled
}

export function setSoundEnabled(next: boolean): void {
  enabled = next
  loaded = true
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(STORAGE_KEY, next ? 'on' : 'off')
  } catch {
    // A blocked storage quota must never break the toggle.
  }
  if (!next) suspend()
}

function audio(): AudioContext | null {
  if (typeof window === 'undefined') return null
  if (context) return context
  const Ctor =
    window.AudioContext ??
    (window as unknown as { webkitAudioContext?: typeof AudioContext })
      .webkitAudioContext
  if (!Ctor) return null
  try {
    context = new Ctor()
  } catch {
    context = null
  }
  return context
}

function suspend(): void {
  if (!context) return
  void context.suspend().catch(() => undefined)
}

/** A single shaped tone. Short attack/decay, no clicks, well under 400ms. */
function tone(
  ctx: AudioContext,
  frequency: number,
  at: number,
  duration: number,
  gain: number,
  type: OscillatorType = 'sine'
): void {
  const oscillator = ctx.createOscillator()
  const envelope = ctx.createGain()
  oscillator.type = type
  oscillator.frequency.value = frequency
  envelope.gain.setValueAtTime(0, at)
  envelope.gain.linearRampToValueAtTime(gain, at + 0.012)
  envelope.gain.exponentialRampToValueAtTime(0.0001, at + duration)
  oscillator.connect(envelope)
  envelope.connect(ctx.destination)
  oscillator.start(at)
  oscillator.stop(at + duration + 0.02)
}

/**
 * Plays a cue when sound is on and the player has not asked for reduced
 * motion — a burst of tones is exactly the kind of surprise that setting is
 * meant to suppress. Failure is always swallowed: a missing or suspended
 * AudioContext can never be allowed to interrupt a move.
 */
export function playCue(cue: Cue): void {
  if (!soundEnabled()) return
  if (
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches
  ) {
    return
  }
  const ctx = audio()
  if (!ctx) return
  if (ctx.state === 'suspended') void ctx.resume().catch(() => undefined)
  try {
    const now = ctx.currentTime
    switch (cue) {
      case 'turn':
        tone(ctx, 320, now, 0.09, 0.06, 'triangle')
        break
      case 'power':
        tone(ctx, 520, now, 0.12, 0.07, 'triangle')
        break
      case 'combo':
        tone(ctx, 660, now, 0.1, 0.08, 'triangle')
        tone(ctx, 880, now + 0.07, 0.12, 0.07, 'triangle')
        break
      case 'win':
        tone(ctx, 523.25, now, 0.16, 0.09, 'triangle')
        tone(ctx, 659.25, now + 0.09, 0.18, 0.08, 'triangle')
        tone(ctx, 783.99, now + 0.18, 0.28, 0.09, 'triangle')
        break
    }
  } catch {
    // Ignore: audio is a garnish, never a requirement.
  }
}
