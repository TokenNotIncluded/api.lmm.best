/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'
import {
  ChevronDown,
  Copy,
  Download,
  FileCode2,
  Plus,
  Save,
  Trash2,
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
import { Textarea } from '@/components/ui/textarea'
import { SettingsSection } from '@/features/system-settings/components/settings-section'
import { api } from '@/lib/api'

type ScriptMeta = {
  name: string
  size?: number
  updated?: string
}

type ScriptResponse = {
  success: boolean
  message?: string
  data?: ScriptMeta[] | { name: string; content: string }
}

const scriptBaseUrl = () =>
  typeof window === 'undefined'
    ? 'https://api.lmm.best'
    : window.location.origin

function scriptUrl(name: string) {
  return `${scriptBaseUrl()}/scripts/${encodeURIComponent(name)}`
}

function commandFor(name: string, platform: 'linux' | 'macos' | 'windows') {
  const url = scriptUrl(name)
  if (platform === 'windows') {
    return `Invoke-WebRequest -Uri "${url}" -OutFile "${name}"; .\\${name}`
  }
  return `curl -fsSL "${url}" -o "${name}" && chmod +x "${name}" && ./${name}`
}

async function readScript(name: string, publicOnly = false) {
  const response = await api.get<ScriptResponse | string>(
    `/api/scripts/${encodeURIComponent(name)}${publicOnly ? '/raw' : ''}`,
    { skipBusinessError: true }
  )
  if (publicOnly) {
    if (typeof response.data === 'string') {
      return response.data
    }
    throw new Error('Unable to load script')
  }
  if (typeof response.data === 'string') {
    throw new Error('Unable to load script')
  }
  if (
    !response.data.success ||
    !response.data.data ||
    Array.isArray(response.data.data)
  ) {
    throw new Error(response.data.message || 'Unable to load script')
  }
  return response.data.data.content
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
    staleTime: 60_000,
  })
}

export function PublicScriptsPanel() {
  const { t } = useTranslation()
  const scripts = useScriptList()
  const [openName, setOpenName] = useState<string | null>(null)
  const [content, setContent] = useState('')
  const [loadingName, setLoadingName] = useState<string | null>(null)

  const toggleScript = async (name: string) => {
    if (openName === name) {
      setOpenName(null)
      return
    }
    setLoadingName(name)
    try {
      const script = await readScript(name, true)
      setContent(script)
      setOpenName(name)
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Unable to load script')
      )
    } finally {
      setLoadingName(null)
    }
  }

  if (scripts.isError || !scripts.data?.length) return null

  return (
    <div className='space-y-2'>
      {scripts.data.map((script) => (
        <div key={script.name} className='border'>
          <button
            type='button'
            className='flex w-full items-center justify-between gap-3 px-3 py-2 text-left text-sm'
            onClick={() => void toggleScript(script.name)}
            aria-expanded={openName === script.name}
          >
            <span className='flex min-w-0 items-center gap-2'>
              <FileCode2 className='size-4 shrink-0' aria-hidden='true' />
              <span className='truncate font-medium'>{script.name}</span>
            </span>
            <ChevronDown
              className={`size-4 shrink-0 transition-transform ${openName === script.name ? 'rotate-180' : ''}`}
              aria-hidden='true'
            />
          </button>
          {openName === script.name && (
            <div className='space-y-3 border-t p-3'>
              <div className='max-h-80 overflow-auto'>
                <CodeBlock
                  code={loadingName === script.name ? t('Loading...') : content}
                  language={
                    script.name.endsWith('.ps1') ? 'powershell' : 'bash'
                  }
                >
                  <CodeBlockCopyButton />
                </CodeBlock>
              </div>
              <div className='grid gap-2 sm:grid-cols-3'>
                {(['linux', 'macos', 'windows'] as const).map((platform) => (
                  <Button
                    key={platform}
                    type='button'
                    size='sm'
                    variant='outline'
                    onClick={() =>
                      void navigator.clipboard.writeText(
                        commandFor(script.name, platform)
                      )
                    }
                  >
                    <Copy className='me-2 size-4' />
                    {platform === 'linux'
                      ? 'Linux'
                      : platform === 'macos'
                        ? 'macOS'
                        : 'Windows'}
                  </Button>
                ))}
              </div>
            </div>
          )}
        </div>
      ))}
    </div>
  )
}

export function ScriptsAdminSection() {
  const { t } = useTranslation()
  const scripts = useScriptList()
  const [selected, setSelected] = useState<string | null>(null)
  const [name, setName] = useState('')
  const [content, setContent] = useState('')
  const [saving, setSaving] = useState(false)

  const names = useMemo(() => scripts.data ?? [], [scripts.data])

  const selectScript = async (script: ScriptMeta) => {
    setSelected(script.name)
    setName(script.name)
    try {
      const raw = await readScript(script.name)
      setContent(raw)
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Unable to load script')
      )
    }
  }

  const reset = () => {
    setSelected(null)
    setName('')
    setContent('')
  }

  const save = async () => {
    const scriptName = name.trim()
    if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(scriptName)) {
      toast.error(t('Name is required'))
      return
    }
    setSaving(true)
    try {
      const response = await api.put<ScriptResponse>(
        `/api/scripts/${encodeURIComponent(scriptName)}`,
        { content }
      )
      if (!response.data.success) {
        throw new Error(response.data.message || t('Failed to save'))
      }
      toast.success(t('Update succeeded'))
      await scripts.refetch()
      setSelected(scriptName)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Failed to save'))
    } finally {
      setSaving(false)
    }
  }

  const remove = async () => {
    if (!selected) return
    setSaving(true)
    try {
      const response = await api.delete<ScriptResponse>(
        `/api/scripts/${encodeURIComponent(selected)}`
      )
      if (!response.data.success) {
        throw new Error(response.data.message || t('Failed to delete'))
      }
      toast.success(t('Deleted'))
      reset()
      await scripts.refetch()
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Failed to delete')
      )
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className='grid gap-6 lg:grid-cols-[minmax(12rem,0.35fr)_minmax(0,1fr)]'>
      <div className='space-y-2'>
        <Button
          type='button'
          variant='outline'
          className='w-full justify-start'
          onClick={reset}
        >
          <Plus className='me-2 size-4' />
          {t('Add')}
        </Button>
        {names.map((script) => (
          <button
            type='button'
            key={script.name}
            className={`flex w-full items-center gap-2 border px-3 py-2 text-left text-sm ${selected === script.name ? 'bg-muted' : ''}`}
            onClick={() => void selectScript(script)}
          >
            <FileCode2 className='size-4' aria-hidden='true' />
            <span className='truncate'>{script.name}</span>
          </button>
        ))}
      </div>
      <div className='space-y-3'>
        <Input
          value={name}
          onChange={(event) => setName(event.target.value)}
          placeholder='script-name.sh'
          disabled={selected !== null}
        />
        <Textarea
          value={content}
          onChange={(event) => setContent(event.target.value)}
          className='min-h-72 font-mono text-xs'
          placeholder='#!/usr/bin/env bash'
        />
        <div className='flex flex-wrap gap-2'>
          <Button type='button' onClick={() => void save()} disabled={saving}>
            <Save className='me-2 size-4' />
            {t('Save')}
          </Button>
          {selected && (
            <Button
              type='button'
              variant='destructive'
              onClick={() => void remove()}
              disabled={saving}
            >
              <Trash2 className='me-2 size-4' />
              {t('Delete')}
            </Button>
          )}
          {selected &&
            (['linux', 'macos', 'windows'] as const).map((platform) => (
              <Button
                key={platform}
                type='button'
                variant='outline'
                onClick={() =>
                  void navigator.clipboard.writeText(
                    commandFor(selected, platform)
                  )
                }
              >
                <Download className='me-2 size-4' />
                {platform === 'linux'
                  ? 'Linux'
                  : platform === 'macos'
                    ? 'macOS'
                    : 'Windows'}
              </Button>
            ))}
        </div>
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
