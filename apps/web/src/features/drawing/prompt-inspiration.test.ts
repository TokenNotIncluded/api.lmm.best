/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  applyPromptIdea,
  composePromptIdea,
  drawPromptIdeas,
  nextPromptIdea,
} from './prompt-inspiration'

test('a composed idea is a non-empty multi-part sentence', () => {
  const idea = composePromptIdea(() => 0)
  assert.ok(idea.trim().length > 0)
  assert.ok(idea.includes(','))
  assert.ok(composePromptIdea(() => 0.999).trim().length > 0)
})

test('composePromptIdea is deterministic for a pinned random source', () => {
  assert.equal(
    composePromptIdea(() => 0.5),
    composePromptIdea(() => 0.5)
  )
})

test('the dice never repeats the previous idea', () => {
  const previous = composePromptIdea(() => 0)
  for (const ratio of [0, 0.25, 0.5, 0.75, 0.999]) {
    assert.notEqual(
      nextPromptIdea(previous, () => ratio),
      previous,
      `random=${ratio} repeated the previous idea`
    )
  }
})

test('the dice always returns usable text', () => {
  assert.ok(nextPromptIdea(undefined, () => 1).trim().length > 0)
  assert.ok(nextPromptIdea('', () => 0).trim().length > 0)
})

test('drawPromptIdeas returns the requested number of distinct ideas', () => {
  const ideas = drawPromptIdeas(5, () => 0.42)
  assert.ok(ideas.length <= 5)
  assert.equal(new Set(ideas).size, ideas.length)
  assert.equal(drawPromptIdeas(1).length, 1)
  assert.equal(drawPromptIdeas(0).length, 1)
})

test('applying an idea fills an empty prompt and appends to an existing one', () => {
  assert.equal(applyPromptIdea('   ', 'a whale'), 'a whale')
  assert.equal(applyPromptIdea('', 'a whale'), 'a whale')
  assert.equal(applyPromptIdea('a cat', 'a whale'), 'a cat\na whale')
  assert.equal(applyPromptIdea('a cat\n\n\n', 'a whale'), 'a cat\na whale')
})
