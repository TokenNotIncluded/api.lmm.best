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
  ChevronDown,
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

import {
  CodeBlock,
  CodeBlockCopyButton,
} from '@/components/ai-elements/code-block'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { SettingsSection } from '@/features/system-settings/components/settings-section'
import { api } from '@/lib/api'

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
function platformsFor(name: string): Platform[] {
  const extension = name.toLowerCase().split('.').pop()
  return extension === 'ps1' || extension === 'cmd' || extension === 'bat'
    ? ['windows']
    : ['linux', 'macos']
}
function commandFor(name: string, platform: Platform) {
  const url = scriptUrl(name)
  if (platform === 'windows') {
    if (name.toLowerCase().endsWith('.ps1')) {
      return `Invoke-WebRequest -Uri "${url}" -OutFile "${name}"; powershell -ExecutionPolicy Bypass -File ".\\${name}"`
    }
    return `Invoke-WebRequest -Uri "${url}" -OutFile "${name}"; .\\${name}`
  }
  const shell = name.toLowerCase().endsWith('.zsh') ? 'zsh' : 'bash'
  return `curl -fsSL "${url}" | ${shell}`
}
async function readScript(name: string, publicOnly = false) {
  const response = await api.get<ScriptResponse | string>(
    `/api/scripts/${encodeURIComponent(name)}${publicOnly ? '/raw' : ''}`,
    { skipBusinessError: true }
  )
  if (publicOnly && typeof response.data === 'string') {
    return response.data
  }
  if (
    !publicOnly &&
    typeof response.data !== 'string' &&
    response.data.success &&
    response.data.data &&
    !Array.isArray(response.data.data)
  ) {
    return response.data.data.content
  }
  throw new Error(
    typeof response.data === 'string'
      ? 'Unable to load script'
      : response.data.message || 'Unable to load script'
  )
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

export function PublicScriptsPanel({
  fullPage = false,
}: {
  fullPage?: boolean
}) {
  const { t } = useTranslation()
  const scripts = useScriptList()
  const [openName, setOpenName] = useState<string | null>(null)
  const [content, setContent] = useState('')
  const [loadingName, setLoadingName] = useState<string | null>(null)
  const [copied, setCopied] = useState<string | null>(null)
  const toggleScript = async (name: string) => {
    if (openName === name) {
      setOpenName(null)
      return
    }
    setLoadingName(name)
    try {
      setContent(await readScript(name, true))
      setOpenName(name)
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Unable to load script')
      )
    } finally {
      setLoadingName(null)
    }
  }
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
        <div className='mb-5 flex items-end justify-between gap-3'>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Browse maintained setup scripts and copy the command for your system.'
            )}
          </p>
          <a
            href='/scripts'
            className='text-primary inline-flex items-center gap-1 text-sm hover:underline'
          >
            {t('Open public script page')}{' '}
            <ExternalLink className='size-3.5' aria-hidden='true' />
          </a>
        </div>
      )}
      {scripts.data.map((script) => {
        const platforms = platformsFor(script.name)
        return (
          <div key={script.name} className='border-border/70 border'>
            <button
              type='button'
              className='hover:bg-muted/50 flex min-h-12 w-full items-center justify-between gap-3 px-3 py-2 text-left text-sm transition-colors'
              onClick={() => void toggleScript(script.name)}
              aria-expanded={openName === script.name}
            >
              <span className='flex min-w-0 items-center gap-2'>
                <FileCode2
                  className='text-muted-foreground size-4 shrink-0'
                  aria-hidden='true'
                />
                <span className='truncate font-medium'>{script.name}</span>
                <span className='text-muted-foreground hidden text-xs sm:inline'>
                  {formatUpdated(script.updated, t('Unknown update time'))}
                </span>
              </span>
              <span className='text-muted-foreground flex shrink-0 items-center gap-2 text-xs'>
                {script.fetches ?? 0} {t('fetches')}
                <ChevronDown
                  className={`size-4 transition-transform ${openName === script.name ? 'rotate-180' : ''}`}
                  aria-hidden='true'
                />
              </span>
            </button>
            {openName === script.name && (
              <div className='space-y-3 border-t p-3'>
                <div className='max-h-80 overflow-auto'>
                  <CodeBlock
                    code={
                      loadingName === script.name ? t('Loading...') : content
                    }
                    language={
                      script.name.endsWith('.ps1') ? 'powershell' : 'bash'
                    }
                  >
                    <CodeBlockCopyButton />
                  </CodeBlock>
                </div>
                <div className='grid gap-2 sm:grid-cols-2'>
                  {platforms.map((platform) => {
                    const id = `${script.name}:${platform}`
                    return (
                      <Button
                        key={platform}
                        type='button'
                        size='sm'
                        variant='outline'
                        onClick={() => void copyCommand(script.name, platform)}
                      >
                        {copied === id ? (
                          <Check className='me-2 size-4' />
                        ) : (
                          <Copy className='me-2 size-4' />
                        )}
                        {platform === 'linux'
                          ? 'Linux'
                          : platform === 'macos'
                            ? 'macOS'
                            : 'Windows'}
                      </Button>
                    )
                  })}
                </div>
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

export function PublicScriptsPage() {
  const { t } = useTranslation()
  return (
    <main className='min-h-screen px-4 py-10 sm:px-8 sm:py-16'>
      <div className='mx-auto w-full max-w-4xl'>
        <div className='mb-8 flex items-start justify-between gap-4'>
          <div>
            <p className='text-muted-foreground text-xs font-medium tracking-[0.16em] uppercase'>
              LMM Forge
            </p>
            <h1 className='mt-2 text-3xl font-semibold tracking-tight'>
              {t('Public scripts')}
            </h1>
          </div>
          <a
            href='/'
            className='text-muted-foreground hover:text-foreground text-sm'
          >
            {t('Back to home')}
          </a>
        </div>
        <PublicScriptsPanel fullPage />
      </div>
    </main>
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
