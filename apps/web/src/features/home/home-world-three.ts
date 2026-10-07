/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import * as THREE from 'three'
import { RoundedBoxGeometry } from 'three/addons/geometries/RoundedBoxGeometry.js'

import type { CorePalette, Rgb } from './home-core'

const color = (rgb: Rgb) =>
  new THREE.Color(`rgb(${rgb.map(Math.round).join(',')})`)
const blend = (a: THREE.Color, b: THREE.Color, amount: number) =>
  a.clone().lerp(b, amount)

export function createWorldMaterials(palette: CorePalette) {
  const paper = new THREE.Color('rgb(240,238,230)')
  const ink = new THREE.Color('rgb(20,20,19)')
  const clay = color(palette.forward)
  const leaf = blend(new THREE.Color('rgb(188,209,202)'), ink, 0.62)
  const make = (value: THREE.Color, roughness = 0.68, metalness = 0) =>
    new THREE.MeshStandardMaterial({ color: value, roughness, metalness })
  return {
    paper: make(paper),
    wall: make(blend(paper, clay, 0.055), 0.9),
    stone: make(blend(paper, ink, 0.22), 0.87),
    grout: make(blend(paper, ink, 0.31), 0.93),
    clay: make(clay, 0.74),
    roof: make(blend(clay, ink, 0.15), 0.76),
    forest: make(leaf, 0.56),
    ink: make(ink, 0.48),
    brass: make(blend(clay, paper, 0.45), 0.3, 0.65),
    metal: make(blend(paper, ink, 0.5), 0.35, 0.68),
    glass: new THREE.MeshPhysicalMaterial({
      color: paper,
      transparent: true,
      opacity: 0.19,
      roughness: 0.16,
      metalness: 0,
      depthWrite: false,
    }),
  }
}
type Materials = ReturnType<typeof createWorldMaterials>

/** Each repeated solid reuses the same bounded, smoothly normalled bevel mesh. */
function modelBuilder(materials: Materials) {
  const group = new THREE.Group()
  const bevel = new RoundedBoxGeometry(1, 1, 1, 2, 0.045)
  const box = (
    size: readonly [number, number, number],
    position: readonly [number, number, number],
    material: THREE.Material,
    rounded = true
  ) => {
    const mesh = new THREE.Mesh(
      rounded ? bevel : new THREE.BoxGeometry(1, 1, 1),
      material
    )
    mesh.scale.set(...size)
    mesh.position.set(...position)
    mesh.castShadow = true
    mesh.receiveShadow = true
    group.add(mesh)
    return mesh
  }
  const cylinder = (
    radius: number,
    length: number,
    position: readonly [number, number, number],
    material: THREE.Material,
    segments = 16
  ) => {
    const mesh = new THREE.Mesh(
      new THREE.CylinderGeometry(radius, radius, length, segments),
      material
    )
    mesh.position.set(...position)
    mesh.castShadow = true
    mesh.receiveShadow = true
    group.add(mesh)
    return mesh
  }
  const sign = (
    text: string,
    width: number,
    height: number,
    position: readonly [number, number, number],
    light = true
  ) => {
    if (typeof document === 'undefined') return
    const canvas = document.createElement('canvas')
    canvas.width = 1024
    canvas.height = 256
    const context = canvas.getContext('2d')
    if (!context) return
    context.clearRect(0, 0, canvas.width, canvas.height)
    context.font = "750 164px 'Public Sans Variable', sans-serif"
    context.fillStyle = light ? '#f0eee6' : '#141413'
    context.textAlign = 'center'
    context.textBaseline = 'middle'
    context.fillText(text, 512, 138, 960)
    const texture = new THREE.CanvasTexture(canvas)
    texture.colorSpace = THREE.SRGBColorSpace
    const mesh = new THREE.Mesh(
      new THREE.PlaneGeometry(width, height),
      new THREE.MeshBasicMaterial({
        map: texture,
        transparent: true,
        depthWrite: false,
      })
    )
    mesh.position.set(...position)
    group.add(mesh)
  }
  return { group, box, cylinder, sign, materials }
}

function cable(
  group: THREE.Group,
  points: readonly (readonly [number, number, number])[],
  material: THREE.Material,
  radius = 0.035
) {
  const mesh = new THREE.Mesh(
    new THREE.TubeGeometry(
      new THREE.CatmullRomCurve3(
        points.map((point) => new THREE.Vector3(...point))
      ),
      48,
      radius,
      8,
      false
    ),
    material
  )
  mesh.castShadow = true
  group.add(mesh)
  return mesh
}

/** A desktop MCP appliance with real patch sockets, cooling fins and cables. */
export function createToolsModel(materials: Materials) {
  const { group, box, cylinder, sign } = modelBuilder(materials)
  box([5.8, 0.22, 4.2], [0, -0.12, 0], materials.stone)
  box([5.58, 0.065, 3.98], [0, 0.03, 0], materials.paper)
  // A gently tilted display and a machined enclosure on four rubber feet.
  for (const x of [-1.85, 1.05]) {
    for (const z of [-0.93, 0.92]) {
      cylinder(0.14, 0.18, [x, 0.15, z], materials.ink)
    }
  }
  box([3.5, 2.76, 2.15], [-0.4, 1.65, -0.06], materials.forest)
  box([3.23, 2.5, 0.14], [-0.4, 1.68, 1.06], materials.paper)
  box([2.94, 0.9, 0.08], [-0.4, 2.38, 1.155], materials.ink)
  sign('MCP', 1.24, 0.44, [-0.52, 2.39, 1.205])
  for (let i = 0; i < 3; i++) {
    const indicator = cylinder(
      0.05,
      0.025,
      [0.54 + i * 0.19, 2.59, 1.22],
      materials.clay
    )
    indicator.rotation.x = Math.PI / 2
  }
  // Two socket banks: recessed wells, metal rims and keyed connector tongues.
  for (let row = 0; row < 2; row++) {
    const y = 1.7 - row * 0.61
    box([2.95, 0.5, 0.075], [-0.4, y, 1.165], materials.stone)
    for (let port = 0; port < 4; port++) {
      const x = -1.46 + port * 0.69
      box([0.46, 0.28, 0.055], [x, y, 1.215], materials.metal)
      box([0.35, 0.18, 0.058], [x, y, 1.254], materials.ink)
      box([0.21, 0.034, 0.018], [x, y - 0.04, 1.288], materials.brass)
    }
  }
  // Repeated vents are instanced: visible chassis detail with one draw call.
  const vents = new THREE.InstancedMesh(
    new RoundedBoxGeometry(0.027, 1.34, 0.065, 2, 0.009),
    materials.ink,
    12
  )
  const matrix = new THREE.Matrix4()
  for (let i = 0; i < 12; i++) {
    matrix.makeTranslation(1.385, 1.92, -0.83 + i * 0.145)
    matrix.multiply(new THREE.Matrix4().makeRotationY(Math.PI / 2))
    vents.setMatrixAt(i, matrix)
  }
  group.add(vents)
  for (const x of [-1.91, 1.11]) {
    for (const y of [0.59, 2.77]) {
      const screw = cylinder(0.055, 0.025, [x, y, 1.2], materials.metal, 12)
      screw.rotation.x = Math.PI / 2
      box([0.062, 0.01, 0.012], [x, y, 1.219], materials.ink)
    }
  }
  // A companion module makes the service connection tangible.
  box([1.02, 0.46, 1.2], [2.04, 0.34, 0.5], materials.clay)
  box([0.75, 0.06, 0.92], [2.04, 0.59, 0.5], materials.paper)
  for (let i = 0; i < 3; i++) {
    box([0.11, 0.018, 0.58], [1.82 + i * 0.21, 0.63, 0.5], materials.forest)
  }
  for (const [x, y] of [
    [-1.46, 1.7],
    [0.61, 1.09],
  ]) {
    box([0.3, 0.15, 0.26], [x, y, 1.38], materials.forest)
  }
  cable(
    group,
    [
      [-1.46, 1.7, 1.48],
      [-1.46, 1.5, 1.93],
      [-0.92, 0.19, 1.95],
      [1.5, 0.15, 1.79],
      [2.04, 0.33, 1.14],
    ],
    materials.forest
  )
  cable(
    group,
    [
      [0.61, 1.09, 1.48],
      [0.84, 0.5, 1.77],
      [1.24, 0.15, 1.3],
      [1.66, 0.15, 0.46],
      [1.66, 0.33, 0.25],
    ],
    materials.clay
  )
  box([1.02, 0.055, 0.58], [-1.8, 0.11, -1.48], materials.forest)
  box([0.74, 0.026, 0.32], [-1.8, 0.15, -1.48], materials.paper)
  group.userData.world = 'tools'
  return group
}

/** Raised continental surfaces follow a subdivided sphere, without latitude wire. */
function continent(
  points: readonly (readonly [number, number])[],
  radius: number
) {
  const outline = points.map(
    ([longitude, latitude]) => new THREE.Vector2(longitude, latitude)
  )
  const faces = THREE.ShapeUtils.triangulateShape(outline, [])
  const positions: number[] = []
  const project = (p: THREE.Vector2) => {
    const latitude = (p.y * Math.PI) / 180
    const longitude = (p.x * Math.PI) / 180
    return new THREE.Vector3(
      Math.cos(latitude) * Math.sin(longitude),
      Math.sin(latitude),
      Math.cos(latitude) * Math.cos(longitude)
    ).multiplyScalar(radius)
  }
  const subdivide = (
    a: THREE.Vector2,
    b: THREE.Vector2,
    c: THREE.Vector2,
    depth: number
  ) => {
    if (!depth) {
      for (const p of [a, b, c]) positions.push(...project(p).toArray())
      return
    }
    const ab = a.clone().lerp(b, 0.5)
    const bc = b.clone().lerp(c, 0.5)
    const ca = c.clone().lerp(a, 0.5)
    subdivide(a, ab, ca, depth - 1)
    subdivide(ab, b, bc, depth - 1)
    subdivide(ca, bc, c, depth - 1)
    subdivide(ab, bc, ca, depth - 1)
  }
  for (const [a, b, c] of faces) {
    subdivide(outline[a], outline[b], outline[c], 3)
  }
  const geometry = new THREE.BufferGeometry()
  geometry.setAttribute(
    'position',
    new THREE.Float32BufferAttribute(positions, 3)
  )
  // Radial normals keep the raised land smooth across triangulated patches.
  const normals: number[] = []
  for (let i = 0; i < positions.length; i += 3) {
    normals.push(
      ...new THREE.Vector3(positions[i], positions[i + 1], positions[i + 2])
        .normalize()
        .toArray()
    )
  }
  geometry.setAttribute('normal', new THREE.Float32BufferAttribute(normals, 3))
  return geometry
}

/** A smooth, solid planet with raised land and three crafted plugin satellites. */
export function createEcosystemModel(materials: Materials) {
  const { group, box, cylinder, sign } = modelBuilder(materials)
  const globe = new THREE.Group()
  globe.position.set(0, 2.0, 0)
  globe.rotation.set(0.1, -0.35, -0.12)
  const ocean = new THREE.Mesh(
    new THREE.SphereGeometry(1.78, 64, 40),
    materials.forest
  )
  ocean.castShadow = true
  ocean.receiveShadow = true
  globe.add(ocean)
  const land = materials.paper.clone()
  land.side = THREE.DoubleSide
  const continents = [
    [
      [-168, 66],
      [-145, 71],
      [-124, 59],
      [-110, 52],
      [-85, 53],
      [-53, 47],
      [-65, 32],
      [-80, 25],
      [-90, 15],
      [-104, 20],
      [-119, 34],
      [-137, 57],
    ],
    [
      [-81, 10],
      [-62, 9],
      [-35, -7],
      [-43, -23],
      [-53, -36],
      [-69, -54],
      [-76, -32],
      [-79, -5],
    ],
    [
      [-17, 35],
      [8, 37],
      [32, 30],
      [45, 12],
      [34, -6],
      [23, -34],
      [13, -35],
      [2, -13],
      [-9, 5],
      [-17, 16],
    ],
    [
      [-10, 37],
      [-9, 58],
      [12, 72],
      [37, 60],
      [63, 72],
      [100, 74],
      [148, 60],
      [161, 51],
      [137, 39],
      [120, 23],
      [106, 5],
      [86, 20],
      [73, 8],
      [55, 29],
      [36, 32],
      [28, 42],
      [9, 43],
    ],
    [
      [113, -22],
      [131, -12],
      [151, -23],
      [147, -39],
      [130, -33],
      [115, -34],
    ],
    [
      [-53, 60],
      [-24, 68],
      [-37, 82],
      [-59, 76],
    ],
  ] as const
  for (const outline of continents) {
    const mesh = new THREE.Mesh(continent(outline, 1.802), land)
    mesh.castShadow = false
    globe.add(mesh)
  }
  // A restrained atmosphere is the only transparent skin around the solid planet.
  const atmosphere = new THREE.Mesh(
    new THREE.SphereGeometry(1.84, 48, 32),
    new THREE.MeshPhysicalMaterial({
      color: materials.paper.color,
      transparent: true,
      opacity: 0.035,
      roughness: 0.2,
      side: THREE.BackSide,
      depthWrite: false,
    })
  )
  globe.add(atmosphere)
  group.add(globe)
  const orbit = new THREE.Mesh(
    new THREE.TorusGeometry(2.28, 0.018, 8, 128),
    materials.brass
  )
  orbit.rotation.set(1.06, 0.18, 0.25)
  orbit.position.y = 2.0
  group.add(orbit)
  const satellites = [
    ['Pi', -2.09, 1.5, 1.22, materials.clay],
    ['dsh', 1.95, 3.24, 0.43, materials.forest],
    ['WWW', 1.69, 0.45, 1.4, materials.clay],
  ] as const
  for (const [name, x, y, z, material] of satellites) {
    box([0.85, 0.48, 0.56], [x, y, z], material)
    box([0.66, 0.29, 0.025], [x, y, z + 0.3], materials.ink)
    sign(name, 0.5, 0.21, [x, y, z + 0.32])
    for (const side of [-1, 1]) {
      const panel = box(
        [0.47, 0.028, 0.43],
        [x + side * 0.62, y, z],
        materials.metal
      )
      panel.rotation.z = side * 0.18
      for (let cell = 0; cell < 3; cell++) {
        box(
          [0.09, 0.018, 0.32],
          [x + side * 0.62 + (cell - 1) * 0.13, y + 0.026, z],
          materials.forest
        )
      }
    }
    cylinder(0.021, 0.35, [x, y + 0.4, z], materials.brass, 12)
  }
  group.userData.world = 'ecosystem'
  return group
}

/** An inhabited observatory: a gateway, planted terraces and a path into tomorrow. */
export function createFutureModel(materials: Materials) {
  const { group, box, cylinder, sign } = modelBuilder(materials)
  box([5.65, 0.2, 4.25], [0, -0.12, 0], materials.stone)
  box([5.43, 0.07, 4.02], [0, 0.02, 0], materials.paper)
  // Curved double portal has actual thickness and an articulated pedestal.
  for (const [radius, z, material] of [
    [1.62, -0.15, materials.forest],
    [1.43, 0.0, materials.brass],
  ] as const) {
    const ring = new THREE.Mesh(
      new THREE.TorusGeometry(radius, radius === 1.62 ? 0.19 : 0.047, 16, 96),
      material
    )
    ring.position.set(0.34, 2.49, z)
    ring.castShadow = true
    ring.receiveShadow = true
    group.add(ring)
  }
  box([1.18, 0.76, 0.84], [0.34, 0.44, -0.14], materials.forest)
  box([0.9, 0.22, 0.9], [0.34, 0.94, -0.14], materials.clay)
  // A sculpted seed is suspended inside the gateway, held by two thin support arcs.
  const seed = new THREE.Mesh(
    new THREE.IcosahedronGeometry(0.62, 3),
    materials.clay
  )
  seed.scale.set(0.75, 1.16, 0.75)
  seed.rotation.z = -0.24
  seed.position.set(0.34, 2.49, -0.02)
  seed.castShadow = true
  group.add(seed)
  for (const side of [-1, 1]) {
    cable(
      group,
      [
        [0.34 + side * 1.4, 2.2, 0.0],
        [0.34 + side * 0.98, 2.3, 0.08],
        [0.34 + side * 0.5, 2.48, 0.02],
      ],
      materials.brass,
      0.014
    )
  }
  // Terraced pavilion: separate facade ribs, inset glazing and planted roof.
  box([1.26, 2.16, 1.0], [-1.63, 1.16, -0.91], materials.clay)
  box([1.36, 0.18, 1.11], [-1.63, 2.29, -0.91], materials.paper)
  for (let floor = 0; floor < 3; floor++) {
    box([1.1, 0.41, 0.03], [-1.63, 0.55 + floor * 0.56, -0.393], materials.ink)
    for (let rib = 0; rib < 5; rib++) {
      box(
        [0.037, 0.44, 0.055],
        [-2.06 + rib * 0.21, 0.55 + floor * 0.56, -0.365],
        materials.brass
      )
    }
  }
  box([0.88, 0.12, 0.7], [-1.63, 2.44, -0.91], materials.forest)
  for (const [x, z, height] of [
    [-1.9, -0.89, 0.28],
    [-1.46, -1.07, 0.34],
    [2.05, 0.06, 0.5],
  ] as const) {
    cylinder(0.16, 0.25, [x, height > 0.4 ? 0.2 : 2.54, z], materials.clay)
    const shrub = new THREE.Mesh(
      new THREE.SphereGeometry(0.27, 20, 16),
      materials.forest
    )
    shrub.scale.y = 1.3
    shrub.position.set(x, height > 0.4 ? 0.55 : 2.78, z)
    shrub.castShadow = true
    group.add(shrub)
  }
  // Ascending paths continue beyond the observatory instead of an empty backdrop.
  for (let step = 0; step < 7; step++) {
    box(
      [0.87, 0.09, 0.37],
      [0.34, 0.105 + step * 0.045, 1.75 - step * 0.3],
      step % 2 ? materials.stone : materials.paper
    )
  }
  box([1.16, 0.68, 0.92], [1.97, 0.45, -1.19], materials.forest)
  box([0.97, 0.4, 0.028], [1.97, 0.46, -0.712], materials.ink)
  sign('LMM', 0.69, 0.23, [1.97, 0.48, -0.69])
  box([1.24, 0.13, 1.04], [1.97, 0.86, -1.19], materials.paper)
  group.userData.world = 'future'
  return group
}

/** An opaque, furnished miniature shop; details are geometry, not a wire overlay. */
export function createStoreModel(materials: Materials) {
  const { group, box, cylinder, sign } = modelBuilder(materials)
  box([5.7, 0.22, 4.3], [0, -0.12, 0], materials.stone)
  box([5.5, 0.05, 4.1], [0, 0.015, 0], materials.paper)
  // Individual front paving stones and a grounded entrance step.
  for (let i = 0; i < 8; i++) {
    box(
      [0.62, 0.055, 0.76],
      [-2.25 + i * 0.64, 0.07, 1.57],
      i % 3 === 0 ? materials.stone : materials.paper
    )
  }
  box([1.2, 0.16, 0.6], [1.4, 0.17, 1.07], materials.stone)
  // Wall thickness and an open display recess make the inside legible.
  box([4.5, 3.22, 0.22], [0, 1.77, -1.24], materials.wall)
  box([0.22, 3.22, 2.7], [-2.14, 1.77, 0], materials.wall)
  box([0.22, 3.22, 2.7], [2.14, 1.77, 0], materials.wall)
  box([4.14, 0.12, 2.5], [0, 0.24, 0], materials.forest)
  box([4.5, 0.46, 0.2], [0, 0.48, 1.23], materials.wall)
  box([4.5, 0.64, 0.21], [0, 3.02, 1.23], materials.wall)
  box([0.2, 2.35, 0.23], [0.74, 1.75, 1.23], materials.wall)
  // The double-pitch roof has a real gable, soffit, ridge and segmented tiles.
  const triangle = new THREE.Shape()
    .moveTo(-2.4, 0)
    .lineTo(2.4, 0)
    .lineTo(0, 0.94)
    .closePath()
  const gable = new THREE.Mesh(
    new THREE.ExtrudeGeometry(triangle, { depth: 2.93, bevelEnabled: false }),
    materials.wall
  )
  gable.position.set(0, 3.36, -1.48)
  gable.castShadow = true
  gable.receiveShadow = true
  group.add(gable)
  const slope = Math.atan2(0.94, 2.4)
  for (const side of [-1, 1]) {
    const roof = box([2.61, 0.14, 3.12], [side * 1.22, 3.83, 0], materials.roof)
    roof.rotation.z = -side * slope
    for (let row = 0; row < 4; row++) {
      const trim = box(
        [2.59, 0.045, 0.035],
        [side * 1.22, 3.91, -1.24 + row * 0.82],
        materials.clay
      )
      trim.rotation.z = -side * slope
    }
  }
  const ridge = cylinder(0.07, 3.16, [0, 4.32, 0], materials.roof, 20)
  ridge.rotation.x = Math.PI / 2
  // Timber window sill, narrow mullions, transparent glazing and furnished shelves.
  box([2.79, 0.14, 0.36], [-0.8, 0.8, 1.37], materials.forest)
  for (const x of [-2.0, 0.48]) {
    box([0.12, 1.85, 0.14], [x, 1.78, 1.36], materials.forest)
  }
  for (const x of [-1.16, -0.34]) {
    box([0.07, 1.8, 0.1], [x, 1.78, 1.39], materials.forest)
  }
  box([2.55, 0.1, 0.12], [-0.76, 2.69, 1.38], materials.forest)
  box(
    [2.55, 1.75, 0.018],
    [-0.76, 1.77, 1.39],
    materials.glass,
    false
  ).castShadow = false
  for (const y of [1.14, 1.83]) {
    box([2.53, 0.095, 0.78], [-0.77, y, 0.85], materials.paper)
  }
  // Merchandise: bound booklets, embossed license boxes, upright delivery cards.
  for (let i = 0; i < 4; i++) {
    const booklet = box(
      [0.19, 0.55, 0.28],
      [-1.74 + i * 0.23, 1.44, 0.9],
      i % 2 ? materials.clay : materials.forest
    )
    booklet.rotation.z = i === 3 ? -0.1 : 0
    box([0.025, 0.36, 0.018], [-1.73 + i * 0.23, 1.46, 1.05], materials.paper)
  }
  box([0.53, 0.44, 0.42], [-0.39, 1.42, 0.9], materials.clay)
  box([0.29, 0.09, 0.024], [-0.39, 1.48, 1.12], materials.paper)
  box([0.14, 0.14, 0.022], [-0.39, 1.34, 1.12], materials.brass)
  for (let i = 0; i < 3; i++) {
    box(
      [0.34, 0.58, 0.055],
      [-1.58 + i * 0.58, 2.16, 1.01],
      i === 1 ? materials.forest : materials.paper
    )
    box(
      [0.2, 0.06, 0.02],
      [-1.58 + i * 0.58, 2.26, 1.05],
      i === 1 ? materials.paper : materials.clay
    )
    box([0.15, 0.018, 0.02], [-1.58 + i * 0.58, 2.06, 1.05], materials.stone)
  }
  // Recessed solid door, glazed inset and a metal pull; frame casts real shadows.
  box([1.0, 2.21, 0.14], [1.39, 1.45, 1.14], materials.forest)
  for (const x of [0.85, 1.93]) {
    box([0.1, 2.35, 0.17], [x, 1.52, 1.32], materials.forest)
  }
  box([1.14, 0.1, 0.18], [1.39, 2.68, 1.32], materials.forest)
  box([0.72, 1.13, 0.04], [1.39, 1.83, 1.25], materials.ink)
  box(
    [0.68, 1.09, 0.016],
    [1.39, 1.83, 1.29],
    materials.glass,
    false
  ).castShadow = false
  box([0.045, 0.35, 0.06], [1.73, 1.28, 1.27], materials.brass)
  box([0.65, 0.3, 0.045], [1.39, 0.74, 1.24], materials.forest)
  // Curved canvas awning: 12 strips, 9 curve segments, physical fabric thickness.
  for (let stripe = 0; stripe < 12; stripe++) {
    const material = stripe % 2 ? materials.paper : materials.clay
    const path = new THREE.Shape()
    const profile: THREE.Vector2[] = []
    for (let i = 0; i <= 9; i++) {
      const t = i / 9
      profile.push(
        new THREE.Vector2(
          1.18 + t * 0.83,
          2.89 - 0.38 * t + Math.sin(t * Math.PI) * 0.09
        )
      )
    }
    path.moveTo(profile[0].x, profile[0].y)
    for (const p of profile.slice(1)) path.lineTo(p.x, p.y)
    for (const p of [...profile].reverse()) path.lineTo(p.x, p.y - 0.033)
    path.closePath()
    const mesh = new THREE.Mesh(
      new THREE.ExtrudeGeometry(path, { depth: 0.375, bevelEnabled: false }),
      material
    )
    mesh.rotation.y = -Math.PI / 2
    mesh.position.x = -1.875 + stripe * 0.375
    mesh.castShadow = true
    mesh.receiveShadow = true
    group.add(mesh)
    const scallop = new THREE.Shape()
      .moveTo(-0.185, 0)
      .lineTo(0.185, 0)
      .lineTo(0.185, -0.12)
      .quadraticCurveTo(0, -0.25, -0.185, -0.12)
      .closePath()
    const edge = new THREE.Mesh(
      new THREE.ExtrudeGeometry(scallop, {
        depth: 0.027,
        bevelEnabled: false,
        curveSegments: 8,
      }),
      material
    )
    edge.position.set(-2.0625 + stripe * 0.375, 2.51, 2.015)
    edge.castShadow = true
    group.add(edge)
  }
  const awningRod = cylinder(0.035, 4.6, [0, 2.54, 1.99], materials.metal)
  awningRod.rotation.z = Math.PI / 2
  for (const x of [-2.15, 2.15]) {
    const rod = cylinder(0.023, 0.93, [x, 2.66, 1.6], materials.metal)
    rod.rotation.x = -1.15
  }
  box([2.05, 0.42, 0.13], [0, 3.11, 1.37], materials.forest)
  sign('LMM', 1.28, 0.32, [0, 3.1, 1.445])
  // Small ground lamp gives the entrance scale without a floating ornament.
  cylinder(0.05, 1.61, [2.46, 0.84, 0.88], materials.metal)
  cylinder(0.18, 0.09, [2.46, 0.08, 0.88], materials.forest)
  const lamp = new THREE.Mesh(
    new THREE.SphereGeometry(0.19, 20, 12),
    materials.paper
  )
  lamp.position.set(2.46, 1.72, 0.88)
  lamp.castShadow = true
  group.add(lamp)
  group.userData.world = 'store'
  return group
}

export type HomeWorldFilm = {
  draw(
    chapter: number,
    pointer: { x: number; y: number },
    progress: number,
    width: number,
    height: number,
    target?: HTMLCanvasElement,
    nextChapter?: number,
    mix?: number
  ): void
  dispose(): void
}

/** One lazy renderer serves the desktop stage and static mobile chapter copies. */
export function createHomeWorldFilm(
  canvas: HTMLCanvasElement,
  palette: CorePalette,
  onUnavailable?: () => void
): HomeWorldFilm | null {
  let renderer: THREE.WebGLRenderer
  try {
    renderer = new THREE.WebGLRenderer({
      canvas,
      alpha: true,
      antialias: true,
      powerPreference: 'low-power',
    })
  } catch {
    return null
  }
  renderer.setClearColor(0, 0)
  renderer.outputColorSpace = THREE.SRGBColorSpace
  renderer.toneMapping = THREE.ACESFilmicToneMapping
  renderer.toneMappingExposure = 0.98
  renderer.shadowMap.enabled = true
  renderer.shadowMap.type = THREE.PCFSoftShadowMap
  renderer.shadowMap.autoUpdate = false
  const scene = new THREE.Scene()
  const materials = createWorldMaterials(palette)
  const models = new Map<number, THREE.Group>()
  const camera = new THREE.OrthographicCamera(-4, 4, 4, -4, 0.1, 60)
  camera.position.set(7, 5.2, 9)
  camera.lookAt(0, 1.8, 0)
  scene.add(new THREE.HemisphereLight(0xffffff, 0x8b887f, 1.65))
  const light = new THREE.DirectionalLight(0xffffff, 2.5)
  light.position.set(-5, 9, 7)
  light.castShadow = true
  light.shadow.mapSize.set(1024, 1024)
  light.shadow.camera.left = -6
  light.shadow.camera.right = 6
  light.shadow.camera.top = 8
  light.shadow.camera.bottom = -5
  light.shadow.camera.near = 0.5
  light.shadow.camera.far = 30
  light.shadow.normalBias = 0.035
  light.shadow.bias = -0.0001
  light.target.position.set(0, 1.5, 0)
  scene.add(light, light.target)
  const rim = new THREE.DirectionalLight(color(palette.feedback), 0.7)
  rim.position.set(6, 5, -5)
  scene.add(rim)
  const ground = new THREE.Mesh(
    new THREE.PlaneGeometry(16, 16),
    new THREE.ShadowMaterial({ color: 0x000000, opacity: 0.22 })
  )
  ground.rotation.x = -Math.PI / 2
  ground.position.y = -0.24
  ground.receiveShadow = true
  scene.add(ground)
  let disposed = false
  let lost = false
  let lastSize = ''
  let lastShadow = ''
  let targets: [THREE.WebGLRenderTarget, THREE.WebGLRenderTarget] | null = null
  const composite = new THREE.Scene()
  const compositeCamera = new THREE.OrthographicCamera(-1, 1, 1, -1, 0, 1)
  const blendMaterial = new THREE.ShaderMaterial({
    uniforms: {
      previous: { value: null },
      next: { value: null },
      mixAmount: { value: 0 },
    },
    vertexShader:
      'varying vec2 vUv; void main(){vUv=uv;gl_Position=vec4(position.xy,0.0,1.0);}',
    fragmentShader: `varying vec2 vUv;
      uniform sampler2D previous; uniform sampler2D next; uniform float mixAmount;
      void main(){gl_FragColor=mix(texture2D(previous,vUv),texture2D(next,vUv),mixAmount);
      #include <tonemapping_fragment>
      #include <colorspace_fragment>
      }`,
    depthTest: false,
    depthWrite: false,
    transparent: true,
  })
  const quad = new THREE.Mesh(new THREE.PlaneGeometry(2, 2), blendMaterial)
  composite.add(quad)
  const factories = [
    createStoreModel,
    createToolsModel,
    createEcosystemModel,
    createFutureModel,
  ]
  const getModel = (chapter: number) => {
    let model = models.get(chapter)
    if (!model) {
      model = factories[Math.max(0, Math.min(3, chapter - 1))](materials)
      models.set(chapter, model)
      scene.add(model)
    }
    return model
  }
  const renderModel = (
    chapter: number,
    offset: number,
    buffer: THREE.WebGLRenderTarget | null
  ) => {
    const model = getModel(chapter)
    for (const [key, value] of models) value.visible = key === chapter
    model.rotation.y = offset * 0.22
    model.position.x = -offset * 0.45
    model.scale.setScalar(1 - Math.abs(offset) * 0.08)
    const shadow = `${chapter}:${offset.toFixed(4)}:${light.shadow.mapSize.x}`
    if (shadow !== lastShadow) {
      renderer.shadowMap.needsUpdate = true
      lastShadow = shadow
    }
    renderer.setRenderTarget(buffer)
    renderer.render(scene, camera)
    return model
  }
  const contextLost = () => {
    lost = true
    onUnavailable?.()
  }
  canvas.addEventListener('webglcontextlost', contextLost)
  return {
    draw(
      chapter,
      pointer,
      progress,
      width,
      height,
      target,
      nextChapter = chapter,
      mix = 0
    ) {
      if (disposed || lost || !width || !height) return
      const halfHeight = Math.max(3.55, 3.5 / (width / height))
      camera.left = (-halfHeight * width) / height
      camera.right = (halfHeight * width) / height
      camera.top = halfHeight
      camera.bottom = -halfHeight
      camera.updateProjectionMatrix()
      const angle = pointer.x * 0.065 + (progress - 0.5) * 0.035
      camera.position.set(
        7 * Math.cos(angle) + 9 * Math.sin(angle),
        5.2 + pointer.y * 0.35,
        9 * Math.cos(angle) - 7 * Math.sin(angle)
      )
      camera.lookAt(0, 1.8, 0)
      // Phone chapter copies render once at up to 2x; they own no animation loop.
      const ratio = target ? 2 : Math.min(window.devicePixelRatio || 1, 2)
      const size = `${width}:${height}:${ratio}`
      if (size !== lastSize) {
        renderer.setPixelRatio(ratio)
        renderer.setSize(width, height, false)
        lastSize = size
      }
      const shadowSize = target ? 512 : 1024
      if (light.shadow.mapSize.x !== shadowSize) {
        light.shadow.map?.dispose()
        light.shadow.map = null
        light.shadow.mapSize.set(shadowSize, shadowSize)
        lastShadow = ''
      }
      let model: THREE.Group
      if (!target && nextChapter !== chapter && mix > 0 && mix < 1) {
        const bufferWidth = Math.round(width * ratio)
        const bufferHeight = Math.round(height * ratio)
        if (!targets) {
          targets = [
            new THREE.WebGLRenderTarget(bufferWidth, bufferHeight),
            new THREE.WebGLRenderTarget(bufferWidth, bufferHeight),
          ]
        }
        for (const buffer of targets) {
          if (buffer.width !== bufferWidth || buffer.height !== bufferHeight) {
            buffer.setSize(bufferWidth, bufferHeight)
          }
        }
        renderModel(chapter, mix, targets[0])
        model = renderModel(nextChapter, mix - 1, targets[1])
        blendMaterial.uniforms.previous.value = targets[0].texture
        blendMaterial.uniforms.next.value = targets[1].texture
        blendMaterial.uniforms.mixAmount.value = mix
        renderer.setRenderTarget(null)
        renderer.render(composite, compositeCamera)
        canvas.dataset.transition = `${chapter}:${nextChapter}:${mix.toFixed(3)}`
      } else {
        model = renderModel(
          mix >= 1 ? nextChapter : chapter,
          mix < 0 ? mix : 0,
          null
        )
        delete canvas.dataset.transition
      }
      canvas.dataset.world = model.userData.world ?? 'pending'
      if (target) {
        target.width = Math.round(width * ratio)
        target.height = Math.round(height * ratio)
        target
          .getContext('2d')
          ?.drawImage(canvas, 0, 0, target.width, target.height)
        target.dataset.world = model.userData.world ?? 'pending'
      }
    },
    dispose() {
      if (disposed) return
      disposed = true
      canvas.removeEventListener('webglcontextlost', contextLost)
      const geometries = new Set<THREE.BufferGeometry>()
      const materialSet = new Set<THREE.Material>(Object.values(materials))
      const textures = new Set<THREE.Texture>()
      scene.traverse((object) => {
        if (!(object instanceof THREE.Mesh)) return
        geometries.add(object.geometry)
        for (const material of Array.isArray(object.material)
          ? object.material
          : [object.material]) {
          materialSet.add(material)
          for (const value of Object.values(material)) {
            if (value instanceof THREE.Texture) textures.add(value)
          }
        }
      })
      for (const geometry of geometries) geometry.dispose()
      for (const material of materialSet) material.dispose()
      for (const texture of textures) texture.dispose()
      for (const buffer of targets ?? []) buffer.dispose()
      quad.geometry.dispose()
      blendMaterial.dispose()
      light.shadow.map?.dispose()
      renderer.dispose()
      renderer.forceContextLoss()
      models.clear()
    },
  }
}
