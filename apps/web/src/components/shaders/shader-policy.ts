/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
export const FORGE_SHADER_LIMITS = {
  contexts: 1,
  width: 1024,
  height: 640,
  frameRate: 24,
  intentFrameRate: 18,
} as const

export type ForgeShaderVariant =
  | 'home'
  | 'store'
  | 'tools'
  | 'ecosystem'
  | 'future'
  | 'assistant'

export async function shaderCapability(
  gpu: { requestAdapter(): Promise<unknown | null> } | undefined
) {
  if (!gpu || typeof gpu.requestAdapter !== 'function') {
    return 'unsupported' as const
  }
  try {
    return (await gpu.requestAdapter())
      ? ('available' as const)
      : ('no-adapter' as const)
  } catch {
    return 'request-failed' as const
  }
}

export function shaderRenderSize(width: number, height: number) {
  if (
    !Number.isFinite(width) ||
    !Number.isFinite(height) ||
    width <= 0 ||
    height <= 0
  ) {
    return null
  }
  // The renderer caps its base DPR at 2. Size the CSS canvas before GPU
  // initialization, so even its first allocation fits the same pixel budget.
  const scale = Math.min(
    1,
    FORGE_SHADER_LIMITS.width / (width * 2),
    FORGE_SHADER_LIMITS.height / (height * 2)
  )
  return {
    width: Math.max(1, Math.floor(width * scale)),
    height: Math.max(1, Math.floor(height * scale)),
    scale: 1 / scale,
  }
}

export function shaderChapterVariant(
  chapter: string | undefined
): ForgeShaderVariant {
  return (
    (['home', 'store', 'tools', 'ecosystem', 'future'] as const)[
      Number(chapter)
    ] ?? 'home'
  )
}

export function canAnimateShader(state: {
  active: boolean
  visible: boolean
  documentVisible: boolean
  reducedMotion: boolean
  saveData: boolean
  homePaused: boolean
  intent: boolean
}) {
  return (
    state.active &&
    state.visible &&
    state.documentVisible &&
    !state.reducedMotion &&
    !state.saveData &&
    !state.homePaused &&
    state.intent
  )
}

const holders = new Set<symbol>()
const waiting = new Map<symbol, () => void>()

/** All routes share this limit; a list or a future consumer cannot create an
 * unbounded collection of independent render contexts. */
export function requestShaderSlot(onGranted: () => void) {
  const token = Symbol('forge-shader')
  const grant = () => {
    holders.add(token)
    onGranted()
  }
  if (holders.size < FORGE_SHADER_LIMITS.contexts) grant()
  else waiting.set(token, grant)
  return () => {
    waiting.delete(token)
    if (!holders.delete(token)) return
    const next = waiting.entries().next().value
    if (next) {
      waiting.delete(next[0])
      next[1]()
    }
  }
}
