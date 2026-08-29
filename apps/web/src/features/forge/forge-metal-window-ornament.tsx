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
import {
  Component,
  type ComponentType,
  type ErrorInfo,
  type ReactNode,
  useCallback,
  useEffect,
  useState,
  useSyncExternalStore,
} from 'react'

import {
  type ForgeMetalWindowOrnamentCapabilities,
  isAppleWebKitBrowser,
  shouldEnableForgeMetalWindowOrnament,
} from './forge-metal-window-ornament-policy'

import styles from './forge-metal-window-ornament.module.css'

export interface ForgeMetalWindowOrnamentEnvironment {
  read: () => ForgeMetalWindowOrnamentCapabilities
  subscribe: (listener: () => void) => () => void
}

export type ForgeMetalWindowOrnamentRuntimeComponent = ComponentType

export type ForgeMetalWindowOrnamentRuntimeLoader = () => Promise<{
  ForgeMetalWindowOrnamentRuntime: ForgeMetalWindowOrnamentRuntimeComponent
}>

interface ForgeMetalWindowOrnamentProps {
  environment?: ForgeMetalWindowOrnamentEnvironment
  loadRuntime?: ForgeMetalWindowOrnamentRuntimeLoader
}

const MEDIA_QUERIES = [
  '(prefers-reduced-motion: reduce)',
  '(forced-colors: active)',
  '(pointer: coarse)',
  '(max-width: 767px)',
] as const

interface NetworkInformationLike extends EventTarget {
  readonly saveData?: boolean
}

function blockedCapabilities(): ForgeMetalWindowOrnamentCapabilities {
  return {
    appleWebKit: true,
    coarsePointer: true,
    forcedColors: true,
    narrowViewport: true,
    reducedMotion: true,
    saveData: true,
    supportsAnimationFrame: false,
    supportsCanvas2D: false,
    supportsIntersectionObserver: false,
    supportsResizeObserver: false,
    supportsRoundRect: false,
    supportsWebGL: false,
  }
}

type RenderingSupport = Pick<
  ForgeMetalWindowOrnamentCapabilities,
  | 'supportsAnimationFrame'
  | 'supportsCanvas2D'
  | 'supportsIntersectionObserver'
  | 'supportsResizeObserver'
  | 'supportsRoundRect'
  | 'supportsWebGL'
>

let cachedRenderingSupport: RenderingSupport | undefined

function readRenderingSupport(): RenderingSupport {
  if (cachedRenderingSupport) return cachedRenderingSupport

  const canvas2D = document.createElement('canvas').getContext('2d')
  const webGlCanvas = document.createElement('canvas')
  const webGl = (webGlCanvas.getContext('webgl') ??
    webGlCanvas.getContext(
      'experimental-webgl'
    )) as WebGLRenderingContext | null
  const supportsWebGL = webGl !== null
  webGl?.getExtension('WEBGL_lose_context')?.loseContext()

  cachedRenderingSupport = {
    supportsAnimationFrame:
      typeof globalThis.requestAnimationFrame === 'function' &&
      typeof globalThis.cancelAnimationFrame === 'function',
    supportsCanvas2D: canvas2D !== null,
    supportsIntersectionObserver:
      typeof globalThis.IntersectionObserver === 'function',
    supportsResizeObserver: typeof globalThis.ResizeObserver === 'function',
    supportsRoundRect:
      canvas2D !== null && typeof canvas2D.roundRect === 'function',
    supportsWebGL,
  }
  return cachedRenderingSupport
}

function browserEnvironment(): ForgeMetalWindowOrnamentEnvironment {
  const read = (): ForgeMetalWindowOrnamentCapabilities => {
    try {
      if (
        typeof window === 'undefined' ||
        typeof document === 'undefined' ||
        typeof navigator === 'undefined' ||
        typeof window.matchMedia !== 'function'
      ) {
        return blockedCapabilities()
      }

      const reducedMotion = window.matchMedia(MEDIA_QUERIES[0]).matches
      const forcedColors = window.matchMedia(MEDIA_QUERIES[1]).matches
      const coarsePointer = window.matchMedia(MEDIA_QUERIES[2]).matches
      const narrowViewport = window.matchMedia(MEDIA_QUERIES[3]).matches
      const connection = (
        navigator as Navigator & { connection?: NetworkInformationLike }
      ).connection
      const renderingSupport = readRenderingSupport()

      return {
        appleWebKit: isAppleWebKitBrowser(navigator.userAgent),
        coarsePointer,
        forcedColors,
        narrowViewport,
        reducedMotion,
        saveData: connection?.saveData === true,
        ...renderingSupport,
      }
    } catch {
      return blockedCapabilities()
    }
  }

  const subscribe = (listener: () => void) => {
    const removeListeners: Array<() => void> = []
    const cleanUp = () => {
      for (const removeListener of removeListeners.splice(0)) {
        try {
          removeListener()
        } catch {
          // Optional capability observers must not destabilize the host.
        }
      }
    }

    try {
      if (
        typeof window === 'undefined' ||
        typeof navigator === 'undefined' ||
        typeof window.matchMedia !== 'function'
      ) {
        return cleanUp
      }

      for (const query of MEDIA_QUERIES) {
        const mediaQuery = window.matchMedia(query)
        mediaQuery.addEventListener('change', listener)
        removeListeners.push(() =>
          mediaQuery.removeEventListener('change', listener)
        )
      }

      const connection = (
        navigator as Navigator & { connection?: NetworkInformationLike }
      ).connection
      if (connection) {
        connection.addEventListener('change', listener)
        removeListeners.push(() =>
          connection.removeEventListener('change', listener)
        )
      }

      return cleanUp
    } catch {
      cleanUp()
      return () => undefined
    }
  }

  return { read, subscribe }
}

const defaultEnvironment = browserEnvironment()

// Keep the shader package isolated in its own Forge-only async chunk.
const loadDefaultRuntime: ForgeMetalWindowOrnamentRuntimeLoader = () =>
  import('./forge-metal-window-ornament-runtime')

interface EnhancementBoundaryProps {
  children: ReactNode
  onError: () => void
}

class EnhancementBoundary extends Component<
  EnhancementBoundaryProps,
  { failed: boolean }
> {
  state = { failed: false }

  static getDerivedStateFromError() {
    return { failed: true }
  }

  componentDidCatch(_error: Error, _info: ErrorInfo) {
    this.props.onError()
  }

  render() {
    return this.state.failed ? null : this.props.children
  }
}

export function ForgeMetalWindowOrnament({
  environment = defaultEnvironment,
  loadRuntime = loadDefaultRuntime,
}: ForgeMetalWindowOrnamentProps) {
  const [Runtime, setRuntime] =
    useState<ForgeMetalWindowOrnamentRuntimeComponent | null>(null)
  const subscribe = useCallback(
    (listener: () => void) => {
      try {
        const cleanUp = environment.subscribe(listener)
        if (typeof cleanUp !== 'function') return () => undefined
        return () => {
          try {
            cleanUp()
          } catch {
            // A decorative enhancement must never fail during unmount.
          }
        }
      } catch {
        return () => undefined
      }
    },
    [environment]
  )
  const getSnapshot = useCallback(() => {
    try {
      return shouldEnableForgeMetalWindowOrnament(environment.read())
    } catch {
      return false
    }
  }, [environment])
  const enabled = useSyncExternalStore(subscribe, getSnapshot, () => false)

  useEffect(() => {
    if (!enabled) return

    let active = true
    void loadRuntime()
      .then((module) => {
        if (active) setRuntime(() => module.ForgeMetalWindowOrnamentRuntime)
      })
      .catch(() => {
        if (active) setRuntime(null)
      })

    return () => {
      active = false
    }
  }, [enabled, loadRuntime])

  const ActiveRuntime = enabled ? Runtime : null

  return (
    <div
      aria-hidden='true'
      className={styles.root}
      data-forge-metal-window-ornament={ActiveRuntime ? 'enhanced' : 'static'}
      inert
    >
      <span className={styles.fallback} data-forge-metal-window-fallback>
        <span
          className={`${styles.dot} ${styles.dotClose}`}
          data-forge-metal-window-dot
        />
        <span
          className={`${styles.dot} ${styles.dotMinimize}`}
          data-forge-metal-window-dot
        />
        <span
          className={`${styles.dot} ${styles.dotExpand}`}
          data-forge-metal-window-dot
        />
      </span>
      {ActiveRuntime ? (
        <EnhancementBoundary onError={() => setRuntime(null)}>
          <ActiveRuntime />
        </EnhancementBoundary>
      ) : null}
    </div>
  )
}
