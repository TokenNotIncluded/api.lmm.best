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
  type ForgeLiquidAccentCapabilities,
  shouldEnableForgeLiquidAccent,
} from './forge-liquid-accent-policy'

import styles from './forge-liquid-accent.module.css'

export interface ForgeLiquidAccentEnvironment {
  read: () => ForgeLiquidAccentCapabilities
  subscribe: (listener: () => void) => () => void
}

export type ForgeLiquidAccentRuntimeComponent = ComponentType

export type ForgeLiquidAccentRuntimeLoader = () => Promise<{
  ForgeLiquidAccentRuntime: ForgeLiquidAccentRuntimeComponent
}>

interface ForgeLiquidAccentProps {
  environment?: ForgeLiquidAccentEnvironment
  loadRuntime?: ForgeLiquidAccentRuntimeLoader
}

const MEDIA_QUERIES = [
  '(prefers-reduced-motion: reduce)',
  '(forced-colors: active)',
  '(pointer: coarse)',
  '(max-width: 767px)',
] as const

function browserEnvironment(): ForgeLiquidAccentEnvironment {
  const read = (): ForgeLiquidAccentCapabilities => {
    if (
      typeof window === 'undefined' ||
      typeof window.matchMedia !== 'function'
    ) {
      return {
        coarsePointer: true,
        forcedColors: false,
        narrowViewport: true,
        reducedMotion: true,
        supportsResizeObserver: false,
        supportsSvgFilters: false,
      }
    }

    return {
      reducedMotion: window.matchMedia(MEDIA_QUERIES[0]).matches,
      forcedColors: window.matchMedia(MEDIA_QUERIES[1]).matches,
      coarsePointer: window.matchMedia(MEDIA_QUERIES[2]).matches,
      narrowViewport: window.matchMedia(MEDIA_QUERIES[3]).matches,
      supportsResizeObserver: typeof ResizeObserver !== 'undefined',
      supportsSvgFilters:
        typeof CSS !== 'undefined' &&
        typeof CSS.supports === 'function' &&
        CSS.supports('filter', 'blur(1px)') &&
        typeof document.createElementNS === 'function',
    }
  }

  const subscribe = (listener: () => void) => {
    if (
      typeof window === 'undefined' ||
      typeof window.matchMedia !== 'function'
    ) {
      return () => undefined
    }

    const queries = MEDIA_QUERIES.map((query) => window.matchMedia(query))
    for (const query of queries) {
      query.addEventListener('change', listener)
    }
    return () => {
      for (const query of queries) {
        query.removeEventListener('change', listener)
      }
    }
  }

  return { read, subscribe }
}

const defaultEnvironment = browserEnvironment()

// Keep the third-party package isolated in its own Forge-only async chunk.
const loadDefaultRuntime: ForgeLiquidAccentRuntimeLoader = () =>
  import('./forge-liquid-accent-runtime')

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

export function ForgeLiquidAccent({
  environment = defaultEnvironment,
  loadRuntime = loadDefaultRuntime,
}: ForgeLiquidAccentProps) {
  const [Runtime, setRuntime] =
    useState<ForgeLiquidAccentRuntimeComponent | null>(null)
  const subscribe = useCallback(
    (listener: () => void) => {
      try {
        return environment.subscribe(listener)
      } catch {
        return () => undefined
      }
    },
    [environment]
  )
  const getSnapshot = useCallback(() => {
    try {
      return shouldEnableForgeLiquidAccent(environment.read())
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
        if (active) setRuntime(() => module.ForgeLiquidAccentRuntime)
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
      data-forge-liquid-accent={ActiveRuntime ? 'enhanced' : 'static'}
    >
      <div className={styles.fallback}>
        <span className={`${styles.fallbackBlob} ${styles.fallbackPrimary}`} />
        <span className={`${styles.fallbackBlob} ${styles.fallbackBridge}`} />
        <span className={`${styles.fallbackBlob} ${styles.fallbackSmall}`} />
      </div>
      {ActiveRuntime ? (
        <EnhancementBoundary onError={() => setRuntime(null)}>
          <ActiveRuntime />
        </EnhancementBoundary>
      ) : null}
    </div>
  )
}
