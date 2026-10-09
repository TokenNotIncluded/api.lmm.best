/* Copyright (C) 2026 LIghtJUNction; licensed under AGPL-3.0-or-later. */
import { Component, lazy, Suspense, useContext, type ReactNode } from 'react'

import { isBoundedOpenUI } from './policy'
import { ResponseStreamingContext } from './streaming-context'

const OpenUIRenderer = lazy(() => import('./renderer'))
type BlockProps = { code: string; fallback: ReactNode }

class OpenUIBoundary extends Component<
  BlockProps & { children: ReactNode },
  { failed: boolean; code: string }
> {
  state = { failed: false, code: this.props.code }
  static getDerivedStateFromProps(props: BlockProps, state: { code: string }) {
    return props.code === state.code
      ? null
      : { failed: false, code: props.code }
  }
  static getDerivedStateFromError() {
    return { failed: true }
  }
  render() {
    return this.state.failed ? this.props.fallback : this.props.children
  }
}

export function OpenUIBlock({ code, fallback }: BlockProps) {
  const isStreaming = useContext(ResponseStreamingContext)
  if (!isBoundedOpenUI(code)) return fallback
  return (
    <OpenUIBoundary code={code} fallback={fallback}>
      <Suspense
        fallback={
          <div
            aria-busy='true'
            className='bg-muted my-4 h-24 rounded-xl motion-safe:animate-pulse'
          />
        }
      >
        <div
          className='my-5 min-w-0'
          inert={isStreaming}
          data-openui-view='true'
        >
          <OpenUIRenderer
            code={code}
            isStreaming={isStreaming}
            fallback={fallback}
          />
        </div>
      </Suspense>
    </OpenUIBoundary>
  )
}
