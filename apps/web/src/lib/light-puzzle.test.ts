/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  createLightPuzzle,
  LIGHT_PUZZLE_SEEDS,
  toggleLight,
} from './light-puzzle'

test('each offered board is non-empty and solvable by the inverse seed moves', () => {
  LIGHT_PUZZLE_SEEDS.forEach((moves, round) => {
    const board = createLightPuzzle(round)
    assert.equal(board.length, 9)
    assert.ok(board.some(Boolean))
    assert.ok(
      moves
        .reduce((state, move) => toggleLight(state, move), board)
        .every((on) => !on)
    )
  })
})

test('moves do not wrap across rows or mutate the original board', () => {
  const board = Array<boolean>(9).fill(false)
  assert.deepEqual(toggleLight(board, 2), [
    false,
    true,
    true,
    false,
    false,
    true,
    false,
    false,
    false,
  ])
  assert.ok(board.every((on) => !on))
  for (let i = 0; i < 9; i++) {
    assert.deepEqual(toggleLight(toggleLight(board, i), i), board)
  }
})

test('invalid moves are rejected', () => {
  for (const move of [-1, 9, 0.5, Number.NaN]) {
    assert.throws(() => toggleLight(createLightPuzzle(), move))
  }
})
