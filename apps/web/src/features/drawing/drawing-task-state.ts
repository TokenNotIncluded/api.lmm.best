/*
Copyright (C) 2026 LIghtJUNction
*/

export type DrawingDraft = {
  prompt: string
  group?: string
  model?: string
  size?: string
  quality?: string
  count?: string
}

export type ActiveDrawingTask = {
  id: string
  userId: number
  prompt: string
  group: string
  model: string
  size?: string
  quality?: string
  count: string
  referenceCount: number
  startedAt: number
  abortController?: AbortController
  promise?: Promise<unknown>
  status: 'generating' | 'stopped' | 'failed' | 'completed'
  error?: string | null
  errorStatus?: number | null
}

const activeDrawingTasks = new Map<number, ActiveDrawingTask>()
const activeTaskListeners = new Map<
  number,
  Set<(task: ActiveDrawingTask | null) => void>
>()

class MemoryStorage implements Storage {
  private store = new Map<string, string>()
  get length() {
    return this.store.size
  }
  clear() {
    this.store.clear()
  }
  getItem(key: string): string | null {
    return this.store.get(key) ?? null
  }
  key(index: number): string | null {
    return Array.from(this.store.keys())[index] ?? null
  }
  removeItem(key: string): void {
    this.store.delete(key)
  }
  setItem(key: string, value: string): void {
    this.store.set(key, String(value))
  }
}

const fallbackStorage = new MemoryStorage()

export function getDrawingStorage(): Storage {
  try {
    if (typeof window !== 'undefined' && window.localStorage) {
      return window.localStorage
    }
  } catch {
    // Access denied
  }
  return fallbackStorage
}

function getStorageKey(userId: number): string {
  return `lmm_drawing_draft_${userId}`
}

export function getDrawingDraft(userId: number): DrawingDraft {
  const storage = getDrawingStorage()
  try {
    const raw = storage.getItem(getStorageKey(userId))
    if (!raw) return { prompt: '' }
    const parsed = JSON.parse(raw) as Partial<DrawingDraft>
    return {
      prompt: typeof parsed.prompt === 'string' ? parsed.prompt : '',
      group: typeof parsed.group === 'string' ? parsed.group : undefined,
      model: typeof parsed.model === 'string' ? parsed.model : undefined,
      size: typeof parsed.size === 'string' ? parsed.size : undefined,
      quality: typeof parsed.quality === 'string' ? parsed.quality : undefined,
      count: typeof parsed.count === 'string' ? parsed.count : undefined,
    }
  } catch {
    return { prompt: '' }
  }
}

export function saveDrawingDraft(
  userId: number,
  patch: Partial<DrawingDraft>
): void {
  const storage = getDrawingStorage()
  try {
    const current = getDrawingDraft(userId)
    const next: DrawingDraft = {
      ...current,
      ...patch,
    }
    storage.setItem(getStorageKey(userId), JSON.stringify(next))
  } catch {
    // Local storage quota or security restrictions ignored safely
  }
}

export function clearDrawingPromptDraft(userId: number): void {
  const storage = getDrawingStorage()
  try {
    const current = getDrawingDraft(userId)
    storage.setItem(
      getStorageKey(userId),
      JSON.stringify({ ...current, prompt: '' })
    )
  } catch {
    // Ignore
  }
}

const MINIGAME_KEY = 'lmm_drawing_minigame_enabled'

export function getDrawingMinigamePref(): boolean {
  const storage = getDrawingStorage()
  try {
    const val = storage.getItem(MINIGAME_KEY)
    return val !== 'false'
  } catch {
    return true
  }
}

export function setDrawingMinigamePref(enabled: boolean): void {
  const storage = getDrawingStorage()
  try {
    storage.setItem(MINIGAME_KEY, enabled ? 'true' : 'false')
  } catch {
    // Ignore
  }
}

function notifyListeners(userId: number) {
  const current = activeDrawingTasks.get(userId) ?? null
  const listeners = activeTaskListeners.get(userId)
  if (listeners) {
    for (const listener of listeners) {
      try {
        listener(current)
      } catch {
        // Prevent listener failures from breaking other listeners
      }
    }
  }
}

export function registerActiveDrawingTask(task: ActiveDrawingTask): void {
  activeDrawingTasks.set(task.userId, task)
  notifyListeners(task.userId)
}

export function getActiveDrawingTask(userId: number): ActiveDrawingTask | null {
  return activeDrawingTasks.get(userId) ?? null
}

export function updateActiveDrawingTask(
  userId: number,
  patch: Partial<ActiveDrawingTask>
): void {
  const existing = activeDrawingTasks.get(userId)
  if (!existing) return
  const updated: ActiveDrawingTask = { ...existing, ...patch }
  activeDrawingTasks.set(userId, updated)
  notifyListeners(userId)
}

export function clearActiveDrawingTask(userId: number, taskId?: string): void {
  const existing = activeDrawingTasks.get(userId)
  if (!existing) return
  if (taskId && existing.id !== taskId) return
  activeDrawingTasks.delete(userId)
  notifyListeners(userId)
}

export function subscribeActiveDrawingTask(
  userId: number,
  listener: (task: ActiveDrawingTask | null) => void
): () => void {
  let listeners = activeTaskListeners.get(userId)
  if (!listeners) {
    listeners = new Set()
    activeTaskListeners.set(userId, listeners)
  }
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
    if (listeners.size === 0) {
      activeTaskListeners.delete(userId)
    }
  }
}

export function resetDrawingTaskState(): void {
  activeDrawingTasks.clear()
  activeTaskListeners.clear()
  fallbackStorage.clear()
  try {
    if (typeof window !== 'undefined' && window.localStorage) {
      window.localStorage.clear()
    }
  } catch {
    // Ignored
  }
}
