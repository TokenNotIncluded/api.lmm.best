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
import { Copy } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

import {
  SettingsPageActionsPortal,
  useSuppressSettingsSectionHeader,
} from './settings-page-context'

type SettingsSectionProps = {
  title: string
  titleProps?: React.HTMLAttributes<HTMLHeadingElement>
  children: React.ReactNode
  className?: string
}

export function SettingsSection({
  title,
  titleProps,
  children,
  className,
}: SettingsSectionProps) {
  const suppressHeader = useSuppressSettingsSectionHeader()
  const { t } = useTranslation()

  const copyConfigurationPrompt = async () => {
    const prompt = JSON.stringify(
      {
        source: 'lmm.best.settings',
        action: 'help_configure',
        page: typeof window === 'undefined' ? '' : window.location.pathname,
        section: title,
        instructions: [
          'Help configure this settings section.',
          'First explain the relevant options and ask concise questions about the desired outcome.',
          'Propose a safe configuration before any change and keep existing security safeguards enabled.',
          'Do not invent server capabilities or credentials.',
        ],
      },
      null,
      2
    )

    try {
      await navigator.clipboard.writeText(prompt)
      toast.success(t('Copied to clipboard'))
    } catch {
      toast.error(t('Failed to copy'))
    }
  }

  const configureButton = (
    <Button
      type='button'
      size='icon'
      variant='ghost'
      onClick={copyConfigurationPrompt}
      title={t('Copy configuration prompt to AI')}
      aria-label={t('Copy configuration prompt to AI')}
    >
      <Copy data-icon='inline-start' />
      <span className='sr-only'>{t('Help me configure')}</span>
    </Button>
  )

  return (
    <section className={cn('settings-section flex flex-col gap-4', className)}>
      {!suppressHeader && (
        <div className='flex flex-wrap items-center justify-between gap-2'>
          <h3
            {...titleProps}
            className={cn('text-base font-semibold', titleProps?.className)}
          >
            {title}
          </h3>
          {configureButton}
        </div>
      )}
      {suppressHeader && (
        <SettingsPageActionsPortal>{configureButton}</SettingsPageActionsPortal>
      )}
      {children}
    </section>
  )
}
