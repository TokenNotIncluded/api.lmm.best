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
import type { QueryClient } from '@tanstack/react-query'
import { ReactQueryDevtools } from '@tanstack/react-query-devtools'
import {
  createRootRouteWithContext,
  Outlet,
  redirect,
  useNavigate,
  useRouterState,
} from '@tanstack/react-router'
import { TanStackRouterDevtools } from '@tanstack/react-router-devtools'
import { lazy, Suspense, useEffect } from 'react'

import { FeedbackRewardButton } from '@/components/feedback-reward-button'
import { Footer } from '@/components/layout/components/footer'
import { NavigationProgress } from '@/components/navigation-progress'
import { Toaster } from '@/components/ui/sonner'
import { ThemeCustomizationProvider } from '@/context/theme-customization-provider'
import { saveAffiliateCode } from '@/features/auth/lib/storage'
import { GeneralError } from '@/features/errors/general-error'
import { NotFoundError } from '@/features/errors/not-found-error'
import { getSetupStatus } from '@/features/setup/api'
import { useSystemConfig } from '@/hooks/use-system-config'
import {
  bootstrapAuthentication,
  clearAuthentication,
} from '@/lib/auth-session'
import { subscribeAuthSessionEvents } from '@/lib/auth-session-sync'
import {
  isConsoleActivated,
  isRestrictedPublicRoute,
} from '@/lib/console-activation'
import { resolveLegacyRoute } from '@/lib/legacy-route'
import { useAuthStore } from '@/stores/auth-store'

const PersonaDebugPanel = __LMM_PERSONA_DEBUG__
  ? lazy(() =>
      import('@/features/debug/persona-debug-panel').then((module) => ({
        default: module.PersonaDebugPanel,
      }))
    )
  : null

function RootComponent() {
  const navigate = useNavigate()
  const pathname = useRouterState({
    select: (state) => state.location.pathname,
  })
  const isHomeIntroSurface = isHomeIntroPath(pathname)

  // Load system configuration (logo, system name, etc.) from backend
  useSystemConfig({ autoLoad: true })

  useEffect(() => {
    const aff = new URLSearchParams(window.location.search).get('aff')?.trim()
    if (aff) {
      saveAffiliateCode(aff)
    }
  }, [])

  useEffect(() => {
    if (__LMM_PERSONA_DEBUG__) return
    return subscribeAuthSessionEvents((event) => {
      const currentSID = useAuthStore.getState().auth.session?.sid

      if (event.kind === 'authenticated') {
        if (event.sid === currentSID) return
        if (currentSID) {
          clearAuthentication(false)
        }
        window.location.reload()
        return
      }

      if (currentSID && event.sid === currentSID) {
        clearAuthentication(false)
        void navigate({ to: '/sign-in', replace: true })
      }
    })
  }, [navigate])

  return (
    <ThemeCustomizationProvider>
      <NavigationProgress />
      <Outlet />
      {isHomeIntroSurface && <Footer />}
      {isHomeIntroSurface && <FeedbackRewardButton />}
      <Toaster closeButton duration={5000} position='top-center' richColors />
      {PersonaDebugPanel &&
      document.documentElement.dataset.personaDebug === 'true' ? (
        <Suspense fallback={null}>
          <PersonaDebugPanel />
        </Suspense>
      ) : null}
      {import.meta.env.DEV &&
        import.meta.env.VITE_ENABLE_DEVTOOLS === 'true' && (
          <>
            <ReactQueryDevtools buttonPosition='bottom-left' />
            <TanStackRouterDevtools position='bottom-right' />
          </>
        )}
    </ThemeCustomizationProvider>
  )
}

function isHomeIntroPath(pathname: string): boolean {
  return pathname === '/'
}

// 缓存 setup 状态检查结果，避免每次导航都重复调用 API
// 使用 localStorage 持久化，避免页面刷新后重复检查
const SETUP_CHECKED_KEY = 'setup_status_checked'

function getSetupStatusFromCache(): boolean {
  try {
    if (typeof window !== 'undefined') {
      return window.localStorage.getItem(SETUP_CHECKED_KEY) === 'true'
    }
  } catch {
    /* empty */
  }
  return false
}

function setSetupStatusCache(value: boolean): void {
  try {
    if (typeof window !== 'undefined') {
      if (value) {
        window.localStorage.setItem(SETUP_CHECKED_KEY, 'true')
      } else {
        window.localStorage.removeItem(SETUP_CHECKED_KEY)
      }
    }
  } catch {
    /* empty */
  }
}

// 内存中的标记，避免同一会话中重复检查
let setupStatusChecked = getSetupStatusFromCache()

const NON_BLOCKING_PUBLIC_PATHS = [
  '/',
  '/challenges',
  '/pricing',
  '/status',
  '/privacy-policy',
  '/user-agreement',
  '/terms',
  '/terms-of-service',
  '/sign-in',
  '/sign-up',
  '/signup',
  '/register',
  '/forgot-password',
  '/reset',
  '/otp',
  '/oauth',
] as const

function isNonBlockingPublicPath(pathname: string): boolean {
  return NON_BLOCKING_PUBLIC_PATHS.some(
    (path) =>
      pathname === path || (path !== '/' && pathname.startsWith(`${path}/`))
  )
}

async function resolveWithTimeout<T>(
  promise: Promise<T>,
  timeoutMs: number
): Promise<T | null> {
  let timeoutId: ReturnType<typeof setTimeout> | undefined
  try {
    return await Promise.race([
      promise,
      new Promise<null>((resolve) => {
        timeoutId = setTimeout(() => resolve(null), timeoutMs)
      }),
    ])
  } finally {
    if (timeoutId !== undefined) clearTimeout(timeoutId)
  }
}

export const Route = createRootRouteWithContext<{
  queryClient: QueryClient
}>()({
  // 应用初始化与路由解析前统一校验会话
  beforeLoad: async ({ location }) => {
    const legacyTarget = resolveLegacyRoute(location.href)
    if (legacyTarget) {
      throw redirect({ href: legacyTarget, replace: true })
    }

    const pathname = location?.pathname || ''
    const needsSetupCheck =
      !setupStatusChecked && !pathname.startsWith('/setup')
    const nonBlockingPublicPath = isNonBlockingPublicPath(pathname)
    const authBootstrap = nonBlockingPublicPath
      ? null
      : bootstrapAuthentication()

    // 只检查 setup 状态（如果需要）
    if (needsSetupCheck) {
      const status = await resolveWithTimeout(
        getSetupStatus().catch((error) => {
          if (import.meta.env.DEV) {
            // eslint-disable-next-line no-console
            console.warn('[root.beforeLoad] setup status check failed', error)
          }
          return null
        }),
        2_000
      )

      if (status?.success && status.data && !status.data.status) {
        setupStatusChecked = false
        setSetupStatusCache(false)
        throw redirect({ to: '/setup' })
      }
      if (status?.success && status.data?.status) {
        setupStatusChecked = true
        setSetupStatusCache(true)
      }
    }

    if (authBootstrap) {
      await resolveWithTimeout(authBootstrap, 2_500)
    } else {
      // Public pages should paint even while an unavailable API is recovering.
      // The store will update the header and CTA when a session eventually
      // resolves, without holding the router's first render hostage.
      void bootstrapAuthentication().catch(() => undefined)
    }

    if (
      isRestrictedPublicRoute(pathname) &&
      !isConsoleActivated(useAuthStore.getState().auth.user)
    ) {
      throw redirect({ to: '/challenges' })
    }
  },
  component: RootComponent,
  notFoundComponent: NotFoundError,
  errorComponent: GeneralError,
})
