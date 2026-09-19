/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  createCircuit,
  hintedTile,
  INPUT_TILE,
  PORTS,
  rotateTile,
  traceCircuit,
} from './signal-game'

test('generated circuits start unsolved and can always be solved by rotations', () => {
  for (let seed = 0; seed < 1000; seed++) {
    const circuit = createCircuit(seed)
    assert.equal(traceCircuit(circuit.tiles).won, false, `initial seed ${seed}`)
    assert.equal(
      traceCircuit(circuit.solution).won,
      true,
      `solution seed ${seed}`
    )
    assert.equal(new Set(circuit.route).size, circuit.route.length)
    assert.ok(circuit.route.length >= 9)
    for (let i = 0; i < circuit.tiles.length; i++) {
      const variants = [circuit.tiles[i]]
      for (let turn = 0; turn < 3; turn++) {
        variants.push(rotateTile(variants.at(-1) ?? circuit.tiles[i]))
      }
      assert.ok(variants.includes(circuit.solution[i]))
      assert.equal(PORTS.filter((port) => circuit.tiles[i] & port).length, 2)
    }
    const board = [...circuit.tiles]
    for (let step = 0; step < 76 && !traceCircuit(board).won; step++) {
      const index = hintedTile(circuit, board)
      assert.ok(index !== null)
      board[index] = rotateTile(board[index])
    }
    assert.equal(traceCircuit(board).won, true, `hint seed ${seed}`)
  }
})

test('signal stops at a disconnected entry or a closed loop', () => {
  const tiles = Array(25).fill(5)
  assert.deepEqual(traceCircuit(tiles), { path: [], won: false })
  tiles[INPUT_TILE] = 10
  tiles[11] = 12
  tiles[16] = 9
  assert.equal(traceCircuit(tiles).won, false)
  assert.ok(traceCircuit(tiles).path.length <= 25)
})

test('four rotations restore a tile and equal seeds reproduce the same round', () => {
  for (const mask of [3, 6, 12, 9, 5, 10]) {
    assert.equal(rotateTile(rotateTile(rotateTile(rotateTile(mask)))), mask)
  }
  assert.deepEqual(createCircuit(42), createCircuit(42))
})
