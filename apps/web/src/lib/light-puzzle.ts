/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
export const LIGHT_GRID_SIZE = 3
export const LIGHT_PUZZLE_SEEDS = [
  [0, 4, 8],
  [1, 3, 7],
  [0, 2, 6, 8],
  [2, 3, 4, 7],
] as const

export function toggleLight(
  board: readonly boolean[],
  index: number
): boolean[] {
  if (
    board.length !== 9 ||
    !Number.isInteger(index) ||
    index < 0 ||
    index >= 9
  ) {
    throw new RangeError('Invalid light puzzle move')
  }
  const row = Math.floor(index / LIGHT_GRID_SIZE)
  const column = index % LIGHT_GRID_SIZE
  return board.map((on, cell) => {
    const distance =
      Math.abs(Math.floor(cell / LIGHT_GRID_SIZE) - row) +
      Math.abs((cell % LIGHT_GRID_SIZE) - column)
    return distance <= 1 ? !on : on
  })
}

/** Start from a solved board and apply known reversible moves: every board is solvable. */
export function createLightPuzzle(round = 0): boolean[] {
  const seed =
    LIGHT_PUZZLE_SEEDS[
      Math.abs(Math.trunc(round)) % LIGHT_PUZZLE_SEEDS.length
    ] ?? LIGHT_PUZZLE_SEEDS[0]
  return seed.reduce<boolean[]>(
    (board, move) => toggleLight(board, move),
    Array<boolean>(9).fill(false)
  )
}
