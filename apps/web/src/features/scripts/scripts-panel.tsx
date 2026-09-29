/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Check,
  Copy,
  ExternalLink,
  FileCode2,
  GitBranch,
  KeyRound,
  RefreshCw,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout/components/section-page-layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { REPOSITORIES, repositoryUrl } from '@/features/repositories/api'
import { RepositoryLink } from '@/features/repositories/repository-link'
import { SettingsSection } from '@/features/system-settings/components/settings-section'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'

type Platform = 'linux' | 'macos' | 'windows'
type ScriptMeta = {
  name: string
  size?: number
  updated?: string
  fetches?: number
}
type ScriptResponse = {
  success: boolean
  message?: string
  data?: ScriptMeta[] | { name: string; content: string }
}
type RepositoryConfig = {
  repository_url: string
  branch: string
  github_key_set: boolean
  last_pulled_at?: string
}
type RepositoryResponse = {
  success: boolean
  message?: string
  data?: RepositoryConfig
}

const scriptBaseUrl = () =>
  typeof window === 'undefined'
    ? 'https://api.lmm.best'
    : window.location.origin
function scriptUrl(name: string) {
  return `${scriptBaseUrl()}/scripts/${encodeURIComponent(name)}`
}
function commandFor(name: string, platform: Platform) {
  const url = scriptUrl(name)
  if (platform === 'windows') {
    if (name.toLowerCase().endsWith('.ps1')) {
      return `irm "${url}" | iex`
    }
    return `Invoke-WebRequest -Uri "${url}" -OutFile "${name}"; .\\${name}`
  }
  const shell = name.toLowerCase().endsWith('.zsh') ? 'zsh' : 'bash'
  return `curl -fsSL "${url}" | ${shell}`
}
/** Best-effort guess at the visitor's platform, so the right tab is
 * pre-selected instead of making them find it. */
function detectPlatform(): Platform {
  if (typeof navigator === 'undefined') return 'linux'
  const ua = navigator.userAgent
  if (/Windows/i.test(ua)) return 'windows'
  if (/Mac OS X|Macintosh/i.test(ua)) return 'macos'
  return 'linux'
}

function useScriptList() {
  return useQuery({
    queryKey: ['public-scripts'],
    queryFn: async () => {
      const response = await api.get<ScriptResponse>('/api/scripts', {
        skipBusinessError: true,
      })
      if (!response.data.success || !Array.isArray(response.data.data)) {
        throw new Error(response.data.message || 'Unable to load scripts')
      }
      return response.data.data
    },
    staleTime: 30_000,
  })
}
function formatUpdated(value: string | undefined, fallback: string) {
  if (!value) return fallback
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? fallback : date.toLocaleString()
}

function PublicScriptSource({ script }: { script: ScriptMeta }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const source = useQuery({
    queryKey: ['public-script-source', script.name, script.updated],
    enabled: open,
    staleTime: 30_000,
    queryFn: async () => {
      const response = await api.get<string>(
        `/api/scripts/${encodeURIComponent(script.name)}/raw`,
        { skipBusinessError: true, responseType: 'text' }
      )
      if (typeof response.data !== 'string') {
        throw new Error('Unable to load script')
      }
      return response.data
    },
  })
  return (
    <details
      className='border-border/70 border-t'
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary className='hover:bg-muted/50 cursor-pointer px-4 py-3 text-sm font-medium'>
        {script.name}
      </summary>
      {open && (
        <div className='space-y-3 px-4 pb-4'>
          <a
            href={scriptUrl(script.name)}
            download={script.name}
            className='text-primary text-sm underline underline-offset-4'
          >
            {t('Download')}
          </a>
          {source.isError ? (
            <p role='alert' className='text-destructive text-sm'>
              {t('Unable to load script')}
            </p>
          ) : (
            <pre
              className='bg-muted max-h-96 overflow-auto rounded-lg p-3 text-xs'
              tabIndex={0}
              aria-label={script.name}
            >
              <code>{source.isPending ? t('Loading...') : source.data}</code>
            </pre>
          )}
        </div>
      )}
    </details>
  )
}

export function PublicScriptsPanel({
  fullPage = false,
}: {
  fullPage?: boolean
}) {
  const { t } = useTranslation()
  const scripts = useScriptList()
  const [copied, setCopied] = useState<string | null>(null)
  const [picked, setPicked] = useState<Platform | null>(null)
  const detected = useMemo(detectPlatform, [])
  const platform: Platform = picked ?? detected
  const menus = [
    platform === 'windows' ? 'menu.ps1' : 'menu.sh',
    ...(scripts.data ?? [])
      .map((script) => script.name)
      .filter((name) => name !== 'menu.sh' && name !== 'menu.ps1'),
  ].flatMap(
    (name) =>
      scripts.data?.filter((script) => script.name === name).slice(0, 1) ?? []
  )
  const copyCommand = async (name: string, platform: Platform) => {
    try {
      await navigator.clipboard.writeText(commandFor(name, platform))
      const id = `${name}:${platform}`
      setCopied(id)
      window.setTimeout(
        () => setCopied((value) => (value === id ? null : value)),
        1600
      )
      toast.success(t('Command copied'))
    } catch {
      toast.error(t('Unable to copy command'))
    }
  }
  if (scripts.isError) {
    if (!fullPage) return null
    return (
      <div
        role='alert'
        className='border-destructive/40 text-destructive border border-dashed p-8 text-sm'
      >
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <span>{t('Unable to load scripts')}</span>
          <Button
            type='button'
            size='sm'
            variant='outline'
            onClick={() => void scripts.refetch()}
          >
            {t('Retry')}
          </Button>
        </div>
      </div>
    )
  }
  if (!scripts.data?.length) {
    if (!fullPage) return null
    return (
      <div className='border-border/70 text-muted-foreground border border-dashed p-8 text-sm'>
        {scripts.isLoading ? t('Loading...') : t('No scripts published yet')}
      </div>
    )
  }
  return (
    <div className={fullPage ? 'space-y-3' : 'space-y-2'}>
      {fullPage && (
        <p className='text-muted-foreground mb-4 text-sm'>
          {t('Pick your system, then copy one command.')}
        </p>
      )}
      <div
        className='flex flex-wrap items-center gap-1.5'
        role='group'
        aria-label={t('Your system')}
      >
        {(
          [
            ['linux', t('Linux')],
            ['macos', t('macOS')],
            ['windows', t('Windows')],
          ] as const
        ).map(([value, label]) => (
          <button
            key={value}
            type='button'
            aria-pressed={platform === value}
            onClick={() => setPicked(value)}
            className={cn(
              'min-h-11 rounded-lg border px-3.5 text-sm font-medium transition-colors',
              platform === value
                ? 'border-primary/50 bg-primary/10 text-foreground'
                : 'border-border/70 text-muted-foreground hover:bg-muted/50 hover:text-foreground'
            )}
          >
            {label}
            {value === detected && picked === null && (
              <span className='text-muted-foreground/70 ml-1.5 text-[10px] font-normal'>
                {t('detected')}
              </span>
            )}
          </button>
        ))}
      </div>

      {menus.map((script, index) => {
        const id = `${script.name}:${platform}`
        return (
          <section
            key={script.name}
            className='border-border/70 space-y-3 rounded-xl border p-4 sm:p-5'
          >
            <div className='flex items-center justify-between gap-3'>
              <h2 className='font-medium'>
                {index === 0
                  ? t('Start here')
                  : platform === 'windows'
                    ? 'Windows · PowerShell'
                    : 'Linux / macOS · Bash'}
              </h2>
              <Button
                type='button'
                variant={index === 0 ? 'default' : 'outline'}
                onClick={() => void copyCommand(script.name, platform)}
              >
                {copied === id ? (
                  <Check className='me-2 size-4' />
                ) : (
                  <Copy className='me-2 size-4' />
                )}
                {copied === id ? t('Copied') : t('Copy command')}
              </Button>
            </div>
            <pre
              className='bg-muted overflow-x-auto rounded-lg p-3 text-sm break-all whitespace-pre-wrap'
              tabIndex={0}
            >
              <code>{commandFor(script.name, platform)}</code>
            </pre>
            <a
              className='text-muted-foreground text-xs underline underline-offset-4'
              href={scriptUrl(script.name)}
            >
              {script.name}
            </a>
          </section>
        )
      })}
      <details className='border-border/70 rounded-xl border'>
        <summary className='hover:bg-muted/50 cursor-pointer p-4 text-sm font-medium'>
          {t('Scripts')} ({scripts.data?.length ?? 0})
        </summary>
        {scripts.data?.map((script) => (
          <PublicScriptSource key={script.name} script={script} />
        ))}
      </details>
    </div>
  )
}

export function PublicScriptsPage() {
  const { t } = useTranslation()
  return (
    <main className='min-h-screen px-5 pt-12 pb-20 md:px-10 md:pt-16'>
      <div className='mx-auto w-full max-w-5xl'>
        <div className='mb-8'>
          <h1 className='text-3xl font-semibold tracking-tight'>
            {t('Public scripts')}
          </h1>
          <p className='text-muted-foreground mt-2 max-w-2xl text-sm'>
            {t(
              'Browse maintained setup scripts and copy the command for your system.'
            )}
          </p>
        </div>
        <div className='mb-8 flex flex-wrap items-center justify-between gap-4 border-y py-5'>
          <div>
            <p className='text-sm font-medium'>{t('Script repository')}</p>
            <a
              className='text-muted-foreground text-sm break-all underline underline-offset-4'
              href={repositoryUrl('scripts')}
              target='_blank'
              rel='noopener noreferrer'
            >
              {REPOSITORIES.scripts}
            </a>
          </div>
          <RepositoryLink kind='scripts' />
        </div>
        <PublicScriptsPanel fullPage />
      </div>
    </main>
  )
}

export function ConsoleScriptsPage() {
  const { t } = useTranslation()

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Scripts')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <RepositoryLink kind='scripts' />
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='mx-auto w-full max-w-5xl space-y-6'>
          <p className='text-muted-foreground max-w-2xl text-sm'>
            {t(
              'Browse maintained setup scripts and copy the command for your system.'
            )}
          </p>
          <div className='border-border/70 border-y py-4'>
            <p className='text-sm font-medium'>{t('Script repository')}</p>
            <a
              className='text-muted-foreground mt-1 block text-sm break-all underline underline-offset-4'
              href={repositoryUrl('scripts')}
              target='_blank'
              rel='noopener noreferrer'
            >
              {REPOSITORIES.scripts}
            </a>
          </div>
          <PublicScriptsPanel fullPage />
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

export function ScriptsAdminSection() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const scripts = useScriptList()
  const repository = useQuery({
    queryKey: ['scripts-repository'],
    queryFn: async () =>
      (await api.get<RepositoryResponse>('/api/scripts/repository')).data,
  })
  const [draft, setDraft] = useState<{
    repositoryUrl: string
    branch: string
  } | null>(null)
  const [githubKey, setGithubKey] = useState('')
  const [clearGithubKey, setClearGithubKey] = useState(false)
  const [copied, setCopied] = useState(false)
  const config = repository.data?.data
  const repositoryUrl = draft?.repositoryUrl ?? config?.repository_url ?? ''
  const branch = draft?.branch ?? config?.branch ?? 'main'
  const saveMutation = useMutation({
    mutationFn: async () =>
      (
        await api.put<RepositoryResponse>('/api/scripts/repository', {
          repository_url: repositoryUrl.trim(),
          branch: branch.trim(),
          github_key: githubKey.trim(),
          clear_github_key: clearGithubKey,
        })
      ).data,
    onSuccess: async (response) => {
      if (!response.success) {
        toast.error(response.message || t('Failed to save'))
        return
      }
      setGithubKey('')
      setClearGithubKey(false)
      await queryClient.invalidateQueries({ queryKey: ['scripts-repository'] })
      toast.success(t('Script repository settings saved'))
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t('Failed to save')),
  })
  const pullMutation = useMutation({
    mutationFn: async () =>
      (await api.post<RepositoryResponse>('/api/scripts/repository/pull')).data,
    onSuccess: async (response) => {
      if (!response.success) {
        toast.error(response.message || t('Failed to pull repository'))
        return
      }
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['public-scripts'] }),
        queryClient.invalidateQueries({ queryKey: ['scripts-repository'] }),
      ])
      toast.success(t('Scripts updated from repository'))
    },
    onError: (error) =>
      toast.error(
        error instanceof Error ? error.message : t('Failed to pull repository')
      ),
  })
  const sortedScripts = useMemo(
    () =>
      [...(scripts.data ?? [])].sort((a, b) => a.name.localeCompare(b.name)),
    [scripts.data]
  )
  const configured = config
  const copyPublicLink = async () => {
    await navigator.clipboard.writeText(`${window.location.origin}/scripts`)
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1600)
    toast.success(t('Copied to clipboard'))
  }
  return (
    <div className='space-y-6'>
      <div className='space-y-4'>
        <div>
          <h3 className='flex items-center gap-2 text-sm font-semibold'>
            <GitBranch className='size-4' aria-hidden='true' />
            {t('Script repository')}
          </h3>
          <p className='text-muted-foreground mt-1 text-xs leading-5'>
            {t(
              'Scripts are read from this repository and exposed on the public scripts page.'
            )}
          </p>
        </div>
        <div className='grid gap-3 sm:grid-cols-[minmax(0,1fr)_10rem]'>
          <div className='space-y-1.5'>
            <Label htmlFor='scripts-repository-url'>
              {t('Repository URL')}
            </Label>
            <Input
              id='scripts-repository-url'
              value={repositoryUrl}
              onChange={(event) =>
                setDraft({ repositoryUrl: event.target.value, branch })
              }
              placeholder='https://github.com/owner/repository.git'
            />
          </div>
          <div className='space-y-1.5'>
            <Label htmlFor='scripts-repository-branch'>{t('Branch')}</Label>
            <Input
              id='scripts-repository-branch'
              value={branch}
              onChange={(event) =>
                setDraft({ repositoryUrl, branch: event.target.value })
              }
              placeholder='main'
            />
          </div>
        </div>
        <div className='space-y-1.5'>
          <Label htmlFor='scripts-github-key'>
            <span className='inline-flex items-center gap-1.5'>
              <KeyRound className='size-3.5' aria-hidden='true' />
              {t('GitHub key (optional)')}
            </span>
          </Label>
          <Input
            id='scripts-github-key'
            type='password'
            value={githubKey}
            onChange={(event) => setGithubKey(event.target.value)}
            placeholder={
              configured?.github_key_set
                ? t('Key is configured; leave blank to keep it')
                : t('Only needed for a private repository')
            }
            autoComplete='new-password'
          />
        </div>
        {configured?.github_key_set && (
          <label className='text-muted-foreground flex items-center gap-2 text-xs'>
            <input
              type='checkbox'
              checked={clearGithubKey}
              onChange={(event) => setClearGithubKey(event.target.checked)}
            />
            {t('Remove the stored GitHub key')}
          </label>
        )}
        <div className='flex flex-wrap gap-2'>
          <Button
            type='button'
            onClick={() => saveMutation.mutate()}
            disabled={saveMutation.isPending}
          >
            <GitBranch className='me-2 size-4' />
            {saveMutation.isPending ? t('Saving...') : t('Save repository')}
          </Button>
          <Button
            type='button'
            variant='outline'
            onClick={() => pullMutation.mutate()}
            disabled={!repositoryUrl.trim() || pullMutation.isPending}
          >
            <RefreshCw
              className={`me-2 size-4 ${pullMutation.isPending ? 'animate-spin' : ''}`}
            />
            {pullMutation.isPending ? t('Pulling...') : t('Pull updates')}
          </Button>
          <Button
            type='button'
            variant='ghost'
            onClick={() => void copyPublicLink()}
          >
            {copied ? (
              <Check className='me-2 size-4' />
            ) : (
              <ExternalLink className='me-2 size-4' />
            )}
            {t('Copy public page link')}
          </Button>
        </div>
        {configured?.last_pulled_at && (
          <p className='text-muted-foreground text-xs'>
            {t('Last pulled')}:{' '}
            {formatUpdated(configured.last_pulled_at, t('Unknown update time'))}
          </p>
        )}
      </div>
      <div className='border-t pt-4'>
        <div className='mb-2 flex items-center justify-between gap-3'>
          <h3 className='text-sm font-semibold'>{t('Published scripts')}</h3>
          <span className='text-muted-foreground text-xs'>
            {sortedScripts.length} {t('scripts')}
          </span>
        </div>
        {sortedScripts.length === 0 ? (
          <p className='text-muted-foreground text-sm'>
            {t('Pull a repository to publish scripts.')}
          </p>
        ) : (
          <div className='divide-border divide-y border'>
            {sortedScripts.map((script) => (
              <div
                key={script.name}
                className='flex items-center justify-between gap-3 px-3 py-2.5 text-sm'
              >
                <span className='flex min-w-0 items-center gap-2'>
                  <FileCode2
                    className='text-muted-foreground size-4'
                    aria-hidden='true'
                  />
                  <span className='truncate'>{script.name}</span>
                </span>
                <span className='text-muted-foreground shrink-0 text-xs'>
                  {script.fetches ?? 0} {t('fetches')} ·{' '}
                  {formatUpdated(script.updated, t('Unknown update time'))}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

export function ScriptsSettingsSection() {
  const { t } = useTranslation()
  return (
    <SettingsSection title={t('Scripts')}>
      <ScriptsAdminSection />
    </SettingsSection>
  )
}
