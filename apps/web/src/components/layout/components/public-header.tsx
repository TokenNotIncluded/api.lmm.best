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
import { Link, useNavigate, useRouterState } from '@tanstack/react-router'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { LanguageSwitcher } from '@/components/language-switcher'
import { LMM_BRAND_NAME } from '@/components/lmm-brand-mark'
import { NotificationPopover } from '@/components/notification-popover'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { ThemeSwitch } from '@/components/theme-switch'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useNotifications } from '@/hooks/use-notifications'
import { useSystemConfig } from '@/hooks/use-system-config'
import { useTopNavLinks } from '@/hooks/use-top-nav-links'
import { getAuthenticatedLandingRoute } from '@/lib/console-activation'
import { DEFAULT_LOGO, DEFAULT_SYSTEM_NAME } from '@/lib/constants'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { defaultTopNavLinks } from '../config/top-nav.config'
import type { TopNavLink } from '../types'
import { HeaderLogo } from './header-logo'
import { getNavLinkKey } from './nav-link-key'

const AUTH_PROMPT_SECONDS = 5

type AuthPromptTarget = {
  title: string
  href: string
}

function getDisplaySiteName(
  customSiteName: string | undefined,
  usesDefaultBrand: boolean,
  systemName: string
) {
  if (customSiteName) return customSiteName
  if (usesDefaultBrand && systemName === DEFAULT_SYSTEM_NAME) {
    return LMM_BRAND_NAME
  }
  return systemName
}

function getPublicNavClassName(editorialHeader: boolean | undefined) {
  return cn(
    'flex h-16 items-center justify-between border-b px-2',
    editorialHeader && 'forge-public-nav'
  )
}

function getDesktopNavLinkClassName(
  editorialHeader: boolean | undefined,
  isActive: boolean,
  disabled: boolean | undefined
) {
  let stateClassName: string
  if (editorialHeader) {
    stateClassName = isActive
      ? 'forge-public-nav-link-active'
      : 'forge-public-nav-link'
  } else {
    stateClassName = isActive
      ? 'text-foreground'
      : 'text-foreground/68 hover:text-foreground'
  }

  return cn(
    'focus-visible:ring-ring focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none touch-manipulation rounded-lg px-3 py-1.5 text-[13px] font-medium transition-colors duration-200',
    stateClassName,
    disabled && 'pointer-events-none opacity-50'
  )
}

function getMobileNavLinkClassName(
  editorialHeader: boolean | undefined,
  isActive: boolean,
  mobileOpen: boolean,
  disabled: boolean | undefined
) {
  let stateClassName: string
  if (editorialHeader) {
    stateClassName = isActive
      ? 'forge-public-mobile-link-active'
      : 'forge-public-mobile-link'
  } else {
    stateClassName = isActive ? 'text-foreground' : 'text-muted-foreground'
  }

  return cn(
    'focus-visible:ring-ring focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none touch-manipulation flex min-h-11 items-center gap-3 py-3 text-base font-medium tracking-tight transition-all duration-500 ease-[cubic-bezier(0.16,1,0.3,1)]',
    mobileOpen ? 'translate-y-0 opacity-100' : 'translate-y-4 opacity-0',
    stateClassName,
    disabled && 'pointer-events-none opacity-50'
  )
}

export interface PublicHeaderProps {
  navLinks?: TopNavLink[]
  mobileLinks?: TopNavLink[]
  navContent?: React.ReactNode
  showThemeSwitch?: boolean
  showLanguageSwitcher?: boolean
  logo?: React.ReactNode
  siteName?: string
  homeUrl?: string
  leftContent?: React.ReactNode
  rightContent?: React.ReactNode
  showNavigation?: boolean
  showAuthButtons?: boolean
  showNotifications?: boolean
  useDynamicNavLinks?: boolean
  className?: string
}

export function PublicHeader(props: PublicHeaderProps) {
  const {
    navLinks = defaultTopNavLinks,
    showThemeSwitch = true,
    showLanguageSwitcher = true,
    logo: customLogo,
    siteName: customSiteName,
    homeUrl = '/',
    showAuthButtons = true,
    showNotifications = true,
    useDynamicNavLinks = true,
  } = props

  const { t } = useTranslation()
  const navigate = useNavigate()
  // The mobile menu is derived from the pathname it was opened on: any
  // navigation (link click, programmatic, browser back) automatically closes
  // it during render — no pathname effect or cascading setState needed.
  const [mobileMenuPath, setMobileMenuPath] = useState<string | null>(null)
  const [authPromptTarget, setAuthPromptTarget] =
    useState<AuthPromptTarget | null>(null)
  const [authPromptSecondsLeft, setAuthPromptSecondsLeft] =
    useState(AUTH_PROMPT_SECONDS)
  const mobileMenuRef = useRef<HTMLDivElement>(null)
  const mobileMenuButtonRef = useRef<HTMLButtonElement>(null)
  const { auth } = useAuthStore()
  const {
    systemName,
    logo: systemLogo,
    loading,
    logoLoaded,
  } = useSystemConfig()
  const dynamicLinks = useTopNavLinks()
  const notifications = useNotifications()
  const routerState = useRouterState()
  const pathname = routerState.location.pathname

  const user = auth.user
  const editorialHeader = props.className?.includes('forge-public-header')
  const isAuthenticated = !!user
  const usesDefaultBrand = !customLogo && systemLogo === DEFAULT_LOGO
  const displaySiteName = getDisplaySiteName(
    customSiteName,
    usesDefaultBrand,
    systemName
  )
  const links =
    useDynamicNavLinks && dynamicLinks.length > 0 ? dynamicLinks : navLinks
  const mobileNavigationLinks = props.mobileLinks ?? links

  const mobileOpen = mobileMenuPath === pathname
  const closeMobileMenu = useCallback(() => setMobileMenuPath(null), [])
  const toggleMobileMenu = useCallback(() => {
    setMobileMenuPath(mobileOpen ? null : pathname)
  }, [mobileOpen, pathname])

  useEffect(() => {
    const previousOverflow = document.body.style.overflow
    if (mobileOpen) document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = previousOverflow
    }
  }, [mobileOpen])

  useEffect(() => {
    if (!mobileOpen) return

    const menu = mobileMenuRef.current
    const focusable = menu
      ? [
          ...menu.querySelectorAll<HTMLElement>(
            'a[href], button:not([disabled]), [tabindex]:not([tabindex="-1"])'
          ),
        ]
      : []
    const firstFocusable = focusable[0]
    firstFocusable?.focus()

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        closeMobileMenu()
        mobileMenuButtonRef.current?.focus()
        return
      }
      if (event.key !== 'Tab' || focusable.length === 0) return

      const activeIndex = focusable.indexOf(
        document.activeElement as HTMLElement
      )
      if (event.shiftKey && (activeIndex <= 0 || activeIndex === -1)) {
        event.preventDefault()
        focusable.at(-1)?.focus()
      } else if (!event.shiftKey && activeIndex === focusable.length - 1) {
        event.preventDefault()
        focusable[0]?.focus()
      }
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [mobileOpen, closeMobileMenu])

  useEffect(() => {
    if (!authPromptTarget) return

    const intervalId = window.setInterval(() => {
      setAuthPromptSecondsLeft((seconds) => Math.max(seconds - 1, 0))
    }, 1000)

    const timeoutId = window.setTimeout(() => {
      const redirect = authPromptTarget.href
      setAuthPromptTarget(null)
      navigate({ to: '/sign-in', search: { redirect } })
    }, AUTH_PROMPT_SECONDS * 1000)

    return () => {
      window.clearInterval(intervalId)
      window.clearTimeout(timeoutId)
    }
  }, [authPromptTarget, navigate])

  const closeAuthPrompt = useCallback(() => {
    setAuthPromptTarget(null)
    setAuthPromptSecondsLeft(AUTH_PROMPT_SECONDS)
  }, [])

  const navigateToSignIn = useCallback(() => {
    const redirect = authPromptTarget?.href || '/'
    setAuthPromptTarget(null)
    navigate({ to: '/sign-in', search: { redirect } })
  }, [authPromptTarget?.href, navigate])

  const handleNavLinkClick = useCallback(
    (
      event: React.MouseEvent<HTMLAnchorElement>,
      link: TopNavLink,
      closeMobile = false
    ) => {
      if (link.disabled) {
        event.preventDefault()
        return
      }

      if (link.requiresAuth) {
        event.preventDefault()
        if (closeMobile) {
          closeMobileMenu()
        }
        setAuthPromptSecondsLeft(AUTH_PROMPT_SECONDS)
        setAuthPromptTarget({
          title: t(link.title),
          href: link.href,
        })
        return
      }

      if (closeMobile) {
        closeMobileMenu()
      }
    },
    [t, closeMobileMenu]
  )

  let logoContent = customLogo
  if (loading) {
    logoContent = <Skeleton className='size-full rounded-lg' />
  } else if (!customLogo) {
    logoContent = (
      <HeaderLogo
        src={systemLogo}
        loading={loading}
        logoLoaded={logoLoaded}
        className='size-full rounded-lg object-contain'
      />
    )
  }

  let desktopAuthContent = <ProfileDropdown />
  if (loading) {
    desktopAuthContent = <Skeleton className='h-8 w-20 rounded-lg' />
  } else if (!isAuthenticated) {
    desktopAuthContent = (
      <Button
        size='sm'
        className={cn(
          'h-8 rounded-lg px-3.5 text-xs font-medium',
          editorialHeader && 'forge-public-sign-in'
        )}
        render={<Link to='/sign-in' />}
      >
        {t('Sign in')}
      </Button>
    )
  }

  return (
    <>
      <header
        className={cn(
          'public-header pointer-events-none fixed inset-x-0 top-0 z-50',
          'bg-background/50 backdrop-blur-2xl',
          props.className
        )}
      >
        <div className='pointer-events-auto mx-auto max-w-7xl px-4 pt-0 md:px-6'>
          <nav
            className={getPublicNavClassName(editorialHeader)}
            aria-label={t('Header navigation')}
          >
            {/* Logo */}
            <Link
              to={homeUrl}
              aria-label={displaySiteName}
              className='group focus-visible:ring-ring flex shrink-0 touch-manipulation items-center gap-2.5 focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none'
            >
              <div className='flex size-7 shrink-0 items-center justify-center transition-all duration-300 group-hover:scale-105'>
                {logoContent}
              </div>
              <span className='text-sm font-semibold tracking-tight'>
                {loading ? <Skeleton className='h-4 w-16' /> : displaySiteName}
              </span>
            </Link>

            {/* Desktop nav */}
            <div className='hidden items-center gap-0.5 sm:flex'>
              {links.map((link) => {
                const isActive = pathname === link.href
                if (link.external) {
                  return (
                    <a
                      key={getNavLinkKey(link)}
                      href={link.href}
                      target='_blank'
                      rel='noopener noreferrer'
                      aria-current={isActive ? 'page' : undefined}
                      aria-disabled={link.disabled || undefined}
                      tabIndex={link.disabled ? -1 : undefined}
                      onClick={(event) => handleNavLinkClick(event, link)}
                      className={getDesktopNavLinkClassName(
                        editorialHeader,
                        isActive,
                        link.disabled
                      )}
                    >
                      {t(link.title)}
                    </a>
                  )
                }
                return (
                  <Link
                    key={getNavLinkKey(link)}
                    to={link.href}
                    disabled={link.disabled}
                    aria-current={isActive ? 'page' : undefined}
                    aria-disabled={link.disabled || undefined}
                    tabIndex={link.disabled ? -1 : undefined}
                    onClick={(event) => handleNavLinkClick(event, link)}
                    className={getDesktopNavLinkClassName(
                      editorialHeader,
                      isActive,
                      link.disabled
                    )}
                  >
                    {t(link.title)}
                  </Link>
                )
              })}

              {(showLanguageSwitcher ||
                showThemeSwitch ||
                showNotifications) && (
                <div className='bg-border/40 mx-2 h-4 w-px' />
              )}

              {showLanguageSwitcher && <LanguageSwitcher />}
              {showThemeSwitch && <ThemeSwitch />}
              {showNotifications && (
                <NotificationPopover
                  open={notifications.popoverOpen}
                  onOpenChange={notifications.setPopoverOpen}
                  unreadCount={notifications.unreadCount}
                  activeTab={notifications.activeTab}
                  onTabChange={notifications.setActiveTab}
                  notice={notifications.notice}
                  announcements={notifications.announcements}
                  bountyTips={notifications.bountyTips}
                  thankingTipId={notifications.thankingTipId}
                  onThankTip={notifications.thankTip}
                  loading={notifications.loading}
                />
              )}

              {showAuthButtons && (
                <div className='contents'>
                  <div className='bg-border/40 mx-1 h-4 w-px' />
                  {desktopAuthContent}
                </div>
              )}
            </div>

            {/* Mobile: compact actions + hamburger */}
            <div className='public-header-mobile-actions flex items-center gap-2 sm:hidden'>
              {showThemeSwitch && <ThemeSwitch />}
              {showAuthButtons && !loading && isAuthenticated && (
                <ProfileDropdown />
              )}
              <Button
                type='button'
                variant='ghost'
                size='icon'
                className='focus-visible:ring-ring size-11 touch-manipulation focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none'
                ref={mobileMenuButtonRef}
                onClick={toggleMobileMenu}
                aria-label={t('Toggle navigation menu')}
                aria-expanded={mobileOpen}
                aria-controls='public-mobile-navigation'
                aria-haspopup='dialog'
              >
                <div className='relative size-4'>
                  <span
                    className={cn(
                      'absolute inset-x-0 block h-[1.5px] origin-center rounded-full bg-current transition-all duration-300',
                      mobileOpen ? 'top-[7px] rotate-45' : 'top-[3px]'
                    )}
                  />
                  <span
                    className={cn(
                      'absolute inset-x-0 top-[7px] block h-[1.5px] rounded-full bg-current transition-all duration-300',
                      mobileOpen ? 'scale-x-0 opacity-0' : 'opacity-100'
                    )}
                  />
                  <span
                    className={cn(
                      'absolute inset-x-0 block h-[1.5px] origin-center rounded-full bg-current transition-all duration-300',
                      mobileOpen ? 'top-[7px] -rotate-45' : 'top-[11px]'
                    )}
                  />
                </div>
              </Button>
            </div>
          </nav>
        </div>
      </header>

      {/* Mobile full-screen overlay */}
      <div
        className={cn(
          'fixed inset-0 z-40 transition-all duration-500 ease-[cubic-bezier(0.16,1,0.3,1)] sm:pointer-events-none sm:hidden',
          editorialHeader
            ? 'forge-public-mobile-overlay'
            : 'bg-background text-foreground',
          mobileOpen
            ? 'pointer-events-auto opacity-100'
            : 'pointer-events-none opacity-0'
        )}
      >
        <div
          id='public-mobile-navigation'
          ref={mobileMenuRef}
          role='dialog'
          aria-modal='true'
          aria-label={t('Header navigation')}
          aria-hidden={!mobileOpen}
          className='public-mobile-navigation flex h-full flex-col justify-between px-8 pt-[calc(5rem+env(safe-area-inset-top))] pb-[max(2.5rem,env(safe-area-inset-bottom))]'
        >
          <nav
            aria-label={t('Header navigation')}
            className='flex flex-col gap-1'
          >
            {mobileNavigationLinks.map((link, i) => {
              const isActive = pathname === link.href
              const linkClassName = getMobileNavLinkClassName(
                editorialHeader,
                isActive,
                mobileOpen,
                link.disabled
              )
              const transitionStyle = {
                transitionDelay: mobileOpen ? `${100 + i * 50}ms` : '0ms',
              }
              if (link.external) {
                return (
                  <a
                    key={getNavLinkKey(link)}
                    href={link.href}
                    target='_blank'
                    rel='noopener noreferrer'
                    aria-current={isActive ? 'page' : undefined}
                    aria-disabled={link.disabled || undefined}
                    tabIndex={!mobileOpen || link.disabled ? -1 : undefined}
                    onClick={(event) => handleNavLinkClick(event, link, true)}
                    className={linkClassName}
                    style={transitionStyle}
                  >
                    {t(link.title)}
                  </a>
                )
              }
              return (
                <Link
                  key={getNavLinkKey(link)}
                  to={link.href}
                  disabled={link.disabled}
                  aria-current={isActive ? 'page' : undefined}
                  aria-disabled={link.disabled || undefined}
                  onClick={(event) => handleNavLinkClick(event, link, true)}
                  className={linkClassName}
                  style={transitionStyle}
                  tabIndex={link.disabled || !mobileOpen ? -1 : undefined}
                >
                  {t(link.title)}
                </Link>
              )
            })}
          </nav>

          <div
            className={cn(
              'flex flex-col gap-3 transition-all duration-500',
              mobileOpen
                ? 'translate-y-0 opacity-100'
                : 'translate-y-4 opacity-0'
            )}
            style={{ transitionDelay: mobileOpen ? '250ms' : '0ms' }}
          >
            {showAuthButtons && (
              <Link
                to={
                  isAuthenticated
                    ? getAuthenticatedLandingRoute(user)
                    : '/sign-in'
                }
                onClick={closeMobileMenu}
                className={cn(
                  'focus-visible:ring-ring focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none touch-manipulation inline-flex min-h-11 h-10 items-center justify-center rounded-lg text-sm font-medium transition-opacity hover:opacity-90 active:opacity-80',
                  editorialHeader
                    ? 'forge-public-mobile-action'
                    : 'bg-foreground text-background'
                )}
                tabIndex={mobileOpen ? undefined : -1}
              >
                {isAuthenticated ? t('Open workspace') : t('Sign in')}
              </Link>
            )}
          </div>
        </div>
      </div>

      <Dialog
        open={!!authPromptTarget}
        onOpenChange={(open) => {
          if (!open) {
            closeAuthPrompt()
          }
        }}
        title={t('Sign in required')}
        description={t('Please sign in to view {{module}}.', {
          module: authPromptTarget?.title || '',
        })}
        contentClassName='sm:max-w-md'
        contentHeight='auto'
        footer={[
          <Button key='cancel' variant='outline' onClick={closeAuthPrompt}>
            {t('Cancel')}
          </Button>,
          <Button key='sign-in' onClick={navigateToSignIn}>
            {t('Sign in now')}
          </Button>,
        ]}
      >
        <div className='bg-muted/40 text-muted-foreground rounded-lg px-3 py-2 text-sm'>
          {t('Redirecting to sign in in {{seconds}} seconds.', {
            seconds: authPromptSecondsLeft,
          })}
        </div>
      </Dialog>
    </>
  )
}
