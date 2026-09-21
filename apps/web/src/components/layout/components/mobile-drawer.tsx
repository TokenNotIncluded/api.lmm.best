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
import { Link, useRouterState } from '@tanstack/react-router'
import { X, User, Wallet, LogOut } from 'lucide-react'
import { AnimatePresence, motion, type Variants } from 'motion/react'
import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'

import { SignOutDialog } from '@/components/sign-out-dialog'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { UserAvatar } from '@/components/user-avatar'
import useDialogState from '@/hooks/use-dialog'
import { useUserDisplay } from '@/hooks/use-user-display'
import { isConsoleActivated } from '@/lib/console-activation'
import type { AuthUser } from '@/stores/auth-store'

import { MOBILE_DRAWER_ANIMATION, MOBILE_DRAWER_CONFIG } from '../constants'
import type { TopNavLink } from '../types'
import { getNavLinkKey } from './nav-link-key'

const MOBILE_NAV_SKELETON_KEYS = ['first', 'second', 'third', 'fourth'] as const

/**
 * Brand logo component with skeleton loading
 */
interface BrandLogoProps {
  homeUrl: string
  displayLogo: React.ReactNode
  displaySiteName: string
  loading: boolean
  logoLoaded: boolean
  onClick?: () => void
}

function BrandLogo({
  homeUrl,
  displayLogo,
  displaySiteName,
  loading,
  logoLoaded,
  onClick,
}: BrandLogoProps) {
  return (
    <Link
      to={homeUrl}
      className='focus-visible:ring-ring flex touch-manipulation items-center gap-2 text-xl font-bold focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none'
      onClick={onClick}
    >
      <div className='relative h-6 w-6'>
        {loading || !logoLoaded ? (
          <Skeleton className='absolute inset-0 rounded-full' />
        ) : null}
        {displayLogo}
      </div>
      {loading ? <Skeleton className='h-5 w-20' /> : displaySiteName}
    </Link>
  )
}

/**
 * Mobile user profile section with navigation links
 */
interface MobileUserProfileProps {
  user: AuthUser | null
  onNavigate?: () => void
}

function MobileUserProfile({ user, onNavigate }: MobileUserProfileProps) {
  const { t } = useTranslation()
  const pathname = useRouterState().location.pathname
  const [signOutOpen, setSignOutOpen] = useDialogState()
  const { displayName, roleLabel } = useUserDisplay(user)
  const consoleActivated = isConsoleActivated(user)

  if (!user) return null

  return (
    <div className='flex flex-col text-sm'>
      {/* User info section - compact style matching navigation */}
      {/* User header - simplified */}
      <div className='border-border flex items-center gap-2.5 border-b p-2.5'>
        <UserAvatar
          name={user.username || displayName}
          email={user.email}
          alt={displayName}
          className='size-9'
          fallbackClassName='text-xs'
          gravatarSize={72}
        />
        <div className='flex flex-1 flex-col gap-0.5 overflow-hidden'>
          <p className='text-foreground truncate font-medium'>{displayName}</p>
          <div className='flex items-center gap-1.5'>
            <span className='text-muted-foreground text-xs'>{roleLabel}</span>
            {user.group ? (
              <span className='text-muted-foreground inline-flex items-center gap-1.5 text-xs'>
                <span aria-hidden='true'>·</span>
                <span>{String(user.group)}</span>
              </span>
            ) : null}
          </div>
        </div>
      </div>

      {/* Navigation links - same style as top nav */}
      {consoleActivated
        ? [
            <Link
              key='profile'
              to='/profile'
              onClick={onNavigate}
              aria-current={pathname === '/profile' ? 'page' : undefined}
              className='text-primary/60 hover:text-primary/80 focus-visible:ring-ring border-border flex min-h-11 touch-manipulation items-center gap-2.5 border-b p-2.5 transition-colors focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none'
            >
              <User className='size-4' aria-hidden='true' />
              {t('Profile')}
            </Link>,
            <Link
              key='wallet'
              to='/wallet'
              onClick={onNavigate}
              aria-current={pathname === '/wallet' ? 'page' : undefined}
              className='text-primary/60 hover:text-primary/80 focus-visible:ring-ring border-border flex min-h-11 touch-manipulation items-center gap-2.5 border-b p-2.5 transition-colors focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none'
            >
              <Wallet className='size-4' aria-hidden='true' />
              {t('Wallet')}
            </Link>,
          ]
        : null}

      {/* Sign out - consistent style */}
      <Button
        variant='ghost'
        onClick={() => setSignOutOpen(true)}
        className='text-destructive hover:text-destructive/80 focus-visible:ring-ring h-auto min-h-11 w-full touch-manipulation justify-start gap-2.5 p-2.5 hover:bg-transparent focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none'
      >
        <LogOut className='size-4' aria-hidden='true' />
        {t('Sign out')}
      </Button>

      <SignOutDialog open={!!signOutOpen} onOpenChange={setSignOutOpen} />
    </div>
  )
}

/**
 * Mobile sign in button for unauthenticated users
 */
interface MobileSignInButtonProps {
  onNavigate?: () => void
}

function MobileSignInButton({ onNavigate }: MobileSignInButtonProps) {
  const { t } = useTranslation()
  return (
    <Button
      variant='secondary'
      size='sm'
      className='focus-visible:ring-ring h-10 min-h-11 w-full touch-manipulation focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none'
      render={<Link to='/sign-in' onClick={onNavigate} />}
    >
      {t('Sign in')}
    </Button>
  )
}

/**
 * Mobile drawer component props
 */
export interface MobileDrawerProps {
  isOpen: boolean
  onClose: () => void
  homeUrl: string
  displayLogo: React.ReactNode
  displaySiteName: string
  loading: boolean
  logoLoaded: boolean
  mobileLinksList: TopNavLink[]
  showAuthButtons: boolean
  user: AuthUser | null
}

/**
 * Mobile drawer component with bottom slide-up animation
 * Displays navigation links and user profile section
 */
export function MobileDrawer({
  isOpen,
  onClose,
  homeUrl,
  displayLogo,
  displaySiteName,
  loading,
  logoLoaded,
  mobileLinksList,
  showAuthButtons,
  user,
}: MobileDrawerProps) {
  const { t } = useTranslation()
  const pathname = useRouterState().location.pathname
  const drawerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!isOpen) return

    const drawer = drawerRef.current
    const focusable = drawer
      ? [
          ...drawer.querySelectorAll<HTMLElement>(
            'a[href], button:not([disabled]), [tabindex]:not([tabindex="-1"])'
          ),
        ]
      : []
    focusable[0]?.focus()

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        onClose()
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
  }, [isOpen, onClose])

  return (
    <AnimatePresence>
      {isOpen && (
        <>
          {/* Overlay */}
          <motion.div
            className={MOBILE_DRAWER_CONFIG.overlayClassName}
            initial='hidden'
            animate='visible'
            exit='exit'
            variants={MOBILE_DRAWER_ANIMATION.overlay as Variants}
            transition={{
              duration: MOBILE_DRAWER_CONFIG.overlayTransitionDuration,
            }}
            onClick={onClose}
            aria-hidden='true'
          />

          {/* Drawer Content */}
          <motion.div
            className={MOBILE_DRAWER_CONFIG.drawerClassName}
            initial='hidden'
            animate='visible'
            exit='exit'
            variants={MOBILE_DRAWER_ANIMATION.drawer as Variants}
            ref={drawerRef}
            role='dialog'
            aria-modal='true'
            aria-label={t('Header navigation')}
            tabIndex={-1}
          >
            <div className='flex flex-col gap-4'>
              {/* Header with logo and close button */}
              <div className='flex items-center justify-between'>
                <BrandLogo
                  homeUrl={homeUrl}
                  displayLogo={displayLogo}
                  displaySiteName={displaySiteName}
                  loading={loading}
                  logoLoaded={logoLoaded}
                  onClick={onClose}
                />
                <Button
                  variant='ghost'
                  size='icon-sm'
                  onClick={onClose}
                  className='hover:text-primary focus-visible:ring-ring cursor-pointer touch-manipulation focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none'
                  aria-label={t('Close menu')}
                >
                  <X className='size-5' aria-hidden='true' />
                </Button>
              </div>

              {/* Navigation links */}
              <motion.nav
                className='border-border mb-4 flex flex-col rounded-md border text-sm'
                variants={{ hidden: { opacity: 0 }, visible: { opacity: 1 } }}
                aria-label={t('Header navigation')}
              >
                {loading ? (
                  <div className='flex flex-col gap-1 p-2'>
                    {MOBILE_NAV_SKELETON_KEYS.map((skeletonKey) => (
                      <Skeleton
                        key={`mobile-nav-skeleton:${skeletonKey}`}
                        className='h-8 w-full'
                      />
                    ))}
                  </div>
                ) : (
                  <AnimatePresence>
                    {mobileLinksList.map((link) => {
                      const isActive = link.isActive ?? pathname === link.href
                      const linkClassName =
                        'text-primary/60 hover:text-primary/80 focus-visible:ring-ring focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none touch-manipulation flex min-h-11 items-center transition-colors'
                      const disabledClassName = link.disabled
                        ? 'pointer-events-none opacity-50'
                        : ''
                      const handleLinkClick = (
                        event: React.MouseEvent<HTMLAnchorElement>
                      ) => {
                        if (link.disabled) {
                          event.preventDefault()
                          return
                        }
                        onClose()
                      }

                      return (
                        <motion.div
                          key={getNavLinkKey(link)}
                          className='border-border border-b p-2.5 last:border-b-0'
                          variants={
                            MOBILE_DRAWER_ANIMATION.menuItem as Variants
                          }
                        >
                          {link.external ? (
                            <a
                              href={link.href}
                              target='_blank'
                              rel='noopener noreferrer'
                              className={`${linkClassName} ${disabledClassName}`}
                              aria-current={isActive ? 'page' : undefined}
                              aria-disabled={link.disabled || undefined}
                              tabIndex={link.disabled ? -1 : undefined}
                              onClick={handleLinkClick}
                            >
                              {link.title}
                            </a>
                          ) : (
                            <Link
                              to={link.href}
                              disabled={link.disabled}
                              className={`${linkClassName} ${disabledClassName}`}
                              aria-current={isActive ? 'page' : undefined}
                              aria-disabled={link.disabled || undefined}
                              tabIndex={link.disabled ? -1 : undefined}
                              onClick={handleLinkClick}
                            >
                              {link.title}
                            </Link>
                          )}
                        </motion.div>
                      )
                    })}
                  </AnimatePresence>
                )}
              </motion.nav>

              {/* User profile section */}
              {showAuthButtons &&
                (user ? (
                  <MobileUserProfile user={user} onNavigate={onClose} />
                ) : (
                  <MobileSignInButton onNavigate={onClose} />
                ))}
            </div>
          </motion.div>
        </>
      )}
    </AnimatePresence>
  )
}
