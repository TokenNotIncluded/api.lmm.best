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
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from 'react'

import {
  DEFAULT_THEME,
  resolveTheme,
  THEME_COLORS,
  type ResolvedTheme,
  type Theme,
} from '@/context/theme'
import { usePreferencePreview } from '@/hooks/use-preference-preview'
import { getCookie, setCookie, removeCookie } from '@/lib/cookies'

const THEME_COOKIE_NAME = 'vite-ui-theme'
const THEME_COOKIE_MAX_AGE = 60 * 60 * 24 * 365 // 1 year
const THEMES = new Set<Theme>(['dark', 'light', 'system'])

type ThemeProviderProps = {
  children: React.ReactNode
  defaultTheme?: Theme
  storageKey?: string
}

type ThemeProviderState = {
  defaultTheme: Theme
  resolvedTheme: ResolvedTheme
  theme: Theme
  setTheme: (theme: Theme) => void
  previewTheme: (theme: Theme) => () => void
  resetTheme: () => void
}

const initialState: ThemeProviderState = {
  defaultTheme: DEFAULT_THEME,
  resolvedTheme: 'light',
  theme: DEFAULT_THEME,
  setTheme: () => null,
  previewTheme: () => () => {},
  resetTheme: () => null,
}

const ThemeContext = createContext<ThemeProviderState>(initialState)

function getSystemTheme(): ResolvedTheme {
  if (typeof window === 'undefined') return 'light'
  return window.matchMedia('(prefers-color-scheme: dark)').matches
    ? 'dark'
    : 'light'
}

function resolveCurrentTheme(theme: Theme): ResolvedTheme {
  return resolveTheme(theme, getSystemTheme() === 'dark')
}

function applyThemeToDocument(theme: ResolvedTheme) {
  const root = window.document.documentElement
  root.classList.remove('light', 'dark')
  root.classList.add(theme)
  window.document
    .querySelector("meta[name='theme-color']")
    ?.setAttribute('content', THEME_COLORS[theme])
}

function getStoredTheme(storageKey: string, fallback: Theme): Theme {
  const storedTheme = getCookie(storageKey) as Theme | undefined
  return storedTheme && THEMES.has(storedTheme) ? storedTheme : fallback
}

export function ThemeProvider({
  children,
  defaultTheme = DEFAULT_THEME,
  storageKey = THEME_COOKIE_NAME,
  ...props
}: ThemeProviderProps) {
  const [theme, _setTheme] = useState<Theme>(() =>
    getStoredTheme(storageKey, defaultTheme)
  )
  const {
    value: displayedTheme,
    preview: previewTheme,
    clear: clearPreview,
  } = usePreferencePreview(theme)
  const [resolvedTheme, setResolvedTheme] = useState<ResolvedTheme>(() =>
    resolveCurrentTheme(getStoredTheme(storageKey, defaultTheme))
  )

  useEffect(() => {
    const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)')

    const applyTheme = () => {
      const nextResolvedTheme = resolveCurrentTheme(displayedTheme)
      applyThemeToDocument(nextResolvedTheme)
      setResolvedTheme(nextResolvedTheme)
    }

    applyTheme()

    mediaQuery.addEventListener('change', applyTheme)

    return () => mediaQuery.removeEventListener('change', applyTheme)
  }, [displayedTheme])

  const setTheme = useCallback(
    (theme: Theme) => {
      clearPreview()
      setCookie(storageKey, theme, THEME_COOKIE_MAX_AGE)
      _setTheme(theme)
    },
    [storageKey, clearPreview]
  )

  const resetTheme = useCallback(() => {
    clearPreview()
    removeCookie(storageKey)
    _setTheme(defaultTheme)
  }, [defaultTheme, storageKey, clearPreview])

  const contextValue = useMemo(
    () => ({
      defaultTheme,
      resolvedTheme,
      resetTheme,
      theme,
      setTheme,
      previewTheme,
    }),
    [defaultTheme, resolvedTheme, resetTheme, theme, setTheme, previewTheme]
  )

  return (
    <ThemeContext value={contextValue} {...props}>
      {children}
    </ThemeContext>
  )
}

// eslint-disable-next-line react-refresh/only-export-components
export const useTheme = () => {
  const context = useContext(ThemeContext)

  if (!context) throw new Error('useTheme must be used within a ThemeProvider')

  return context
}
