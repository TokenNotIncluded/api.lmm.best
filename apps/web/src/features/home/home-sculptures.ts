import * as abstract from './sculptures/abstract'
import * as cosmos from './sculptures/cosmos'
import * as future from './sculptures/future'
import { smooth, type Sculpture } from './sculptures/geometry'
import * as market from './sculptures/market'
/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import * as nature from './sculptures/nature'

const factories = { ...nature, ...market, ...abstract, ...cosmos, ...future }
export type SculptureId = keyof typeof factories
/** Add a factory and its ID here; page layout and pointer physics are independent. */
export const HOME_SEQUENCES = [
  ['lotus', 'fish', 'jellyfish', 'dandelion', 'cloud', 'dragonfly'],
  ['shop', 'vending', 'stall'],
  ['socket', 'gyroscope', 'mobius', 'trefoil', 'helix', 'ribbon'],
  ['earth', 'moon', 'mars', 'sun', 'solarSystem', 'galaxy', 'blackHole'],
  ['pelicanBicycle', 'emperorBear', 'catBomb'],
] as const satisfies readonly (readonly SculptureId[])[]
export const HOLD_SECONDS = 7.2
export const MORPH_SECONDS = 2.4
export const SCENE_SECONDS = HOLD_SECONDS + MORPH_SECONDS

export function sequenceAt(chapter: number, seconds: number) {
  const list =
    HOME_SEQUENCES[
      Math.max(
        0,
        Math.min(4, Math.floor(Number.isFinite(chapter) ? chapter : 0))
      )
    ]
  const time = Math.max(0, Number.isFinite(seconds) ? seconds : 0)
  const index = Math.floor(time / SCENE_SECONDS) % list.length
  const phase = time % SCENE_SECONDS
  return {
    from: list[index],
    to: list[(index + 1) % list.length],
    mix: smooth(
      Math.max(0, Math.min(1, (phase - HOLD_SECONDS) / MORPH_SECONDS))
    ),
  }
}

// Shared across mobile chapter canvases. Never retain all future additions forever.
const cache = new Map<SculptureId, Sculpture>()
let sampledLotus: Sculpture | undefined
export function getSculpture(id: SculptureId): Sculpture {
  if (id === 'lotus' && sampledLotus) return sampledLotus
  const model = cache.get(id) ?? factories[id]()
  cache.delete(id)
  cache.set(id, model)
  if (cache.size > 10) cache.delete(cache.keys().next().value!)
  return model
}
export function replaceLotus(points: Sculpture['points']) {
  sampledLotus = { ...getSculpture('lotus'), points, view: [0, 0] }
  cache.delete('lotus')
}
