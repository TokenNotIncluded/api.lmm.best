/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogTitle, DialogDescription } from '@/components/ui/dialog'
import { storeApi } from './api'
import { StoreError, StoreLoading } from './shared'

export type ExtoreImportResult = { connection: { id: string }; results: {source_id:string;product_id?:string;success:boolean;message:string;last_synced_at?:number}[] }
export function StoreExtoreImport({ callbackUrl, onClose, onImported }: {
 callbackUrl: string | null
 onClose: () => void
 onImported: () => void
}) {
 const {t}=useTranslation()
 const [error,setError]=useState<unknown>(null)
 const [result,setResult]=useState<ExtoreImportResult|null>(null)
 const [busy,setBusy]=useState(true)
 const request=useRef<Promise<unknown>|null>(null)
 const notified=useRef(false)
 const [attempt,setAttempt]=useState(0)
 useEffect(()=>{
  let active=true
  setBusy(true);setError(null)
  if(callbackUrl){
   request.current??=storeApi.extoreCatalog(callbackUrl)
   void request.current.then((value)=>{
    if(!active)return
    const imported=value as ExtoreImportResult
    if(!Array.isArray(imported.results)||!imported.connection?.id)throw new Error('Invalid Extore synchronization response')
    setResult(imported)
    if(!notified.current){notified.current=true;onImported()}
   }).catch((issue:unknown)=>{if(active)setError(issue)}).finally(()=>{if(active)setBusy(false)})
  }else{
   request.current??=storeApi.extoreAuthorize('', '')
   void request.current.then((value)=>{
    if(!active)return
    const authorization=value as {authorization_url:string;base_url:string;redirect_uri:string}
    const url=new URL(authorization.authorization_url)
    if(url.origin!==authorization.base_url||url.protocol!=='https:'||url.username||url.password||url.hash||url.pathname!=='/oauth/authorize'||authorization.redirect_uri!==`${window.location.origin}/store/manage`)throw new Error('Invalid Extore authorization response')
    window.location.assign(url.href)
   }).catch((issue:unknown)=>{if(active){setError(issue);setBusy(false)}})
  }
  return()=>{active=false}
 },[callbackUrl,onImported,attempt])
 return <Dialog open onOpenChange={(open)=>{if(!open)onClose()}}><DialogContent className='max-h-[90svh] overflow-y-auto sm:max-w-xl'><DialogTitle>{t('Import from Extore')}</DialogTitle><DialogDescription>{t('Approve products in Extore. All variants are imported automatically.')}</DialogDescription><StoreError error={error}/>{busy?<StoreLoading/>:result?<div className='space-y-3' role='status'>{result.results.map((item)=><p key={item.source_id}>{item.source_id}: {t(item.message)}</p>)}<Button onClick={onClose}>{t('Back to store')}</Button></div>:<Button onClick={()=>{request.current=null;setAttempt((value)=>value+1)}}>{t('Try again')}</Button>}</DialogContent></Dialog>
}
