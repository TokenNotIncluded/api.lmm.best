/*
Copyright (C) 2026 LIghtJUNction
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { SettingsSection } from '../components/settings-section'
import { getServiceTierPricing, saveServiceTierPricing, syncServiceTierPricing, type ServiceTierPolicy } from './service-tier-api'

const queryKey = ['service-tier-pricing'] as const
export function ServiceTierSettingsCard() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const query = useQuery({ queryKey, queryFn: getServiceTierPricing })
  // Refreshing the catalog must not discard unsaved policy edits.
  const [draft, setDraft] = useState<ServiceTierPolicy | null>(null)
  const save = useMutation({
    mutationFn: saveServiceTierPricing,
    onSuccess(data) {
      client.setQueryData(queryKey, data)
      setDraft(null)
      toast.success(t('Saved successfully'))
    },
  })
  const sync = useMutation({
    mutationFn: syncServiceTierPricing,
    onSuccess(data) {
      client.setQueryData(queryKey, data)
      toast.success(t('Official service-tier prices synchronized'))
    },
  })
  const data = query.data
  const policy = draft ?? data?.policy
  const pending = save.isPending || sync.isPending
  const mutationError = save.error ?? sync.error
  return (
    <SettingsSection title={t('Fast and Ultrafast pricing')}>
      <p className='text-sm text-muted-foreground'>{t('Sync all supported official OpenAI prices once. No per-model multiplier rules are needed.')}</p>
      {query.isPending && <p role='status'>{t('Loading...')}</p>}
      {query.isError && (
        <div role='alert' className='space-y-2 text-sm'>
          <p>{t('Service-tier pricing is unavailable. This feature requires the updated Go backend.')}</p>
          <Button variant='outline' onClick={() => void query.refetch()}>{t('Retry')}</Button>
        </div>
      )}
      {data && policy && (
        <div className='space-y-6'>
          <div className='rounded-lg border p-4 space-y-3'>
            <p className='text-sm' role='status'>
              {data.fresh ? t('Official prices are ready for {{count}} models.', { count: Object.keys(data.catalog.models ?? {}).length }) : t('Prices are missing or expired. Accelerated requests are blocked.')}
            </p>
            {data.catalog.sha256 && <p className='text-xs text-muted-foreground'>{t('Last price sync')}: {new Date(data.catalog.fetched_at).toLocaleString()}</p>}
            <p className='text-sm text-muted-foreground'>{t('Prices expire after {{hours}} hours. A failed sync keeps the previous snapshot and never enables acceleration.', { hours: data.max_age_hours })}</p>
            <Button variant='outline' disabled={pending} onClick={() => sync.mutate()}>{sync.isPending ? t('Synchronizing...') : t('Sync all official service-tier prices')}</Button>
          </div>
          <div className='flex items-center justify-between gap-4'>
            <Label htmlFor='service-tier-enabled'>{t('Enable accelerated pricing')}</Label>
            <Switch id='service-tier-enabled' checked={policy.enabled} disabled={pending || (!data.fresh && !policy.enabled)} onCheckedChange={(enabled) => setDraft({ ...policy, enabled })} />
          </div>
          <p className='text-sm text-muted-foreground'>{t('Disabled by default. Only selected groups can use acceleration. Empty group lists allow nobody.')}</p>
          {(['fast', 'ultrafast'] as const).map((tier) => {
            const groupsKey = tier === 'fast' ? 'fast_groups' : 'ultrafast_groups'
            const markupKey = tier === 'fast' ? 'fast_markup' : 'ultrafast_markup'
            return (
              <fieldset key={tier} disabled={pending} className='space-y-3 rounded-lg border p-4'>
                <legend className='px-1 text-sm font-medium'>{tier === 'fast' ? 'Fast' : 'Ultrafast'}</legend>
                <Label htmlFor={`${tier}-markup`}>{t('Sales multiplier on official tier cost')}</Label>
                <Input id={`${tier}-markup`} type='number' min={1} max={100} step={0.05} value={Number.isFinite(policy[markupKey]) ? policy[markupKey] : ''} onChange={(event) => setDraft({ ...policy, [markupKey]: event.target.valueAsNumber })} />
                <p className='text-xs text-muted-foreground'>{t('1.20 adds 20% to the official tier cost, not to the standard model price.')}</p>
                <div className='flex flex-wrap gap-x-5 gap-y-3'>
                  {Object.keys(data.groups).filter((group) => group !== 'auto').map((group) => (
                    <Label key={group} className='flex items-center gap-2'>
                      <Checkbox checked={policy[groupsKey].includes(group)} onCheckedChange={(checked) => setDraft({ ...policy, [groupsKey]: checked === true ? [...policy[groupsKey].filter((entry) => entry !== group), group] : policy[groupsKey].filter((entry) => entry !== group) })} />
                      {group}
                    </Label>
                  ))}
                </div>
              </fieldset>
            )
          })}
          <p className='text-sm text-muted-foreground'>{t('Accelerated calls use wallet funds and a full reservation. Group discounts below 1 are not applied; higher group multipliers still apply. Ordinary model prices and price locks are unchanged.')}</p>
          <p className='text-sm text-muted-foreground'>{t('Global api.openai.com text requests only. Unpriced models, hosted tools, audio, background tasks and unpriced long contexts are blocked before sending.')}</p>
          {mutationError && <p role='alert' className='text-sm text-destructive'>{mutationError.message}</p>}
          <Button disabled={pending || ![policy.fast_markup, policy.ultrafast_markup].every((value) => Number.isFinite(value) && value >= 1 && value <= 100)} onClick={() => save.mutate(policy)}>{t('Save')}</Button>
        </div>
      )}
    </SettingsSection>
  )
}
