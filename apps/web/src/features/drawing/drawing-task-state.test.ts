/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { beforeEach, describe, test } from 'node:test'

import {
  clearActiveDrawingTask,
  clearDrawingPromptDraft,
  getActiveDrawingTask,
  getDrawingDraft,
  getDrawingMinigamePref,
  registerActiveDrawingTask,
  saveDrawingDraft,
  setDrawingMinigamePref,
  subscribeActiveDrawingTask,
  updateActiveDrawingTask,
  type ActiveDrawingTask,
} from './drawing-task-state'

describe('drawing-task-state', () => {
  beforeEach(() => {
    // Clear mock or browser localStorage
    if (typeof window !== 'undefined' && window.localStorage) {
      window.localStorage.clear()
    }
  })

  test('saves and recovers drawing draft per user', () => {
    saveDrawingDraft(101, {
      prompt: 'A futuristic city in neon rain',
      group: 'image-2',
      model: 'image-2',
      size: '1024x1024',
      quality: 'hd',
      count: '2',
    })

    const draftUser101 = getDrawingDraft(101)
    assert.equal(draftUser101.prompt, 'A futuristic city in neon rain')
    assert.equal(draftUser101.group, 'image-2')
    assert.equal(draftUser101.model, 'image-2')
    assert.equal(draftUser101.size, '1024x1024')
    assert.equal(draftUser101.quality, 'hd')
    assert.equal(draftUser101.count, '2')

    // Different user has empty draft
    const draftUser102 = getDrawingDraft(102)
    assert.equal(draftUser102.prompt, '')
    assert.equal(draftUser102.group, undefined)

    // Clear prompt draft preserves other configuration
    clearDrawingPromptDraft(101)
    const afterClear = getDrawingDraft(101)
    assert.equal(afterClear.prompt, '')
    assert.equal(afterClear.group, 'image-2')
  })

  test('manages minigame preference', () => {
    assert.equal(getDrawingMinigamePref(), true)
    setDrawingMinigamePref(false)
    assert.equal(getDrawingMinigamePref(), false)
    setDrawingMinigamePref(true)
    assert.equal(getDrawingMinigamePref(), true)
  })

  test('tracks active task lifecycle and subscriber notifications', () => {
    const userId = 202
    let notifiedTask: ActiveDrawingTask | null = null
    const unsubscribe = subscribeActiveDrawingTask(userId, (task) => {
      notifiedTask = task
    })

    const abortController = new AbortController()
    const task: ActiveDrawingTask = {
      id: 'task-abc',
      userId,
      prompt: 'Cyberpunk cat',
      group: 'image-2',
      model: 'image-2',
      count: '1',
      referenceCount: 0,
      startedAt: Date.now(),
      abortController,
      status: 'generating',
    }

    registerActiveDrawingTask(task)
    assert.ok(notifiedTask)
    assert.equal((notifiedTask as ActiveDrawingTask | null)?.id, 'task-abc')
    assert.equal(
      (notifiedTask as ActiveDrawingTask | null)?.status,
      'generating'
    )
    assert.equal(getActiveDrawingTask(userId)?.id, 'task-abc')

    updateActiveDrawingTask(userId, {
      status: 'failed',
      error: 'Upstream timeout',
      errorStatus: 504,
    })
    assert.equal((notifiedTask as ActiveDrawingTask | null)?.status, 'failed')
    assert.equal(
      (notifiedTask as ActiveDrawingTask | null)?.error,
      'Upstream timeout'
    )
    assert.equal((notifiedTask as ActiveDrawingTask | null)?.errorStatus, 504)

    clearActiveDrawingTask(userId, 'task-abc')
    assert.equal(notifiedTask, null)
    assert.equal(getActiveDrawingTask(userId), null)

    unsubscribe()
  })
})
