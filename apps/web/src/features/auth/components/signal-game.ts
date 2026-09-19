/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
export const GRID_SIZE = 5
export const INPUT_TILE = 10
export const OUTPUT_TILE = 14
export const PORTS = [1, 2, 4, 8] as const // north, east, south, west
const SHAPES = [3, 6, 12, 9, 5, 10]
export type Circuit = { tiles: number[]; solution: number[]; route: number[] }
export const rotateTile = (mask: number) => ((mask << 1) & 15) | (mask >> 3)
export const opposite = (port: number) => ((port << 2) | (port >> 2)) & 15
export function neighbor(index: number, port: number): number | null {
  const row = Math.floor(index / GRID_SIZE),
    col = index % GRID_SIZE
  if (port === 1) return row > 0 ? index - GRID_SIZE : null
  if (port === 2) return col < GRID_SIZE - 1 ? index + 1 : null
  if (port === 4) return row < GRID_SIZE - 1 ? index + GRID_SIZE : null
  return col > 0 ? index - 1 : null
}
export function traceCircuit(tiles: readonly number[]) {
  const path: number[] = []
  let index = INPUT_TILE,
    incoming = 8
  const seen = new Set<number>()
  while (!seen.has(index) && tiles[index] & incoming) {
    seen.add(index)
    path.push(index)
    const outgoing = PORTS.find(
      (port) => port !== incoming && tiles[index] & port
    )
    if (!outgoing) break
    if (index === OUTPUT_TILE && outgoing === 2) return { path, won: true }
    const next = neighbor(index, outgoing)
    if (next === null) break
    index = next
    incoming = opposite(outgoing)
  }
  return { path, won: false }
}
export function createCircuit(
  seed = Math.floor(Math.random() * 0xffffffff)
): Circuit {
  let state = seed >>> 0
  const random = () => {
    state = (Math.imul(state, 1664525) + 1013904223) >>> 0
    return state / 0x100000000
  }
  const shuffle = <T>(items: readonly T[]) => {
    const result = [...items]
    for (let i = result.length - 1; i > 0; i--) {
      const j = Math.floor(random() * (i + 1))
      ;[result[i], result[j]] = [result[j], result[i]]
    }
    return result
  }
  let budget = 1500
  const visited = new Set<number>()
  const search = (index: number, route: number[]): number[] | null => {
    if (--budget < 0 || route.length > 19) return null
    if (index === OUTPUT_TILE) return route.length >= 9 ? route : null
    visited.add(index)
    for (const port of shuffle(PORTS)) {
      const next = neighbor(index, port)
      if (next === null || visited.has(next)) continue
      const found = search(next, [...route, next])
      if (found) return found
    }
    visited.delete(index)
    return null
  }
  const route = search(INPUT_TILE, [INPUT_TILE]) ?? [
    10, 5, 0, 1, 2, 7, 12, 17, 18, 13, 14,
  ]
  const solution = Array.from(
    { length: GRID_SIZE ** 2 },
    () => SHAPES[Math.floor(random() * SHAPES.length)]
  )
  route.forEach((index, i) => {
    const before =
      i === 0
        ? 8
        : PORTS.find((port) => neighbor(index, port) === route[i - 1])!
    const after =
      i === route.length - 1
        ? 2
        : PORTS.find((port) => neighbor(index, port) === route[i + 1])!
    solution[index] = before | after
  })
  const tiles = solution.map((mask) => {
    let turns = Math.floor(random() * 4)
    while (turns-- > 0) mask = rotateTile(mask)
    return mask
  })
  // Every round is solvable, and none starts with an already connected circuit.
  while (traceCircuit(tiles).won) {
    tiles[INPUT_TILE] = rotateTile(tiles[INPUT_TILE])
  }
  return { tiles, solution, route }
}
export function hintedTile(circuit: Circuit, tiles: readonly number[]) {
  return (
    circuit.route.find((index) => tiles[index] !== circuit.solution[index]) ??
    null
  )
}
