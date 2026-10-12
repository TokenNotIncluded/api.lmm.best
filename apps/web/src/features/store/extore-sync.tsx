/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { useAuthStore } from '@/stores/auth-store'
import { storeApi } from './api'
import { StoreError, StoreLoading } from './shared'
import { extoreText } from './extore-import-protocol'

type Source = {connection_id:string;source_id:string;product_id:string;last_synced_at:number;card_summary?:{total?:number;states?:Record<string,number>}}
export type ExtoreConnections = {connections:{id:string;shop_name:string;expires_at:number}[];sources:Source[]}
function useConnections(){const id=useAuthStore((state)=>state.auth.user?.id);return useQuery({queryKey:['store','extore',id],queryFn:storeApi.extoreConnections,enabled:!!id,retry:false})}
function Sync({connectionId,sourceId,lastSyncedAt}:{connectionId:string;sourceId:string;lastSyncedAt?:number}){
 const {t,i18n}=useTranslation();const client=useQueryClient();const [busy,setBusy]=useState(false);const [error,setError]=useState<unknown>(null);const [done,setDone]=useState(false)
 const owner=useAuthStore((state)=>state.auth.user?.id)
 async function sync(){if(busy)return;setBusy(true);setError(null);setDone(false);try{await storeApi.extoreSync(connectionId,sourceId);if(useAuthStore.getState().auth.user?.id!==owner)return;setDone(true);await client.invalidateQueries({queryKey:['store']})}catch(issue){if(useAuthStore.getState().auth.user?.id===owner)setError(issue)}finally{setBusy(false)}}
 return <div className='space-y-1'><div className='flex flex-wrap items-center gap-2'><Button size='sm' variant='outline' disabled={busy} onClick={()=>void sync()}>{t(busy?'Syncing...':'Sync')}</Button>{lastSyncedAt?<span className='text-muted-foreground text-xs'>{t('Last synced')}: {new Date(lastSyncedAt*1000).toLocaleString(i18n.language)}</span>:null}{done?<span role='status' className='text-xs'>{t('Synced')}</span>:null}</div><StoreError error={error}/></div>
}
export function StoreExtoreProductSync({productId}:{productId:string}){const {t}=useTranslation();const query=useConnections();const source=query.data?.sources.find((item)=>item.product_id===productId);if(!source)return null;return <div className='space-y-1'><span className='text-muted-foreground text-xs'>{t('Imported from Extore')}</span><Sync connectionId={source.connection_id} sourceId={source.source_id} lastSyncedAt={source.last_synced_at}/></div>}
export function StoreExtoreSourceNotice({productId}:{productId:string}){const {t}=useTranslation();const query=useConnections();return query.data?.sources.some((item)=>item.product_id===productId)?<p role='note' className='text-muted-foreground text-sm'>{t('Edit imported product settings in Extore, then sync.')}</p>:null}
function Connection({connection,sources}:{connection:ExtoreConnections['connections'][number];sources:Source[]}){
 const {t,i18n}=useTranslation();const query=useQuery({queryKey:['store','extore-catalog',connection.id],queryFn:()=>storeApi.extoreConnectionCatalog(connection.id),retry:false})
 return <section className='space-y-3 rounded-lg border p-4'><h3 className='font-medium'>{connection.shop_name}</h3><StoreError error={query.error} retry={()=>void query.refetch()}/>{query.isPending?<StoreLoading/>:query.data?.products.map((product)=>{const source=sources.find((item)=>item.source_id===product.id&&item.connection_id===connection.id);return <article key={product.id} className='flex flex-wrap items-start justify-between gap-3 border-t pt-3'><div><p>{extoreText(product.product.name,i18n.language)||product.id}</p><p className='text-muted-foreground text-xs'>{product.variants.length} {t('Variants')}</p>{product.card_summary?<p className='text-muted-foreground text-xs'>{t('Extore code status')}: {product.card_summary.total ?? 0} · {Object.entries(product.card_summary.states ?? {}).filter(([,count])=>count>0).map(([status,count])=>`${t(status)}: ${count}`).join(' · ')}</p>:null}</div><Sync connectionId={connection.id} sourceId={product.id} lastSyncedAt={source?.last_synced_at}/></article>})}</section>
}
export function StoreExtoreConnections(){const {t}=useTranslation();const query=useConnections();if(!query.data?.connections.length)return null;return <section className='space-y-3'><h2 className='font-semibold'>{t('Extore products')}</h2>{query.data.connections.map((connection)=><Connection key={connection.id} connection={connection} sources={query.data.sources}/>)}</section>}
