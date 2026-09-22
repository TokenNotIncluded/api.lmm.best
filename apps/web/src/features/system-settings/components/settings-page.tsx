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
/*
Copyright (C) 2026 LIghtJUNction
*/
import { useParams } from '@tanstack/react-router'
import { Suspense, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

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
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { PageFooterPortal } from '@/components/layout/components/page-footer'

import './settings-workspace.css'
import { LoadingState } from '@/components/loading-state'

import { useSystemOptions, getOptionValue } from '../hooks/use-system-options'
import type { SystemOption } from '../types'
import { SettingsPageProvider } from './settings-page-context'
import { SettingsBreadcrumb, SettingsSearch } from './settings-search'

type SettingsPageProps<
  TSettings extends Record<string, string | number | boolean | unknown[]>,
  TSectionId extends string,
  TExtraArgs extends unknown[] = [],
> = {
  routePath: string
  defaultSettings: TSettings
  defaultSection: TSectionId
  getSectionContent: (
    sectionId: TSectionId,
    settings: TSettings,
    ...extraArgs: TExtraArgs
  ) => ReactNode
  getSectionMeta: (sectionId: TSectionId) => {
    titleKey: string
  }
  extraArgs?: TExtraArgs
  loadingMessage?: string
  resolveSettings?: (
    settings: TSettings,
    raw: SystemOption[] | undefined
  ) => TSettings
}

type SettingsPageFrameProps = {
  title: ReactNode
  children: ReactNode
}

function SettingsPageFrame(props: SettingsPageFrameProps) {
  const [formActionsContainer, setFormActionsContainer] =
    useState<HTMLDivElement | null>(null)
  const [actionsContainer, setActionsContainer] =
    useState<HTMLDivElement | null>(null)
  const [titleStatusContainer, setTitleStatusContainer] =
    useState<HTMLSpanElement | null>(null)

  return (
    <SettingsPageProvider
      actionsContainer={actionsContainer}
      formActionsContainer={formActionsContainer}
      titleStatusContainer={titleStatusContainer}
    >
      <SectionPageLayout className='settings-workspace'>
        <SectionPageLayout.Breadcrumb>
          <SettingsBreadcrumb />
        </SectionPageLayout.Breadcrumb>
        <SectionPageLayout.Title>
          <span className='inline-flex max-w-full min-w-0 flex-wrap items-center gap-3 align-middle'>
            <span className='truncate'>{props.title}</span>
            <span
              ref={setTitleStatusContainer}
              className='inline-flex min-w-0 shrink-0 items-center'
            />
          </span>
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <SettingsSearch />
          <div
            ref={setActionsContainer}
            className='flex flex-wrap items-center justify-end gap-2'
          />
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='settings-sheet'>{props.children}</div>
          <PageFooterPortal>
            <div className='settings-save-dock'>
              <span className='settings-save-context text-muted-foreground text-sm'>
                {props.title}
              </span>
              <div
                ref={setFormActionsContainer}
                className='settings-save-target flex flex-wrap items-center justify-end gap-2'
              />
            </div>
          </PageFooterPortal>
        </SectionPageLayout.Content>
      </SectionPageLayout>
    </SettingsPageProvider>
  )
}

/**
 * Generic settings page component
 * Handles loading state, data fetching, and section rendering
 */
export function SettingsPage<
  TSettings extends Record<string, string | number | boolean | unknown[]>,
  TSectionId extends string,
  TExtraArgs extends unknown[] = [],
>({
  routePath,
  defaultSettings,
  defaultSection,
  getSectionContent,
  getSectionMeta,
  extraArgs,
  loadingMessage = 'Loading settings...',
  resolveSettings,
}: SettingsPageProps<TSettings, TSectionId, TExtraArgs>) {
  const { t } = useTranslation()
  const { data, isLoading, isError, refetch } = useSystemOptions()
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const params = useParams({ from: routePath as any })
  const activeSection = (params?.section ?? defaultSection) as TSectionId
  const sectionMeta = getSectionMeta(activeSection)

  const settings = useMemo(() => {
    const baseSettings = getOptionValue(
      data?.data,
      defaultSettings
    ) as TSettings
    return resolveSettings
      ? resolveSettings(baseSettings, data?.data)
      : baseSettings
  }, [data?.data, defaultSettings, resolveSettings])

  if (isLoading) {
    return (
      <SettingsPageFrame title={t(sectionMeta.titleKey)}>
        <LoadingState message={t(loadingMessage)} />
      </SettingsPageFrame>
    )
  }

  if (isError) {
    return (
      <SettingsPageFrame title={t(sectionMeta.titleKey)}>
        <ErrorState
          title={t('Unable to load settings')}
          description={t('Your settings have not been changed. Try again.')}
          onRetry={() => {
            void refetch()
          }}
        />
      </SettingsPageFrame>
    )
  }

  const sectionContent = getSectionContent(
    activeSection,
    settings,
    ...((extraArgs ?? []) as TExtraArgs)
  )

  return (
    <SettingsPageFrame title={t(sectionMeta.titleKey)}>
      <Suspense fallback={<LoadingState message={t(loadingMessage)} />}>
        {sectionContent}
      </Suspense>
    </SettingsPageFrame>
  )
}
