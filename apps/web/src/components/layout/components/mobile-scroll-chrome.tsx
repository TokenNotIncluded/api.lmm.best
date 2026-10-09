/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react'

import { observeMobileScroll } from '../lib/mobile-scroll-controller'

import './mobile-scroll-chrome.css'

const HiddenContext = createContext(false)

export function MobileScrollChromeProvider(props: {
  children: ReactNode
  resetKey: string
  documentScroll?: boolean
}) {
  const root = useRef<HTMLDivElement>(null)
  const { resetKey, documentScroll = false } = props
  const [state, setState] = useState({ resetKey, hidden: false })

  useEffect(() => {
    if (!root.current) return
    return observeMobileScroll(
      root.current,
      (hidden) => setState({ resetKey, hidden }),
      documentScroll
    )
  }, [resetKey, documentScroll])

  return (
    <HiddenContext.Provider value={state.resetKey === resetKey && state.hidden}>
      <div ref={root} className='contents'>
        {props.children}
      </div>
    </HiddenContext.Provider>
  )
}

export function MobileScrollChrome(props: {
  children: ReactNode
  overlay?: boolean
}) {
  const hidden = useContext(HiddenContext)
  return (
    <div
      data-mobile-scroll-chrome=''
      data-hidden={hidden}
      data-mode={props.overlay ? 'overlay' : 'flow'}
    >
      <div
        className='mobile-scroll-chrome-inner'
        inert={hidden}
        aria-hidden={hidden || undefined}
      >
        {props.children}
      </div>
    </div>
  )
}
