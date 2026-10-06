/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { PALETTE } from './home-core'
import {
  createWorldMaterials,
  createStoreModel,
  createToolsModel,
  createEcosystemModel,
  createFutureModel,
} from './home-world-three'
import {
  homeWorldChapter,
  homeWorldFrame,
  smoothWorldProgress,
} from './home-worlds'

test('four successive native-scroll chapters follow the introduction, including the endpoint', () => {
  assert.deepEqual(
    [0, 0.19, 0.2, 0.39, 0.4, 0.59, 0.6, 0.79, 0.8, 1].map(homeWorldChapter),
    [0, 0, 1, 1, 2, 2, 3, 3, 4, 4]
  )
  assert.equal(homeWorldChapter(Number.NaN), 0)
  assert.equal(homeWorldChapter(-5), 0)
  assert.equal(homeWorldChapter(5), 4)
})

test('scroll transitions overlap continuously and have the same frame in either direction', () => {
  for (const boundary of [0.2, 0.4, 0.6, 0.8]) {
    const center = boundary - 0.045
    const before = homeWorldFrame(center - 0.00001)
    const at = homeWorldFrame(center)
    const after = homeWorldFrame(center + 0.00001)
    assert.equal(at.to, at.from + 1)
    assert.ok(Math.abs(at.mix - 0.5) < 0.000001)
    assert.ok(before.mix < at.mix && after.mix > at.mix)
    assert.ok(after.mix - before.mix < 0.001)
    const settled = homeWorldFrame(boundary)
    assert.equal(settled.from, settled.to)
    assert.equal(settled.chapter, Math.round(boundary * 5))
  }
  const forward = Array.from({ length: 101 }, (_, i) => homeWorldFrame(i / 100))
  const backward = Array.from({ length: 101 }, (_, i) =>
    homeWorldFrame((100 - i) / 100)
  ).reverse()
  assert.deepEqual(forward, backward)
  assert.deepEqual(homeWorldFrame(Number.NaN), homeWorldFrame(0))
  assert.equal(homeWorldFrame(1).chapter, 4)
})

test('time damping settles a fast scroll in 600ms and reverses without overshoot', () => {
  let progress = 0
  for (let i = 0; i < 15; i++) progress = smoothWorldProgress(progress, 0.8, 40)
  assert.ok(progress > 0.775 && progress < 0.8)
  for (let i = 0; i < 15; i++) progress = smoothWorldProgress(progress, 0.2, 40)
  assert.ok(progress > 0.2 && progress < 0.22)
  assert.equal(smoothWorldProgress(0.2, 0.2, 40), 0.2)
  assert.ok(smoothWorldProgress(0, 0.8, 600) > 0.775)
})

test('all four detailed models use normalled solids within the scene geometry budget', () => {
  const materials = createWorldMaterials(PALETTE)
  const geometries = new Set<{ dispose(): void }>()
  const ownedMaterials = new Set(Object.values(materials))
  try {
    for (const factory of [
      createStoreModel,
      createToolsModel,
      createEcosystemModel,
      createFutureModel,
    ]) {
      const model = factory(materials)
      let meshes = 0
      let triangles = 0
      let opaque = 0
      model.traverse((object) => {
        if (!('isMesh' in object) || !object.isMesh) return
        const mesh = object as import('three').Mesh
        const geometry = mesh.geometry
        geometries.add(geometry)
        assert.ok(
          geometry.attributes.normal,
          `${model.userData.world} mesh lacks surface normals`
        )
        const faces =
          (geometry.index?.count ?? geometry.attributes.position.count) / 3
        triangles += faces * ('count' in mesh ? Number(mesh.count) : 1)
        meshes++
        for (const material of Array.isArray(mesh.material)
          ? mesh.material
          : [mesh.material]) {
          ownedMaterials.add(material as typeof materials.paper)
          if (!material.transparent) opaque++
        }
      })
      assert.ok(meshes <= 100, `${model.userData.world}: ${meshes} meshes`)
      assert.ok(
        triangles < 30000,
        `${model.userData.world}: ${triangles} triangles`
      )
      assert.ok(
        opaque > meshes * 0.9,
        `${model.userData.world} must retain solid depth at rest`
      )
    }
  } finally {
    for (const geometry of geometries) geometry.dispose()
    for (const material of ownedMaterials) material.dispose()
  }
})
