/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import {
  parseAssistantUIPreferenceAction,
  uiPreferenceUndoPatch,
  type AssistantUIPreferenceAction,
  type UIPreferencePatch,
  type UIPreferences,
} from './assistant-ui-preferences-contract'

export type UIPreferenceOwner = { userID: number; sessionID: string }
export type UIPreferenceAdapter = {
  owner: () => UIPreferenceOwner | undefined
  read: () => UIPreferences
  save: (patch: UIPreferencePatch, signal: AbortSignal) => Promise<void>
  preview: (patch: UIPreferencePatch) => () => void
}
export type UIPreferenceReceipt = {
  status: 'applying' | 'applied' | 'previewing' | 'restored' | 'unchanged' | 'failed' | 'unavailable'
  canUndo: boolean
}

type Undo = {
  id: string
  owner: UIPreferenceOwner
  before: UIPreferences
  patch: UIPreferencePatch
  stop?: () => void
}
const sameOwner = (a: UIPreferenceOwner | undefined, b: UIPreferenceOwner) =>
  a?.userID === b.userID && a.sessionID === b.sessionID

export function createUIPreferenceRuntime(
  now = () => Date.now(),
  schedule = (callback: () => void, delay: number): (() => void) => {
    const timer = setTimeout(callback, delay)
    return () => clearTimeout(timer)
  }
) {
  const records = new Map<string, { expires: number; receipt: UIPreferenceReceipt }>()
  const listeners = new Set<() => void>()
  let lastUndo: Undo | undefined
  let busy = false
  let lastPreviewAt = Number.NEGATIVE_INFINITY

  const publish = (id: string, receipt: UIPreferenceReceipt) => {
    const entry = records.get(id)
    if (entry) {
      entry.receipt = receipt
      for (const listener of listeners) listener()
    }
    return receipt
  }
  const discardUndo = () => {
    const previous = lastUndo
    lastUndo = undefined
    previous?.stop?.()
    if (previous) {
      const receipt = records.get(previous.id)?.receipt
      if (receipt) publish(previous.id, { ...receipt, canUndo: false })
    }
  }

  async function undo(
    adapter: UIPreferenceAdapter,
    signal: AbortSignal,
    expectedID?: string
  ): Promise<UIPreferenceReceipt> {
    const saved = lastUndo
    if (busy || signal.aborted || !saved ||
        (expectedID !== undefined && saved.id !== expectedID) ||
        !sameOwner(adapter.owner(), saved.owner)) {
      return { status: 'unchanged', canUndo: false }
    }
    lastUndo = undefined
    if (saved.stop) {
      saved.stop()
      return publish(saved.id, { status: 'restored', canUndo: false })
    }
    const patch = uiPreferenceUndoPatch(saved.before, saved.patch, adapter.read())
    if (Object.keys(patch).length === 0) {
      return publish(saved.id, { status: 'unchanged', canUndo: false })
    }
    busy = true
    publish(saved.id, { status: 'applying', canUndo: false })
    try {
      await adapter.save(patch, signal)
      if (signal.aborted || !sameOwner(adapter.owner(), saved.owner)) throw new Error('session changed')
      return publish(saved.id, { status: 'restored', canUndo: false })
    } catch {
      // Never automatically repeat an uncertain profile write.
      return publish(saved.id, { status: 'failed', canUndo: false })
    } finally {
      busy = false
    }
  }

  async function run(
    value: AssistantUIPreferenceAction,
    adapter: UIPreferenceAdapter,
    signal: AbortSignal
  ): Promise<UIPreferenceReceipt> {
    const action = parseAssistantUIPreferenceAction(value)
    const unavailable: UIPreferenceReceipt = { status: 'unavailable', canUndo: false }
    if (!action) return unavailable
    // Keep IDs until their server expiry: eviction must never enable a replay.
    for (const [id, entry] of records) {
      if (entry.expires < now()) records.delete(id)
    }
    const owner = { userID: action.actor_user_id, sessionID: action.actor_session_id }
    if (signal.aborted || !sameOwner(adapter.owner(), owner) ||
        action.expires_at * 1000 <= now() ||
        action.expires_at * 1000 > now() + 135_000) return unavailable
    const existing = records.get(action.action_id)
    if (existing) return existing.receipt
    if (busy || records.size >= 128) return unavailable
    records.set(action.action_id, { expires: action.expires_at * 1000, receipt: unavailable })
    if (action.tool === 'restore_ui_preferences') {
      return publish(action.action_id, await undo(adapter, signal))
    }
    if (action.temporary && now() - lastPreviewAt < 15_000) {
      return publish(action.action_id, unavailable)
    }
    busy = true
    publish(action.action_id, { status: 'applying', canUndo: false })
    try {
      discardUndo()
      const before = adapter.read()
      const patch = Object.fromEntries(
        Object.entries(action.preview).filter(([key, selected]) => before[key as keyof UIPreferences] !== selected)
      ) as UIPreferencePatch
      if (Object.keys(patch).length === 0) {
        return publish(action.action_id, { status: 'unchanged', canUndo: false })
      }
      if (action.temporary) {
        lastPreviewAt = now()
        const release = adapter.preview(patch)
        let stopped = false
        let cancelTimer = () => {}
        const stop = () => {
          if (stopped) return
          stopped = true
          cancelTimer()
          signal.removeEventListener('abort', stop)
          release()
          if (lastUndo?.id === action.action_id) lastUndo = undefined
          publish(action.action_id, { status: 'restored', canUndo: false })
        }
        lastUndo = { id: action.action_id, owner, before, patch, stop }
        signal.addEventListener('abort', stop, { once: true })
        cancelTimer = schedule(stop, 8_000)
        if (signal.aborted) {
          stop()
          return records.get(action.action_id)?.receipt ?? unavailable
        }
        return publish(action.action_id, { status: 'previewing', canUndo: true })
      }
      await adapter.save(patch, signal)
      if (signal.aborted || !sameOwner(adapter.owner(), owner)) throw new Error('session changed')
      lastUndo = { id: action.action_id, owner, before, patch }
      return publish(action.action_id, { status: 'applied', canUndo: true })
    } catch {
      return publish(action.action_id, { status: 'failed', canUndo: false })
    } finally {
      busy = false
    }
  }

  return {
    run,
    undo,
    receipt: (id: string) => records.get(id)?.receipt,
    subscribe: (listener: () => void) => {
      listeners.add(listener)
      return () => { listeners.delete(listener) }
    },
  }
}

// One instance across the page prevents mobile/desktop mounts and repeated
// stream delivery from applying the same action twice. Nothing is persisted.
export const assistantUIPreferenceRuntime = createUIPreferenceRuntime()
