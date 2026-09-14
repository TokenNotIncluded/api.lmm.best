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
import { ExternalLinkIcon, RefreshCcwIcon, SaveIcon } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Markdown } from '@/components/ui/markdown'
import { Switch } from '@/components/ui/switch'
import { api } from '@/lib/api'
import { getBuildVersion } from '@/lib/build-metadata'
import { openExternalUrl } from '@/lib/external-navigation'
import { formatTimestamp, formatTimestampToDate } from '@/lib/format'
import { validatedExternalUrl } from '@/lib/validated-external-url'

import { SettingsSection } from '../components/settings-section'

type ReleaseInfo = {
  tag_name: string
  name?: string
  body?: string
  html_url?: string
  published_at?: string
}

type UpdateSource = {
  type?: string
  url?: string
  enabled?: boolean
}

type UpdateTarget = {
  current?: string
  latest?: string
  updated?: string
  sources?: UpdateSource[]
}

type UpdateSourcesResponse = {
  backend?: UpdateTarget
  frontend?: UpdateTarget
}

type UpdateSourceConfig = {
  github: string
  aur: string
  githubEnabled: boolean
  aurEnabled: boolean
}

const defaultSourceConfig = (
  target: 'backend' | 'frontend'
): UpdateSourceConfig => ({
  github:
    target === 'backend'
      ? 'https://github.com/TokenNotIncluded/api.lmm.best/releases'
      : 'https://github.com/TokenNotIncluded/api.lmm.best/releases',
  aur:
    target === 'backend'
      ? 'https://aur.archlinux.org/packages/lmm-api-go-bin'
      : 'https://aur.archlinux.org/packages/lmm-api-web-bin',
  githubEnabled: true,
  aurEnabled: true,
})

function buildSourceConfig(
  target: UpdateTarget | undefined,
  name: 'backend' | 'frontend'
) {
  const config = defaultSourceConfig(name)
  for (const source of target?.sources ?? []) {
    const type = source.type?.toLowerCase()
    if (type === 'github' || type === 'github_release') {
      config.github = source.url || config.github
      config.githubEnabled = source.enabled !== false
    } else if (type === 'aur') {
      config.aur = source.url || config.aur
      config.aurEnabled = source.enabled !== false
    }
  }
  return config
}

function repositoryFromUrl(value: string) {
  try {
    const url = new URL(value)
    const parts = url.pathname.split('/').filter(Boolean)
    return parts.length >= 2 ? `${parts[0]}/${parts[1]}` : ''
  } catch {
    return ''
  }
}

function packageFromUrl(value: string) {
  try {
    const url = new URL(value)
    const parts = url.pathname.split('/').filter(Boolean)
    return parts.at(-1) || ''
  } catch {
    return ''
  }
}

function toSources(config: UpdateSourceConfig, target: 'backend' | 'frontend') {
  return [
    {
      type: 'github_release',
      repository: repositoryFromUrl(config.github.trim()),
      component: target,
      tag_prefix: target === 'backend' ? 'go-v' : 'web-v',
      url: config.github.trim(),
      enabled: config.githubEnabled,
    },
    {
      type: 'aur',
      package: packageFromUrl(config.aur.trim()),
      component: target,
      url: config.aur.trim(),
      enabled: config.aurEnabled,
    },
  ].filter((source) => source.repository || source.package)
}

type UpdateCheckerSectionProps = {
  currentVersion?: string | null
  startTime?: number | null
}

export function UpdateCheckerSection({
  currentVersion,
  startTime,
}: UpdateCheckerSectionProps) {
  const { t } = useTranslation()
  const [checking, setChecking] = useState(false)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [release, setRelease] = useState<ReleaseInfo | null>(null)
  const [targets, setTargets] = useState<UpdateSourcesResponse | null>(null)
  const [sourceConfig, setSourceConfig] = useState({
    backend: defaultSourceConfig('backend'),
    frontend: defaultSourceConfig('frontend'),
  })
  const [savingSources, setSavingSources] = useState(false)

  const uptime = startTime ? formatTimestamp(startTime) : t('Unknown')
  const version = currentVersion || t('Unknown')

  const handleCheckUpdates = async () => {
    setChecking(true)
    try {
      const response = await api.get('/api/option/updates', {
        skipBusinessError: true,
        params: { frontend_version: getBuildVersion() },
      })
      if (!response.data?.success) {
        throw new Error(t('Failed to check for updates'))
      }

      const data = response.data.data as UpdateSourcesResponse
      setTargets(data)
      setSourceConfig({
        backend: buildSourceConfig(data.backend, 'backend'),
        frontend: buildSourceConfig(data.frontend, 'frontend'),
      })

      const dataRelease = data.backend
      if (!dataRelease?.latest) {
        toast.success(t('No version updates have been published yet.'))
        return
      }

      if (!data.backend?.updated && !data.frontend?.updated) {
        toast.success(
          t('You are running the latest version ({{version}}).', {
            version: dataRelease.latest,
          })
        )
        return
      }

      setRelease({
        tag_name: dataRelease.latest,
        body: `Backend: ${data.backend?.current || t('Unknown')} -> ${data.backend?.latest || t('Unknown')}\n\nFrontend: ${data.frontend?.current || t('Unknown')} -> ${data.frontend?.latest || t('Unknown')}`,
        published_at: dataRelease.updated,
      })
      setDialogOpen(true)
    } catch (error) {
      const message =
        error instanceof Error
          ? error.message
          : t('Failed to check for updates')
      toast.error(message)
    } finally {
      setChecking(false)
    }
  }

  const saveSources = async () => {
    setSavingSources(true)
    try {
      const response = await api.put('/api/option/', {
        key: 'UpdateSources',
        value: JSON.stringify({
          backend: toSources(sourceConfig.backend, 'backend'),
          frontend: toSources(sourceConfig.frontend, 'frontend'),
        }),
      })
      if (!response.data?.success) {
        throw new Error(response.data?.message || t('Failed to save'))
      }
      toast.success(t('Update succeeded'))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Failed to save'))
    } finally {
      setSavingSources(false)
    }
  }

  const updateSourceConfig = (
    target: 'backend' | 'frontend',
    patch: Partial<UpdateSourceConfig>
  ) => {
    setSourceConfig((current) => ({
      ...current,
      [target]: { ...current[target], ...patch },
    }))
  }

  const targetCard = (target: 'backend' | 'frontend') => {
    const item = targets?.[target]
    const config = sourceConfig[target]
    return (
      <div className='space-y-3 rounded-none border p-4' key={target}>
        <div className='flex items-center justify-between gap-3'>
          <div className='font-medium'>
            {target === 'backend' ? 'Backend' : 'Frontend'}
          </div>
          <span className='text-muted-foreground text-xs'>
            {item?.updated ? t('Update available') : t('Up to date')}
          </span>
        </div>
        <div className='grid gap-2 text-sm sm:grid-cols-2'>
          <div>
            <div className='text-muted-foreground'>{t('Current version')}</div>
            <div className='font-medium'>{item?.current || t('Unknown')}</div>
          </div>
          <div>
            <div className='text-muted-foreground'>{t('Latest version')}</div>
            <div className='font-medium'>{item?.latest || t('Unknown')}</div>
          </div>
        </div>
        <div className='space-y-3'>
          <label className='flex items-center gap-2 text-sm'>
            <Switch
              checked={config.githubEnabled}
              onCheckedChange={(enabled) =>
                updateSourceConfig(target, { githubEnabled: enabled })
              }
            />
            GitHub release
          </label>
          <Input
            value={config.github}
            onChange={(event) =>
              updateSourceConfig(target, { github: event.target.value })
            }
            placeholder='https://github.com/owner/repository/releases'
            aria-label='GitHub release URL'
          />
          <label className='flex items-center gap-2 text-sm'>
            <Switch
              checked={config.aurEnabled}
              onCheckedChange={(enabled) =>
                updateSourceConfig(target, { aurEnabled: enabled })
              }
            />
            AUR
          </label>
          <Input
            value={config.aur}
            onChange={(event) =>
              updateSourceConfig(target, { aur: event.target.value })
            }
            placeholder='https://aur.archlinux.org/packages/package-name'
            aria-label='AUR URL'
          />
        </div>
      </div>
    )
  }

  const goToRelease = async () => {
    if (!release?.html_url) return

    const releaseUrl = validatedExternalUrl(release.html_url, {
      protocols: ['https:'],
      origins: ['https://github.com'],
      hosts: ['github.com'],
      paths: { prefixes: ['/TokenNotIncluded/api.lmm.best/commit/'] },
    })
    if (releaseUrl) {
      // Invariant: releaseUrl is HTTPS on github.com under this repository's commit path.
      // pi-lens-ignore: ts-open-redirect, no-open-redirect
      const opened = await openExternalUrl(releaseUrl)
      if (!opened) toast.error(t('Unable to open link'))
    }
  }

  return (
    <>
      <SettingsSection title={t('System maintenance')}>
        <div className='space-y-6'>
          <div className='grid gap-4 md:grid-cols-2'>
            <div className='rounded-none border p-4'>
              <div className='text-muted-foreground text-sm'>
                {t('Current version')}
              </div>
              <div className='text-lg font-semibold'>{version}</div>
            </div>
            <div className='rounded-none border p-4'>
              <div className='text-muted-foreground text-sm'>
                {t('Uptime since')}
              </div>
              <div className='text-lg font-semibold'>{uptime}</div>
            </div>
          </div>

          <Button onClick={handleCheckUpdates} disabled={checking}>
            {checking ? (
              t('Checking updates...')
            ) : (
              <>
                <RefreshCcwIcon className='me-2 h-4 w-4' />
                {t('Check for updates')}
              </>
            )}
          </Button>
          <div className='grid gap-4 md:grid-cols-2'>
            {targetCard('backend')}
            {targetCard('frontend')}
          </div>
          <Button
            variant='outline'
            onClick={saveSources}
            disabled={savingSources}
          >
            <SaveIcon className='me-2 h-4 w-4' />
            {t('Save')}
          </Button>
        </div>
      </SettingsSection>

      <Dialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        title={
          release?.tag_name
            ? t('New version available: {{version}}', {
                version: release.tag_name,
              })
            : t('Release details')
        }
        description={
          release?.published_at
            ? `${t('Published')} ${formatTimestampToDate(
                new Date(release.published_at).getTime(),
                'milliseconds'
              )}`
            : undefined
        }
        contentClassName='max-h-[80vh] overflow-y-auto'
        contentHeight='auto'
        bodyClassName='space-y-4'
        footer={
          <>
            <Button
              type='button'
              variant='secondary'
              onClick={() => setDialogOpen(false)}
            >
              {t('Close')}
            </Button>
            {release?.html_url && (
              <Button type='button' onClick={goToRelease}>
                <ExternalLinkIcon className='me-2 h-4 w-4' />
                {t('Open release')}
              </Button>
            )}
          </>
        }
      >
        <div className='space-y-4'>
          {release?.body ? (
            <Markdown>{release.body}</Markdown>
          ) : (
            <p className='text-muted-foreground text-sm'>
              {t('No release notes provided.')}
            </p>
          )}
        </div>
      </Dialog>
    </>
  )
}
