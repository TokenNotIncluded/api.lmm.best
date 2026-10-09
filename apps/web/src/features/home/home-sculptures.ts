/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
/*
Copyright (C) 2026 LIghtJUNction
*/
import * as abstract from './sculptures/abstract'
import * as cosmos from './sculptures/cosmos'
import { pelicanBicycle } from './sculptures/future'
import { smooth, type Sculpture } from './sculptures/geometry'
import * as market from './sculptures/market'
import { claudeMark, openaiMark } from './sculptures/marks'
import { moonFarSide } from './sculptures/moon-far-side'
import { cloud, dandelion, dragonfly, fish, jellyfish } from './sculptures/nature'
import { smilingPortrait } from './sculptures/portrait'
import {
  atomicExplosion,
  rotatingChair,
  spacexRocket,
} from './sculptures/spectacle'
import { blueWhale } from './sculptures/whale'
const factories = {
  smilingPortrait,
  blueWhale,
  fish,
  jellyfish,
  dandelion,
  cloud,
  dragonfly,
  claudeMark,
  openaiMark,
  moonFarSide,
  ...market,
  ...abstract,
  ...cosmos,
  pelicanBicycle,
  spacexRocket,
  atomicExplosion,
  rotatingChair,
}

export type SculptureId = keyof typeof factories
/** Add a factory and its ID here; page layout and pointer physics are independent. */

export const HOME_SEQUENCES = [
  [
    'smilingPortrait',
    'blueWhale',
    'fish',
    'jellyfish',
    'dandelion',
    'cloud',
    'dragonfly',
    'claudeMark',
    'openaiMark',
    'moonFarSide',
  ],
  ['shop', 'vending', 'stall'],
  ['socket', 'gyroscope', 'mobius', 'trefoil', 'helix', 'ribbon'],
  ['earth', 'moon', 'mars', 'sun', 'solarSystem', 'galaxy', 'blackHole'],
  ['pelicanBicycle', 'spacexRocket', 'atomicExplosion', 'rotatingChair'],
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
    // Incoming scenes start at zero, then continue across the slot boundary.
    age: phase + MORPH_SECONDS,
    nextAge: Math.max(0, phase - HOLD_SECONDS),
    mix: smooth(
      Math.max(0, Math.min(1, (phase - HOLD_SECONDS) / MORPH_SECONDS))
    ),
  }
}
// Shared across mobile chapter canvases. Never retain all future additions forever.
const cache = new Map<SculptureId, Sculpture>()

export function getSculpture(id: SculptureId): Sculpture {
  const model = cache.get(id) ?? factories[id]()
  cache.delete(id)
  cache.set(id, model)
  if (cache.size > 10) cache.delete(cache.keys().next().value!)
  return model
}
