/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { ArrowDown, ArrowUp, Plus, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  parseRSSFeeds,
  RSS_MAX_FEEDS,
  safeRSSFeedUrl,
  type RSSFeedConfig,
} from '@/features/rss/api'
import { registerRSSTranslations } from '@/features/rss/i18n'

import { FormNavigationGuard } from '../components/form-navigation-guard'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

registerRSSTranslations()

function validateFeeds(feeds: RSSFeedConfig[]): Record<string, string> {
  const errors: Record<string, string> = {}
  const seenURLs = new Set<string>()
  for (const feed of feeds) {
    if (!feed.name.trim() || feed.name.trim().length > 80) {
      errors[feed.id] = 'Enter a feed name (up to 80 characters).'
      continue
    }
    const normalizedURL = safeRSSFeedUrl(feed.url)
    if (!normalizedURL || feed.url.length > 2048) {
      errors[feed.id] = 'Enter a valid HTTP or HTTPS feed URL.'
      continue
    }
    if (seenURLs.has(normalizedURL)) {
      errors[feed.id] = 'This feed URL is already configured.'
      continue
    }
    seenURLs.add(normalizedURL)
  }
  return errors
}

export function RSSFeedsSection({ initialValue }: { initialValue: string }) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const initialFeeds = useMemo(
    () => parseRSSFeeds(initialValue),
    [initialValue]
  )
  const [feeds, setFeeds] = useState<RSSFeedConfig[]>(initialFeeds ?? [])
  const [activeID, setActiveID] = useState<string | null>(null)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const dirty = JSON.stringify(feeds) !== JSON.stringify(initialFeeds ?? [])

  useEffect(() => {
    setFeeds(initialFeeds ?? [])
    setErrors({})
  }, [initialFeeds])

  const updateFeed = (id: string, patch: Partial<RSSFeedConfig>) => {
    setFeeds((current) =>
      current.map((feed) => (feed.id === id ? { ...feed, ...patch } : feed))
    )
    setErrors((current) => {
      if (!current[id]) return current
      const next = { ...current }
      delete next[id]
      return next
    })
  }

  const moveFeed = (index: number, direction: -1 | 1) => {
    setFeeds((current) => {
      const next = [...current]
      const target = index + direction
      if (target < 0 || target >= next.length) return current
      ;[next[index], next[target]] = [next[target], next[index]]
      return next
    })
  }

  const addFeed = () => {
    if (feeds.length >= RSS_MAX_FEEDS) return
    const id = crypto.randomUUID()
    setFeeds((current) => [
      ...current,
      { id, name: '', url: 'https://', enabled: true },
    ])
    setActiveID(id)
  }

  const save = async () => {
    const next = feeds.map((feed) => ({
      ...feed,
      name: feed.name.trim(),
      url: feed.url.trim(),
    }))
    const nextErrors = validateFeeds(next)
    setErrors(nextErrors)
    if (Object.keys(nextErrors).length) {
      setActiveID(Object.keys(nextErrors)[0])
      toast.error(t('Check the highlighted feed.'))
      return
    }
    await updateOption.mutateAsync({
      key: 'RSSFeeds',
      value: JSON.stringify(next),
    })
  }

  return (
    <SettingsSection title='RSS'>
      <FormNavigationGuard when={dirty} />
      <SettingsPageFormActions
        onSave={() => void save()}
        onReset={() => {
          setFeeds(initialFeeds ?? [])
          setErrors({})
          setActiveID(null)
        }}
        isSaving={updateOption.isPending}
        isSaveDisabled={!dirty}
        isResetDisabled={!dirty}
        saveLabel='Save feeds'
      />

      <div className='flex flex-wrap items-start justify-between gap-4'>
        <p className='text-muted-foreground max-w-2xl text-sm leading-relaxed'>
          {t(
            'Configure the RSS and Atom sources shown in the reader. Changes are shared with all users.'
          )}
        </p>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={addFeed}
          disabled={feeds.length >= RSS_MAX_FEEDS}
        >
          <Plus data-icon='inline-start' />
          {t('Add feed')}
        </Button>
      </div>

      {initialFeeds === null && (
        <p className='bg-destructive/10 rounded-xl p-4 text-sm'>
          {t('Check the highlighted feed.')}
        </p>
      )}

      {feeds.length === 0 && initialFeeds !== null && (
        <p className='text-muted-foreground rounded-xl border border-dashed p-8 text-center text-sm'>
          {t('No feeds are configured. Add one to start the reader.')}
        </p>
      )}

      {feeds.length > 0 && (
        <div className='divide-border overflow-hidden rounded-xl border'>
          {feeds.map((feed, index) => (
            <div key={feed.id} className='border-border border-b last:border-b-0'>
              <div className='flex min-w-0 items-center gap-2 px-4 py-3'>
                <button
                  type='button'
                  className='focus-visible:ring-ring min-w-0 flex-1 rounded text-left focus-visible:ring-2'
                  aria-expanded={activeID === feed.id}
                  onClick={() =>
                    setActiveID(activeID === feed.id ? null : feed.id)
                  }
                >
                  <span className='block truncate text-sm font-semibold'>
                    {feed.name || t('New feed')}
                  </span>
                  <span className='text-muted-foreground block truncate text-xs'>
                    {feed.url}
                  </span>
                </button>
                <Switch
                  checked={feed.enabled}
                  onCheckedChange={(enabled) =>
                    updateFeed(feed.id, { enabled })
                  }
                  aria-label={t('Fetch this feed')}
                />
                <Button
                  type='button'
                  variant='ghost'
                  size='icon-sm'
                  disabled={index === 0}
                  onClick={() => moveFeed(index, -1)}
                  aria-label={t('Move {{name}} up', {
                    name: feed.name || t('New feed'),
                  })}
                >
                  <ArrowUp />
                </Button>
                <Button
                  type='button'
                  variant='ghost'
                  size='icon-sm'
                  disabled={index === feeds.length - 1}
                  onClick={() => moveFeed(index, 1)}
                  aria-label={t('Move {{name}} down', {
                    name: feed.name || t('New feed'),
                  })}
                >
                  <ArrowDown />
                </Button>
                <Button
                  type='button'
                  variant='ghost'
                  size='icon-sm'
                  onClick={() => {
                    setFeeds((current) =>
                      current.filter((item) => item.id !== feed.id)
                    )
                    if (activeID === feed.id) setActiveID(null)
                  }}
                  aria-label={t('Remove {{name}}', {
                    name: feed.name || t('New feed'),
                  })}
                >
                  <Trash2 />
                </Button>
              </div>

              {activeID === feed.id && (
                <div className='bg-muted/25 grid gap-4 border-t p-4 lg:grid-cols-2'>
                  <div className='grid gap-1.5'>
                    <Label htmlFor={`rss-name-${feed.id}`}>
                      {t('Feed name')}
                    </Label>
                    <Input
                      id={`rss-name-${feed.id}`}
                      value={feed.name}
                      maxLength={80}
                      onChange={(event) =>
                        updateFeed(feed.id, { name: event.target.value })
                      }
                    />
                  </div>
                  <div className='grid gap-1.5'>
                    <Label htmlFor={`rss-url-${feed.id}`}>
                      {t('Feed URL')}
                    </Label>
                    <Input
                      id={`rss-url-${feed.id}`}
                      type='url'
                      value={feed.url}
                      onChange={(event) =>
                        updateFeed(feed.id, { url: event.target.value })
                      }
                      placeholder='https://example.com/feed.xml'
                    />
                  </div>
                  <p className='text-muted-foreground text-xs lg:col-span-2'>
                    {t('Disabled feeds stay saved but are not fetched.')}
                  </p>
                  {errors[feed.id] && (
                    <p
                      className='text-destructive text-sm lg:col-span-2'
                      role='alert'
                    >
                      {t(errors[feed.id])}
                    </p>
                  )}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </SettingsSection>
  )
}
