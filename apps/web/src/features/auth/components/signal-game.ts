/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
export const GRID_SIZE = 5
export const INPUT_TILE = 10
export const OUTPUT_TILE = 14
export const RULES_VERSION = 2
export const BOARD_SIZES = [5, 8, 12, 24, 48, 64] as const
export const PORTS = [1, 2, 4, 8] as const
const SHAPES = [3, 6, 12, 9, 5, 10]
export type Circuit = {
  size: number
  seed: number
  tiles: number[]
  solution: number[]
  route: number[]
}
export const inputTile = (size: number) => Math.floor(size / 2) * size
export const outputTile = (size: number) => inputTile(size) + size - 1
export const maxMoves = (size: number) => Math.max(400, size * size * 16)
export const rotateTile = (mask: number) => ((mask << 1) & 15) | (mask >> 3)
export const opposite = (port: number) => ((port << 2) | (port >> 2)) & 15
export function neighbor(
  index: number,
  port: number,
  size = GRID_SIZE
): number | null {
  const row = Math.floor(index / size),
    col = index % size
  if (port === 1) return row > 0 ? index - size : null
  if (port === 2) return col < size - 1 ? index + 1 : null
  if (port === 4) return row < size - 1 ? index + size : null
  return col > 0 ? index - 1 : null
}
export function traceCircuit(tiles: readonly number[], size = GRID_SIZE) {
  const path: number[] = []
  let index = inputTile(size),
    incoming = 8
  const seen = new Set<number>()
  while (!seen.has(index) && tiles[index] & incoming) {
    seen.add(index)
    path.push(index)
    const outgoing = PORTS.find(
      (port) => port !== incoming && tiles[index] & port
    )
    if (!outgoing) break
    if (index === outputTile(size) && outgoing === 2) return { path, won: true }
    const next = neighbor(index, outgoing, size)
    if (next === null) break
    index = next
    incoming = opposite(outgoing)
  }
  return { path, won: false }
}
export function createCircuit(
  seed = Math.floor(Math.random() * 0xffffffff),
  size = GRID_SIZE
): Circuit {
  if (!(BOARD_SIZES as readonly number[]).includes(size)) {
    throw new Error('Unsupported board size')
  }
  let state = seed >>> 0
  const random = () => {
    state = (Math.imul(state, 1664525) + 1013904223) >>> 0
    return state / 0x100000000
  }
  const shuffled = () => {
    const result: number[] = [...PORTS]
    for (let i = 3; i > 0; i--) {
      const j = Math.floor(random() * (i + 1))
      ;[result[i], result[j]] = [result[j], result[i]]
    }
    return result
  }
  // Iterative depth-first traversal visits each cell at most once, even on 64 × 64 boards.
  const visited = new Set<number>([inputTile(size)])
  const stack = [{ index: inputTile(size), ports: shuffled(), next: 0 }]
  let route: number[] = []
  while (stack.length) {
    const current = stack.at(-1)
    if (!current) break
    if (current.index === outputTile(size)) {
      route = stack.map((item) => item.index)
      break
    }
    if (current.next === 4) {
      stack.pop()
      continue
    }
    const next = neighbor(current.index, current.ports[current.next++], size)
    if (
      next === null ||
      visited.has(next) ||
      (next === outputTile(size) && stack.length < 2 * size - 2)
    ) {
      continue
    }
    visited.add(next)
    stack.push({ index: next, ports: shuffled(), next: 0 })
  }
  if (!route.length) {
    for (let row = Math.floor(size / 2); row >= 0; row--) route.push(row * size)
    for (let col = 1; col < size; col++) route.push(col)
    for (let row = 1; row <= Math.floor(size / 2); row++) {
      route.push(row * size + size - 1)
    }
  }
  const solution = Array.from(
    { length: size * size },
    () => SHAPES[Math.floor(random() * SHAPES.length)]
  )
  route.forEach((index, i) => {
    const before =
      i === 0
        ? 8
        : PORTS.find((port) => neighbor(index, port, size) === route[i - 1])
    const after =
      i === route.length - 1
        ? 2
        : PORTS.find((port) => neighbor(index, port, size) === route[i + 1])
    if (before === undefined || after === undefined) {
      throw new Error('Invalid generated route')
    }
    solution[index] = before | after
  })
  const tiles = solution.map((mask) => {
    let turns = Math.floor(random() * 4)
    while (turns-- > 0) mask = rotateTile(mask)
    return mask
  })
  while (traceCircuit(tiles, size).won) {
    tiles[inputTile(size)] = rotateTile(tiles[inputTile(size)])
  }
  return { size, seed: seed >>> 0, tiles, solution, route }
}
export function hintedTile(circuit: Circuit, tiles: readonly number[]) {
  return (
    circuit.route.find((index) => tiles[index] !== circuit.solution[index]) ??
    null
  )
}
