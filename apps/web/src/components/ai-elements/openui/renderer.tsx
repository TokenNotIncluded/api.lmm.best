/* Copyright (C) 2026 LIghtJUNction; licensed under AGPL-3.0-or-later. */
import { Renderer } from '@openuidev/react-lang'
import { useCallback, useState, type ReactNode } from 'react'

import { assistantOpenUILibrary } from './library'
export default function OpenUIRenderer({
  code,
  isStreaming,
  fallback,
}: {
  code: string
  isStreaming: boolean
  fallback: ReactNode
}) {
  const [failedCode, setFailedCode] = useState<string | null>(null)
  const onError = useCallback(
    (errors: readonly unknown[]) => {
      // Incomplete references are normal during streaming. Never log private output.
      if (!isStreaming && errors.length > 0) setFailedCode(code)
    },
    [code, isStreaming]
  )
  if (!isStreaming && failedCode === code) return fallback
  return (
    <Renderer
      key={isStreaming ? 'stream' : 'complete'}
      response={code}
      library={assistantOpenUILibrary}
      isStreaming={isStreaming}
      onError={onError}
    />
  )
}
