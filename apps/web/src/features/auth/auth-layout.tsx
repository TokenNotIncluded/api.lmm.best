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
import { Link } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { LanguageSwitcher } from '@/components/language-switcher'
import { LmmBrandMark } from '@/components/lmm-brand-mark'
import { ThemeSwitch } from '@/components/theme-switch'
import { useSystemConfig } from '@/hooks/use-system-config'

import { AuthArtPanel } from './components/auth-art-panel'

import '../forge/forge-public-shell.css'

type AuthLayoutProps = {
  children: React.ReactNode
}

export function AuthLayout({ children }: AuthLayoutProps) {
  const { systemName } = useSystemConfig()
  const { t } = useTranslation()
  const [mobileGameOpen, setMobileGameOpen] = useState(false)
  useEffect(() => {
    const previousTitle = document.title
    document.title = systemName
    return () => {
      document.title = previousTitle
    }
  }, [systemName])

  return (
    <div className='auth-editorial relative flex h-dvh max-w-none flex-col overflow-hidden lg:grid lg:grid-cols-[minmax(31rem,0.92fr)_minmax(31rem,1.08fr)]'>
      <header className='relative z-20 flex min-h-16 shrink-0 items-center justify-between px-5 sm:min-h-20 sm:px-8 lg:absolute lg:inset-x-0 lg:top-0 lg:min-h-24'>
        <Link
          to='/'
          className='flex items-center gap-2 transition-opacity hover:opacity-80'
        >
          <LmmBrandMark className='size-8' title={systemName} />
          <h1 className='text-lg font-medium sm:text-xl'>{systemName}</h1>
        </Link>
        <div className='flex items-center gap-1'>
          <LanguageSwitcher />
          <ThemeSwitch />
        </div>
      </header>
      <div className='grid min-h-0 flex-1 lg:col-start-1 lg:h-full'>
        <div className='no-scrollbar container min-h-0 overflow-y-auto lg:pt-24'>
          <div className='mx-auto flex min-h-full w-full max-w-md flex-col justify-center px-5 py-7 sm:px-8 sm:py-12'>
            {children}
            <details
              className='mt-8 w-full lg:hidden'
              onToggle={(event) => setMobileGameOpen(event.currentTarget.open)}
            >
              <summary className='text-muted-foreground cursor-pointer py-3 text-center text-sm underline underline-offset-4'>
                {t('Play a round')}
              </summary>
              {mobileGameOpen && <AuthArtPanel />}
            </details>
          </div>
        </div>
      </div>
      <div className='hidden lg:sticky lg:top-0 lg:col-start-2 lg:row-start-1 lg:block lg:h-svh lg:min-h-0 lg:overflow-y-auto lg:p-3 lg:pl-0'>
        <AuthArtPanel />
      </div>
    </div>
  )
}
