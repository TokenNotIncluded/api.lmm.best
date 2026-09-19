/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

import { createCircuit } from '@/features/auth/components/signal-game'
test('browser generation matches the immutable cross-language rules fixture', () => {
  const rows = JSON.parse(
    readFileSync(
      new URL(
        '../../../../../contracts/signal-game/v2-golden.json',
        import.meta.url
      ),
      'utf8'
    )
  ) as {
    size: number
    seed: number
    tiles_sha256: string
    solution_sha256: string
    route_length: number
  }[]
  const digest = (values: number[]) =>
    createHash('sha256').update(Uint8Array.from(values)).digest('hex')
  for (const row of rows) {
    const c = createCircuit(row.seed, row.size)
    assert.equal(digest(c.tiles), row.tiles_sha256)
    assert.equal(digest(c.solution), row.solution_sha256)
    assert.equal(c.route.length, row.route_length)
  }
})
