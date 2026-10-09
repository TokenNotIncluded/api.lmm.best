/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Loader2, Pencil, RotateCcw } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

import {
  expandOverviewGreeting,
  GREETING_LANGUAGES,
  GREETING_VARIABLES,
  overviewLanguage,
  parseOverviewPreference,
  validGreetingTemplate,
  type GreetingLanguage,
  type OverviewGreetingPreference,
} from './overview-personalization-data'

import './overview-personalization.css'

const languageNames: Record<GreetingLanguage, string> = {
  en: 'English',
  zh: '简体中文',
  'zh-TW': '繁體中文',
  fr: 'Français',
  ja: '日本語',
  ru: 'Русский',
  vi: 'Tiếng Việt',
}

export function OverviewGreeting() {
  const user = useAuthStore((state) => state.auth.user)
  return user ? <OverviewGreetingSession key={user.id} user={user} /> : null
}
function OverviewGreetingSession({ user }: { user: AuthUser }) {
  const { t, i18n } = useTranslation()
  const client = useQueryClient()
  const wallet = useWalletCurrency()
  const site = useSystemConfigStore((state) => state.config.systemName)
  const [clock, setClock] = useState(() => new Date())
  const [editor, setEditor] = useState<{
    language: GreetingLanguage
    template: string
    revision: number
  } | null>(null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState(false)
  useEffect(() => {
    const timer = setInterval(() => setClock(new Date()), 30_000)
    return () => clearInterval(timer)
  }, [])
  const preference = useQuery({
    queryKey: ['overview-greeting', user.id],
    queryFn: async ({ signal }) => {
      const { data } = await api.get<{ success: boolean; data: unknown }>(
        '/api/assistant/workspace/greeting',
        { signal }
      )
      if (!data.success) throw new Error('Greeting unavailable')
      return parseOverviewPreference(data.data)
    },
    retry: false,
    staleTime: 30_000,
  })
  const language = overviewLanguage(i18n.resolvedLanguage || i18n.language)
  const level =
    user.role >= ROLE.SUPER_ADMIN
      ? `L6 (${t('Super administrator')})`
      : user.role >= ROLE.ADMIN
        ? `L5 (${t('Administrator')})`
        : user.trust_level_info?.level !== undefined
          ? `L${user.trust_level_info.level}`
          : '—'
  const valuesFor = (locale: string) => ({
    $name: user.display_name || user.username,
    $time: new Intl.DateTimeFormat(locale, {
      hour: '2-digit',
      minute: '2-digit',
    }).format(clock),
    $date: new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(
      clock
    ),
    $weekday: new Intl.DateTimeFormat(locale, { weekday: 'long' }).format(
      clock
    ),
    $level: level,
    $site: site,
    $balance:
      typeof user.quota === 'number' ? wallet.formatQuota(user.quota) : '—',
  })
  const defaultTemplate = (locale: string) =>
    t('HI,$name, it is $time', { lng: locale })
  const template =
    preference.data?.templates[language] || defaultTemplate(language)
  const greeting = expandOverviewGreeting(template, valuesFor(language))
  const startEditing = () => {
    if (!preference.data) return
    setError(false)
    setEditor({
      language,
      template: preference.data.templates[language] ?? '',
      revision: preference.data.revision,
    })
  }

  async function save() {
    if (
      !editor ||
      saving ||
      !validGreetingTemplate(editor.template) ||
      useAuthStore.getState().auth.user?.id !== user.id
    ) {
      return
    }
    setSaving(true)
    setError(false)
    try {
      const { data } = await api.put<{ success: boolean; data: unknown }>(
        '/api/assistant/workspace/greeting',
        editor
      )
      if (!data.success) throw new Error('Greeting not saved')
      const saved: OverviewGreetingPreference = parseOverviewPreference(
        data.data
      )
      if (useAuthStore.getState().auth.user?.id !== user.id) return
      client.setQueryData(['overview-greeting', user.id], saved)
      setEditor(null)
    } catch {
      setError(true)
      void preference.refetch()
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <section
        className='overview-greeting'
        aria-label={t('Overview greeting')}
      >
        <div className='overview-greeting-meta'>
          <span>
            {valuesFor(language).$date} · {valuesFor(language).$weekday}
          </span>
          <Button
            type='button'
            size='icon'
            variant='ghost'
            className='size-11'
            disabled={!preference.data || preference.isError}
            onClick={startEditing}
            aria-label={t('Edit overview greeting')}
          >
            <Pencil className='size-4' />
          </Button>
        </div>
        <div className='overview-greeting-quote'>
          <span aria-hidden='true' className='overview-greeting-mark'>
            “
          </span>
          <h2 className='overview-greeting-message'>{greeting}</h2>
          <span
            aria-hidden='true'
            className='overview-greeting-mark overview-greeting-mark-end'
          >
            ”
          </span>
        </div>
        <div className='overview-greeting-footer'>
          <span>{site}</span>
          <span>{level}</span>
        </div>
        {preference.isError && (
          <p role='alert' className='text-muted-foreground mt-3 text-xs'>
            {t(
              'Saved greeting could not be loaded. The default greeting is shown.'
            )}{' '}
            <Button
              type='button'
              size='sm'
              variant='link'
              onClick={() => void preference.refetch()}
            >
              {t('Retry')}
            </Button>
          </p>
        )}
      </section>
      <Dialog
        open={Boolean(editor)}
        onOpenChange={(open) => {
          if (!open && !saving) setEditor(null)
        }}
      >
        <DialogContent className='sm:max-w-2xl'>
          <DialogHeader>
            <DialogTitle>{t('Edit overview greeting')}</DialogTitle>
            <DialogDescription>
              {t(
                'Each language has its own template. An empty template restores that language’s default. You can also ask the assistant to edit it.'
              )}
            </DialogDescription>
          </DialogHeader>
          {editor && (
            <div className='space-y-4'>
              <label className='block space-y-2 text-sm'>
                <span>{t('Language')}</span>
                <select
                  className='border-input bg-background h-10 w-full rounded-lg border px-3'
                  disabled={saving}
                  value={editor.language}
                  onChange={(event) => {
                    const selected = event.target.value as GreetingLanguage
                    setEditor({
                      ...editor,
                      language: selected,
                      template: preference.data?.templates[selected] ?? '',
                    })
                  }}
                >
                  {GREETING_LANGUAGES.map((locale) => (
                    <option key={locale} value={locale}>
                      {languageNames[locale]}
                    </option>
                  ))}
                </select>
              </label>
              <label className='block space-y-2 text-sm'>
                <span>{t('Greeting template')}</span>
                <textarea
                  className='border-input bg-background min-h-28 w-full resize-y rounded-xl border p-3'
                  disabled={saving}
                  value={editor.template}
                  placeholder={defaultTemplate(editor.language)}
                  onChange={(event) =>
                    setEditor({ ...editor, template: event.target.value })
                  }
                />
              </label>
              <div className='flex flex-wrap gap-2'>
                {GREETING_VARIABLES.map((variable) => (
                  <Button
                    type='button'
                    key={variable}
                    variant='outline'
                    size='sm'
                    disabled={saving}
                    onClick={() =>
                      setEditor({
                        ...editor,
                        template: editor.template + variable,
                      })
                    }
                  >
                    <code>{variable}</code>
                  </Button>
                ))}
              </div>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Supported variables: name, local time, date, weekday, level, site and balance. Use $$ for a literal dollar sign. Maximum 512 characters.'
                )}
              </p>
              <blockquote className='bg-muted/40 rounded-xl p-5 text-xl leading-relaxed break-words whitespace-pre-wrap'>
                “
                {expandOverviewGreeting(
                  editor.template || defaultTemplate(editor.language),
                  valuesFor(editor.language)
                )}
                ”
              </blockquote>
              {!validGreetingTemplate(editor.template) && (
                <p role='alert' className='text-destructive text-sm'>
                  {t(
                    'Use only the listed variables and at most 512 characters.'
                  )}
                </p>
              )}
              {error && (
                <div
                  role='alert'
                  className='text-destructive space-y-2 text-sm'
                >
                  <p>
                    {t(
                      'The greeting was not saved. It may have changed elsewhere. Reload the saved version before editing again.'
                    )}
                  </p>
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    disabled={!preference.data || preference.isFetching}
                    onClick={() => {
                      setEditor({
                        language: editor.language,
                        template:
                          preference.data?.templates[editor.language] ?? '',
                        revision: preference.data?.revision ?? editor.revision,
                      })
                      setError(false)
                    }}
                  >
                    {t('Reload saved version')}
                  </Button>
                </div>
              )}
              <div className='flex flex-wrap justify-between gap-3 border-t pt-4'>
                <Button
                  type='button'
                  variant='ghost'
                  disabled={saving}
                  onClick={() => setEditor({ ...editor, template: '' })}
                >
                  <RotateCcw className='size-4' />
                  {t('Use the default greeting')}
                </Button>
                <Button
                  type='button'
                  disabled={
                    saving || error || !validGreetingTemplate(editor.template)
                  }
                  onClick={() => void save()}
                >
                  {saving && <Loader2 className='size-4 animate-spin' />}
                  {t('Save')}
                </Button>
              </div>
            </div>
          )}
        </DialogContent>
      </Dialog>
    </>
  )
}
