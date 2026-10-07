/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import {
  createGpuUniformsMap,
  rootPassthrough,
  shaderRendererGPU,
} from 'shaders/core'
import grid from 'shaders/core/Grid'
import mesh from 'shaders/core/MeshGradient'

import editorComposition from './forge-ambient.generated.json'
import { FORGE_SHADER_LIMITS, type ForgeShaderVariant } from './shader-policy'

export type ShaderPalette = {
  ground: string
  primary: string
  muted: string
  ink: string
}

const compositions = {
  home: {
    countOffset: 0,
    smoothness: 1,
    swirl: 1,
    drift: 1,
    speed: 1,
    cells: 10 / 14,
    rotation: 0,
    gridOpacity: 0,
  },
  store: {
    countOffset: -1,
    smoothness: 0.8,
    swirl: 2 / 3,
    drift: 0.75,
    speed: 0.8,
    cells: 8 / 14,
    rotation: 0,
    gridOpacity: 2 / 3,
  },
  tools: {
    countOffset: 0,
    smoothness: 2.6 / 3,
    swirl: -1,
    drift: 0.875,
    speed: 1,
    cells: 1,
    rotation: 0,
    gridOpacity: 1.5,
  },
  ecosystem: {
    countOffset: 1,
    smoothness: 3.2 / 3,
    swirl: 2.5,
    drift: 1.125,
    speed: 0.8,
    cells: 12 / 14,
    rotation: 45,
    gridOpacity: 7 / 12,
  },
  future: {
    countOffset: 0,
    smoothness: 2.8 / 3,
    swirl: -2.5,
    drift: 1.25,
    speed: 1.2,
    cells: 18 / 14,
    rotation: 30,
    gridOpacity: 5 / 6,
  },
  assistant: {
    countOffset: -1,
    smoothness: 2.6 / 3,
    swirl: 3.75,
    drift: 0.75,
    speed: 2,
    cells: 8 / 14,
    rotation: 45,
    gridOpacity: 0,
  },
} as const

const editorMesh: Record<string, unknown> = editorComposition.meshGradient
const editorGrid: Record<string, unknown> = editorComposition.grid
const numeric = (
  props: Record<string, unknown>,
  key: string,
  fallback: number
) => (typeof props[key] === 'number' ? props[key] : fallback)
const clamp = (value: number, min: number, max: number) =>
  Math.max(min, Math.min(max, value))
const gridOpacity = (variant: ForgeShaderVariant) =>
  clamp(
    numeric(editorGrid, 'opacity', 1) * compositions[variant].gridOpacity,
    0,
    1
  )

function meshProps(variant: ForgeShaderVariant, palette: ShaderPalette) {
  const config = compositions[variant]
  return {
    ...Object.fromEntries(
      Object.entries(mesh.props).map(([key, value]) => [key, value.default])
    ),
    ...editorMesh,
    // Account-owned geometry/motion comes from the verified CLI source. Theme
    // colors replace its editor stops; each route applies a relative variation.
    colorA: palette.ground,
    colorB: palette.primary,
    stops: null,
    colorSpace: 'oklab',
    count: clamp(
      Math.round(numeric(editorMesh, 'count', 4) + config.countOffset),
      2,
      8
    ),
    smoothness: clamp(
      numeric(editorMesh, 'smoothness', 3) * config.smoothness,
      0,
      10
    ),
    swirl: clamp(numeric(editorMesh, 'swirl', 0) * config.swirl, -4, 4),
    drift: clamp(numeric(editorMesh, 'drift', 0) * config.drift, 0, 4),
    speed: clamp(numeric(editorMesh, 'speed', 0.1) * config.speed, -1, 1),
  }
}

function gridProps(variant: ForgeShaderVariant, palette: ShaderPalette) {
  const config = compositions[variant]
  return {
    ...Object.fromEntries(
      Object.entries(grid.props).map(([key, value]) => [key, value.default])
    ),
    ...Object.fromEntries(
      Object.entries(editorGrid).filter(([key]) => key in grid.props)
    ),
    color: palette.ink,
    cellColor: 'transparent',
    cells: clamp(
      Math.round(numeric(editorGrid, 'cells', 14) * config.cells),
      1,
      48
    ),
    rotation: numeric(editorGrid, 'rotation', 0) + config.rotation,
  }
}

export type ForgeShaderHandle = {
  pause(): void
  resume(): void
  resize(width: number, height: number): void
  update(variant: ForgeShaderVariant, palette: ShaderPalette): void
  destroy(): Promise<void>
}

/** This module is imported only after visibility, motion and capacity gates.
 * Use the package's public renderer API: its React root does not expose frame
 * rate or resolution controls. No account, preset API or telemetry is used. */
export function createForgeShader(
  canvas: HTMLCanvasElement,
  variant: ForgeShaderVariant,
  palette: ShaderPalette,
  callbacks: { ready(): void; unavailable(reason?: string): void }
): ForgeShaderHandle {
  const renderer = shaderRendererGPU()
  let disposed = false
  let initialized = false
  let playing = true
  let latestVariant = variant
  let latestPalette = palette
  const rootId = 'forge-root'
  const meshId = 'forge-field'
  const gridId = 'forge-lines'
  const update = (next: ForgeShaderVariant, colors: ShaderPalette) => {
    latestVariant = next
    latestPalette = colors
    if (!initialized || disposed) return
    for (const [key, value] of Object.entries(meshProps(next, colors))) {
      renderer.updateUniformValue(meshId, key, value)
    }
    for (const [key, value] of Object.entries(gridProps(next, colors))) {
      renderer.updateUniformValue(gridId, key, value)
    }
    renderer.updateNodeMetadata(gridId, {
      opacity: gridOpacity(next),
    })
  }
  renderer.setOnReady(() => {
    if (!disposed) callbacks.ready()
  })
  renderer.setOnUnavailable((reason) => {
    if (!disposed) callbacks.unavailable(reason)
  })
  renderer.setOnDeviceLost((reason) => {
    if (!disposed) callbacks.unavailable(reason)
  })
  const initialization = renderer
    .initialize({
      canvas,
      observeElement: false,
      colorSpace: 'srgb',
      toneMapping: 'linear',
      enablePerformanceTracking: false,
    })
    .then(() => {
      if (disposed) {
        // acquireRoot is asynchronous. Cleanup again after it resolves: an
        // earlier cleanup cannot release a GPU root which has not arrived yet.
        renderer.cleanup()
        return
      }
      if (renderer.getFailureReason()) {
        callbacks.unavailable(renderer.getFailureReason() ?? undefined)
        renderer.cleanup()
        return
      }
      renderer.stopAnimation()
      renderer.setResolutionScale(0.8)
      renderer.setFrameRateCap(
        variant === 'assistant'
          ? FORGE_SHADER_LIMITS.intentFrameRate
          : FORGE_SHADER_LIMITS.frameRate
      )
      renderer.registerNode(
        rootId,
        rootPassthrough.fragment,
        null,
        null,
        {},
        rootPassthrough
      )
      // These official definitions are the bridge's runtime inputs; its
      // declaration incorrectly makes their PropConfig transforms invariant.
      type BridgeDefinition = Parameters<typeof createGpuUniformsMap>[0]
      renderer.registerNode(
        meshId,
        mesh.fragment,
        rootId,
        { blendMode: 'normal', opacity: 1, visible: true, renderOrder: 0 },
        createGpuUniformsMap(
          mesh as unknown as BridgeDefinition,
          meshProps(latestVariant, latestPalette),
          meshId
        ),
        mesh
      )
      renderer.registerNode(
        gridId,
        grid.fragment,
        rootId,
        {
          blendMode: 'normal',
          visible: true,
          renderOrder: 1,
          opacity: gridOpacity(latestVariant),
        },
        createGpuUniformsMap(
          grid as unknown as BridgeDefinition,
          gridProps(latestVariant, latestPalette),
          gridId
        ),
        grid
      )
      initialized = true
      if (playing) renderer.startAnimation()
    })
    .catch(() => {
      renderer.cleanup()
      if (!disposed) callbacks.unavailable()
    })
  return {
    pause() {
      playing = false
      if (initialized && !disposed) renderer.stopAnimation()
    },
    resume() {
      playing = true
      if (initialized && !disposed) renderer.startAnimation()
    },
    resize(width, height) {
      if (initialized && !disposed) renderer.resize(width, height)
    },
    update,
    destroy() {
      if (disposed) return initialization
      disposed = true
      renderer.setOnReady(null)
      renderer.setOnUnavailable(null)
      renderer.setOnDeviceLost(null)
      renderer.cleanup()
      return initialization.then(() => {
        renderer.cleanup()
        const context = canvas.getContext('webgpu')
        if (
          context &&
          'unconfigure' in context &&
          typeof context.unconfigure === 'function'
        ) {
          context.unconfigure()
        }
      })
    },
  }
}
